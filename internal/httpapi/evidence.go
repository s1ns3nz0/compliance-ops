package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/s1ns3nz0/compliance-ops/internal/blob"
	"github.com/s1ns3nz0/compliance-ops/internal/store"
)

const (
	maxTextField    = 64 << 10
	maxTitleLength  = 200
	defaultMimeType = "application/octet-stream"
)

type uploadFields struct {
	title, description, validFrom, validUntil, url string
	requirementIDs                                 []string
}

// evidenceJSONBody is the application/json variant of POST /v1/evidence,
// used for link evidence.
type evidenceJSONBody struct {
	Kind           string   `json:"kind"`
	URL            string   `json:"url"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	ValidFrom      string   `json:"validFrom"`
	ValidUntil     string   `json:"validUntil"`
	RequirementIDs []string `json:"requirementIds"`
}

// splitIDs appends the non-empty, trimmed, comma-separated ids in value.
func splitIDs(dst []string, value string) []string {
	for _, id := range strings.Split(value, ",") {
		if id = strings.TrimSpace(id); id != "" {
			dst = append(dst, id)
		}
	}
	return dst
}

// buildEvidence validates the shared text fields and returns the evidence
// skeleton (kind/file fields unset) or a client-facing error message.
func buildEvidence(f uploadFields) (store.Evidence, string) {
	if f.title == "" || len(f.title) > maxTitleLength {
		return store.Evidence{}, "title is required and must be at most 200 characters"
	}
	if len(f.description) > 10000 {
		return store.Evidence{}, "description must be at most 10000 characters"
	}
	if len(f.requirementIDs) == 0 {
		return store.Evidence{}, "at least one requirementIds value is required"
	}
	ev := store.Evidence{Title: f.title, Description: f.description, RequirementIDs: dedupe(f.requirementIDs)}
	if f.validFrom != "" {
		d, err := parseDate(f.validFrom)
		if err != nil {
			return store.Evidence{}, "validFrom must be YYYY-MM-DD"
		}
		ev.ValidFrom = &d
	}
	if f.validUntil != "" {
		d, err := parseDate(f.validUntil)
		if err != nil {
			return store.Evidence{}, "validUntil must be YYYY-MM-DD"
		}
		ev.ValidUntil = &d
	}
	if ev.ValidFrom != nil && ev.ValidUntil != nil && ev.ValidUntil.Before(*ev.ValidFrom) {
		return store.Evidence{}, "validUntil must not be before validFrom"
	}
	return ev, ""
}

// isJSONRequest reports whether the request body is declared as JSON.
func isJSONRequest(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == "application/json"
}

// createEvidence accepts either a multipart upload (file evidence, or link
// evidence when a `url` field replaces the file part) or a JSON body
// describing link evidence.
func (a *api) createEvidence(w http.ResponseWriter, r *http.Request) {
	if isJSONRequest(r) {
		a.createLinkEvidenceJSON(w, r)
		return
	}
	a.createEvidenceMultipart(w, r)
}

func (a *api) createLinkEvidenceJSON(w http.ResponseWriter, r *http.Request) {
	var body evidenceJSONBody
	if err := decodeJSONBody(w, r, &body, maxJSONBody); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "body must be a JSON object with kind, url, title, requirementIds")
		return
	}
	if body.Kind != store.EvidenceKindLink {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "JSON evidence bodies must have kind \"link\"; upload files as multipart/form-data")
		return
	}
	fields := uploadFields{
		title: strings.TrimSpace(body.Title), description: strings.TrimSpace(body.Description),
		validFrom: strings.TrimSpace(body.ValidFrom), validUntil: strings.TrimSpace(body.ValidUntil),
		url: strings.TrimSpace(body.URL),
	}
	for _, id := range body.RequirementIDs {
		if id = strings.TrimSpace(id); id != "" {
			fields.requirementIDs = append(fields.requirementIDs, id)
		}
	}
	a.finishLinkEvidence(w, r, fields)
}

// finishLinkEvidence validates and stores link evidence.
func (a *api) finishLinkEvidence(w http.ResponseWriter, r *http.Request, fields uploadFields) {
	if !store.ValidEvidenceURL(fields.url) {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "url must be an http(s) URL with a host, no credentials, at most 2048 characters")
		return
	}
	ev, msg := buildEvidence(fields)
	if msg != "" {
		writeError(w, http.StatusBadRequest, codeInvalidBody, msg)
		return
	}
	ev.Kind, ev.URL = store.EvidenceKindLink, fields.url
	created, err := a.deps.Store.CreateEvidence(r.Context(), ev, actor(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// createEvidenceMultipart streams a multipart upload: the file part goes to a
// temp file while SHA-256 is computed, then to the blob store (skipped when
// the digest already exists) and finally to the tracking store.
func (a *api) createEvidenceMultipart(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, a.deps.MaxUploadBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "multipart/form-data or application/json body required")
		return
	}

	tmp, err := os.CreateTemp("", "evidence-*")
	if err != nil {
		a.logger.Error("evidence.tempfile", "error", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "")
		return
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	var (
		fields      uploadFields
		haveFile    bool
		fileName    string
		contentType string
		size        int64
		hasher      = sha256.New()
	)
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if tooLarge(err) {
				writeError(w, http.StatusRequestEntityTooLarge, codePayloadTooLarge, "upload exceeds the size limit")
				return
			}
			writeError(w, http.StatusBadRequest, codeInvalidBody, "malformed multipart body")
			return
		}
		name := part.FormName()
		if name == "file" {
			if haveFile {
				_ = part.Close()
				writeError(w, http.StatusBadRequest, codeInvalidBody, "only one file part is allowed")
				return
			}
			haveFile = true
			fileName = sanitizeFileName(part.FileName())
			contentType = partContentType(part.Header.Get("Content-Type"))
			n, err := io.Copy(tmp, io.TeeReader(part, hasher))
			_ = part.Close()
			if err != nil {
				if tooLarge(err) {
					writeError(w, http.StatusRequestEntityTooLarge, codePayloadTooLarge, "upload exceeds the size limit")
					return
				}
				writeError(w, http.StatusBadRequest, codeInvalidBody, "malformed multipart body")
				return
			}
			size = n
			continue
		}
		value, err := readTextPart(part)
		if err != nil {
			if tooLarge(err) {
				writeError(w, http.StatusRequestEntityTooLarge, codePayloadTooLarge, "upload exceeds the size limit")
				return
			}
			writeError(w, http.StatusBadRequest, codeInvalidBody, "form field too large or malformed")
			return
		}
		switch name {
		case "title":
			fields.title = strings.TrimSpace(value)
		case "description":
			fields.description = strings.TrimSpace(value)
		case "validFrom":
			fields.validFrom = strings.TrimSpace(value)
		case "validUntil":
			fields.validUntil = strings.TrimSpace(value)
		case "url":
			fields.url = strings.TrimSpace(value)
		case "requirementIds":
			fields.requirementIDs = splitIDs(fields.requirementIDs, value)
		default:
			writeError(w, http.StatusBadRequest, codeInvalidBody, "unknown form field")
			return
		}
	}

	if haveFile && fields.url != "" {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "provide either a file or a url, not both")
		return
	}
	if !haveFile {
		if fields.url == "" {
			writeError(w, http.StatusBadRequest, codeInvalidBody, "file or url is required")
			return
		}
		a.finishLinkEvidence(w, r, fields)
		return
	}
	if size == 0 {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "file must not be empty")
		return
	}
	ev, msg := buildEvidence(fields)
	if msg != "" {
		writeError(w, http.StatusBadRequest, codeInvalidBody, msg)
		return
	}
	ev.Kind = store.EvidenceKindFile
	ev.FileName, ev.ContentType, ev.SizeBytes, ev.Sha256 = fileName, contentType, size, hex.EncodeToString(hasher.Sum(nil))
	// Validate requirement ids before touching blob storage.
	for _, id := range ev.RequirementIDs {
		if _, err := a.deps.Store.GetRequirement(r.Context(), id); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	unlock, err := a.deps.Store.AcquireEvidenceDigestLock(r.Context(), ev.Sha256)
	if err != nil {
		a.logger.Error("evidence.digest_lock", "error", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "")
		return
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := unlock(ctx); err != nil {
			a.logger.Error("evidence.digest_unlock", "error", err)
		}
	}()

	exists, err := a.deps.Blobs.Exists(r.Context(), ev.Sha256)
	if err != nil {
		a.logger.Error("blob.exists", "error", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "")
		return
	}
	if !exists {
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			a.logger.Error("evidence.tempfile", "error", err)
			writeError(w, http.StatusInternalServerError, codeInternal, "")
			return
		}
		if err := a.deps.Blobs.Put(r.Context(), ev.Sha256, tmp, size, contentType); err != nil {
			a.logger.Error("blob.put", "error", err)
			writeError(w, http.StatusInternalServerError, codeInternal, "")
			return
		}
	}
	created, err := a.deps.Store.CreateEvidence(r.Context(), ev, actor(r))
	if err != nil {
		// Compensation is best-effort. The digest lock keeps Exists, Put,
		// metadata creation, and this conditional cleanup serialized across all
		// application replicas, so a successful peer cannot lose its blob.
		if !exists {
			if deleteErr := a.deps.Blobs.Delete(r.Context(), ev.Sha256); deleteErr != nil {
				a.logger.Error("blob.compensate_delete", "error", deleteErr)
			}
		}
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func tooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

func readTextPart(part io.ReadCloser) (string, error) {
	defer part.Close()
	b, err := io.ReadAll(io.LimitReader(part, maxTextField+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxTextField {
		return "", errors.New("field too large")
	}
	return string(b), nil
}

func partContentType(ct string) string {
	ct = strings.TrimSpace(ct)
	if ct == "" {
		return defaultMimeType
	}
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return defaultMimeType
	}
	return mime.FormatMediaType(mt, params)
}

// sanitizeFileName keeps a display-safe base name for Content-Disposition.
func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '"' || r == '\\' || r == ';' || unicode.IsControl(r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" || out == "." || out == ".." {
		return "evidence"
	}
	if len(out) > 255 {
		out = out[:255]
	}
	return out
}

// contentDisposition builds `attachment; filename="<sanitized>"`. The quoted
// filename is always present (RFC 6266 §4.1). CR/LF, quotes, backslashes and
// path separators are stripped from the quoted form; when the name contains
// non-ASCII bytes an RFC 8187 `filename*=UTF-8”<percent-encoded>` parameter is
// appended so browsers can restore the original name.
func contentDisposition(fileName string) string {
	name := sanitizeFileName(fileName)
	var quoted strings.Builder
	ascii := true
	for _, r := range name {
		switch {
		case r == '\r' || r == '\n' || r == '"' || r == '\\' || r == '/':
			continue
		case r > unicode.MaxASCII:
			ascii = false
			continue
		}
		quoted.WriteRune(r)
	}
	q := strings.TrimSpace(quoted.String())
	if q == "" {
		q = "evidence"
	}
	out := `attachment; filename="` + q + `"`
	if !ascii {
		out += "; filename*=UTF-8''" + url.PathEscape(strings.NewReplacer("\r", "", "\n", "", `"`, "", `\`, "", "/", "").Replace(name))
	}
	return out
}

func dedupe(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func (a *api) listEvidence(w http.ResponseWriter, r *http.Request) {
	if !allowedQuery(r, "requirementId", "kind", "q", "limit", "offset") {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "unknown query parameter")
		return
	}
	q := r.URL.Query()
	f := store.EvidenceFilter{RequirementID: q.Get("requirementId"), Kind: q.Get("kind"), Query: q.Get("q")}
	if f.Kind != "" && !store.ValidEvidenceKind(f.Kind) {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "invalid kind")
		return
	}
	var ok bool
	if f.Limit, ok = intQuery(r, "limit", 0, 0, 1<<30); !ok {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "limit must be a non-negative integer")
		return
	}
	if f.Offset, ok = intQuery(r, "offset", 0, 0, 1<<30); !ok {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "offset must be a non-negative integer")
		return
	}
	items, total, err := a.deps.Store.ListEvidence(r.Context(), f)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if items == nil {
		items = []store.Evidence{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (a *api) getEvidence(w http.ResponseWriter, r *http.Request) {
	ev, err := a.deps.Store.GetEvidence(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ev)
}

func (a *api) downloadEvidence(w http.ResponseWriter, r *http.Request) {
	ev, err := a.deps.Store.GetEvidence(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if ev.Kind == store.EvidenceKindLink {
		writeError(w, http.StatusNotFound, codeNotFound, "link evidence has no file")
		return
	}
	rc, err := a.deps.Blobs.Get(r.Context(), ev.Sha256)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, "stored file is unavailable")
			return
		}
		a.logger.Error("blob.get", "error", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "")
		return
	}
	defer rc.Close()
	ct := ev.ContentType
	if ct == "" {
		ct = defaultMimeType
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("Content-Length", strconv.FormatInt(ev.SizeBytes, 10))
	h.Set("Content-Disposition", contentDisposition(ev.FileName))
	h.Set("X-Content-Sha256", ev.Sha256)
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, rc); err != nil {
		a.logger.Warn("evidence.download_interrupted", "error", err)
	}
}
