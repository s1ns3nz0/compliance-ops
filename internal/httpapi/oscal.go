package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
	"github.com/s1ns3nz0/compliance-ops/internal/store"
)

// DocumentSummary is the legacy safe summary of an OSCAL document.
type DocumentSummary struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Title        string `json:"title"`
	LastModified string `json:"lastModified,omitempty"`
	ControlCount int    `json:"controlCount"`
	Version      string `json:"version,omitempty"`
}

// DocumentDetail is the summary plus the canonical document.
type DocumentDetail struct {
	DocumentSummary
	Document map[string]any `json:"document"`
}

// ControlList is the legacy control search result.
type ControlList struct {
	DocumentID string          `json:"documentId"`
	Items      []oscal.Control `json:"items"`
	Total      int             `json:"total"`
}

// ControlDetail is one control with its document id.
type ControlDetail struct {
	DocumentID string `json:"documentId"`
	oscal.Control
}

func summarize(d oscal.Document) DocumentSummary {
	return DocumentSummary{ID: d.ID, Type: d.Type, Title: d.Title, LastModified: d.LastModified, ControlCount: len(d.Controls), Version: d.Version}
}

type sourceSummary struct {
	ID        string            `json:"id"`
	URL       string            `json:"url"`
	Status    string            `json:"status"`
	Documents []DocumentSummary `json:"documents"`
	ErrorCode string            `json:"errorCode,omitempty"`
}

func safeSourceURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	u.ForceQuery = false
	return u.String()
}

func sourceID(raw string) string {
	u, err := url.Parse(raw)
	if err == nil {
		raw = u.String()
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}

func (a *api) listOscalSources(w http.ResponseWriter, r *http.Request) {
	if !allowedQuery(r) {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "")
		return
	}
	repo, ok := a.deps.Oscal.(oscal.SourceRepository)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, codeOscalSourceUnavailable, "")
		return
	}
	items := make([]sourceSummary, 0, len(repo.Sources()))
	for _, raw := range repo.Sources() {
		row := sourceSummary{ID: sourceID(raw), URL: safeSourceURL(raw), Status: "available", Documents: []DocumentSummary{}}
		docs, err := repo.FetchSource(r.Context(), raw)
		if err != nil {
			row.Status, row.ErrorCode = "unavailable", codeOscalSourceUnavailable
		} else {
			for _, d := range docs {
				row.Documents = append(row.Documents, summarize(d))
			}
		}
		items = append(items, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

const maxOscalUploadBytes int64 = 10 << 20

func readSingleOscalUpload(r *http.Request) ([]byte, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, err
	}
	var data []byte
	files := 0
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if p.FormName() != "file" || p.FileName() == "" {
			p.Close()
			return nil, errors.New("only file is accepted")
		}
		files++
		if files > 1 {
			p.Close()
			return nil, errors.New("exactly one file is required")
		}
		data, err = io.ReadAll(io.LimitReader(p, maxOscalUploadBytes+1))
		p.Close()
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > maxOscalUploadBytes {
			return nil, &http.MaxBytesError{Limit: maxOscalUploadBytes}
		}
	}
	if files != 1 || len(data) == 0 {
		return nil, errors.New("exactly one non-empty file is required")
	}
	return data, nil
}

func (a *api) createOscalUpload(w http.ResponseWriter, r *http.Request) {
	if !allowedQuery(r) {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "")
		return
	}
	data, err := readSingleOscalUpload(r)
	if err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			writeError(w, http.StatusRequestEntityTooLarge, codePayloadTooLarge, "OSCAL file exceeds 10 MiB")
		} else {
			writeError(w, http.StatusBadRequest, codeInvalidBody, "exactly one JSON file is required")
		}
		return
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "file must contain one valid OSCAL JSON object")
		return
	}
	roots := 0
	for _, typ := range oscal.DocumentTypes {
		if _, ok := object[typ].(map[string]any); ok {
			roots++
		}
	}
	if roots != 1 {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "file must contain exactly one OSCAL document")
		return
	}
	docs, err := oscal.Parse(object)
	if err != nil || len(docs) != 1 {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "file must contain one valid OSCAL document")
		return
	}
	sum := sha256.Sum256(data)
	u, err := a.deps.Store.CreateOscalUpload(r.Context(), docs[0], data, hex.EncodeToString(sum[:]), int64(len(data)), actor(r))
	if err != nil {
		var ce *store.ConflictError
		if errors.As(err, &ce) {
			writeJSON(w, http.StatusConflict, map[string]any{"code": codeConflict, "message": "OSCAL file already uploaded", "existing": ce.Existing})
			return
		}
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

func (a *api) listOscalUploads(w http.ResponseWriter, r *http.Request) {
	if !allowedQuery(r) {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "")
		return
	}
	items, err := a.deps.Store.ListOscalUploads(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if items == nil {
		items = []store.OscalUpload{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *api) getOscalUpload(w http.ResponseWriter, r *http.Request) {
	if !allowedQuery(r) {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "")
		return
	}
	u, err := a.deps.Store.GetOscalUpload(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (a *api) deleteOscalUpload(w http.ResponseWriter, r *http.Request) {
	if !allowedQuery(r) {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "")
		return
	}
	err := a.deps.Store.DeleteOscalUpload(r.Context(), r.PathValue("id"), actor(r))
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, codeConflict, "uploaded OSCAL document is referenced by an imported framework")
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// oscalDocuments lists documents from the source, answering 503 on failure.
func (a *api) oscalDocuments(w http.ResponseWriter, r *http.Request) ([]oscal.Document, bool) {
	docs, err := a.deps.Oscal.List(r.Context())
	if err != nil {
		a.logger.Warn("oscal.source_unavailable", "error", err)
		writeError(w, http.StatusServiceUnavailable, codeOscalSourceUnavailable, "")
		return nil, false
	}
	return docs, true
}

// oscalDocument finds one document by id, answering 503/404 on failure.
func (a *api) oscalDocument(w http.ResponseWriter, r *http.Request, id string) (oscal.Document, bool) {
	docs, ok := a.oscalDocuments(w, r)
	if !ok {
		return oscal.Document{}, false
	}
	for _, d := range docs {
		if d.ID == id {
			return d, true
		}
	}
	writeError(w, http.StatusNotFound, codeNotFound, "")
	return oscal.Document{}, false
}

func (a *api) listOscalDocuments(w http.ResponseWriter, r *http.Request) {
	if !allowedQuery(r) {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "")
		return
	}
	docs, ok := a.oscalDocuments(w, r)
	if !ok {
		return
	}
	items := make([]DocumentSummary, 0, len(docs))
	for _, d := range docs {
		items = append(items, summarize(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *api) getOscalDocument(w http.ResponseWriter, r *http.Request) {
	if !allowedQuery(r) {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "")
		return
	}
	doc, ok := a.oscalDocument(w, r, r.PathValue("documentId"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, DocumentDetail{DocumentSummary: summarize(doc), Document: doc.Raw})
}

func (a *api) listOscalControls(w http.ResponseWriter, r *http.Request) {
	if !allowedQuery(r, "q", "limit") {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "")
		return
	}
	limit, ok := intQuery(r, "limit", 50, 1, 100)
	if !ok {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "")
		return
	}
	doc, ok := a.oscalDocument(w, r, r.PathValue("documentId"))
	if !ok {
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	matches := doc.Controls
	if q != "" {
		matches = []oscal.Control{}
		for _, c := range doc.Controls {
			if strings.Contains(strings.ToLower(c.ID+" "+c.Title+" "+c.Text), q) {
				matches = append(matches, c)
			}
		}
	}
	items := matches
	if len(items) > limit {
		items = items[:limit]
	}
	if items == nil {
		items = []oscal.Control{}
	}
	writeJSON(w, http.StatusOK, ControlList{DocumentID: doc.ID, Items: items, Total: len(matches)})
}

func (a *api) getOscalControl(w http.ResponseWriter, r *http.Request) {
	if !allowedQuery(r) {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "")
		return
	}
	doc, ok := a.oscalDocument(w, r, r.PathValue("documentId"))
	if !ok {
		return
	}
	controlID := r.PathValue("controlId")
	for _, c := range doc.Controls {
		if c.ID == controlID {
			writeJSON(w, http.StatusOK, ControlDetail{DocumentID: doc.ID, Control: c})
			return
		}
	}
	writeError(w, http.StatusNotFound, codeNotFound, "")
}
