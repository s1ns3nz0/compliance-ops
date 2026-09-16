package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/s1ns3nz0/compliance-ops/internal/assessment"
	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
	"github.com/s1ns3nz0/compliance-ops/internal/store"
)

type assessmentSummary struct {
	UploadID             string              `json:"uploadId"`
	DocumentID           string              `json:"documentId"`
	Type                 string              `json:"type"`
	Title                string              `json:"title"`
	Version              string              `json:"version,omitempty"`
	LastModified         string              `json:"lastModified,omitempty"`
	UploadedAt           time.Time           `json:"uploadedAt"`
	EffectiveFrameworkID string              `json:"effectiveFrameworkId,omitempty"`
	LinkSource           string              `json:"linkSource"`
	Counts               assessment.CountSet `json:"counts"`
}

func assessmentUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func importRefID(s string) string {
	s = strings.TrimSpace(strings.TrimPrefix(s, "#"))
	if i := strings.LastIndex(s, "#"); i >= 0 {
		s = s[i+1:]
	}
	if strings.Contains(s, "/") {
		s = path.Base(s)
	}
	return s
}
func effectiveFramework(d assessment.Document, u store.OscalUpload, uploads []store.OscalUpload, fws []store.Framework) (string, string) {
	if u.LinkedFrameworkID != "" {
		return u.LinkedFrameworkID, "manual"
	}
	candidates := map[string]bool{}
	for _, ref := range d.ImportRefs {
		id := importRefID(ref)
		for _, f := range fws {
			if id == f.OscalDocumentID {
				candidates[f.ID] = true
			}
		}
		for _, other := range uploads {
			if id == other.DocumentID {
				if other.LinkedFrameworkID != "" {
					candidates[other.LinkedFrameworkID] = true
				}
				if other.ImportedFrameworkID != "" {
					candidates[other.ImportedFrameworkID] = true
				}
			}
		}
	}
	if len(candidates) == 1 {
		for id := range candidates {
			return id, "automatic"
		}
	}
	return "", "unlinked"
}
func allRequirements(ctxReq *http.Request, st store.Store) ([]store.Requirement, error) {
	out := []store.Requirement{}
	for off := 0; ; {
		p, total, err := st.ListRequirements(ctxReq.Context(), store.RequirementFilter{Limit: 200, Offset: off})
		if err != nil {
			return nil, err
		}
		out = append(out, p...)
		off += len(p)
		if len(p) == 0 || off >= total {
			break
		}
	}
	return out, nil
}
func assessmentItems(d *assessment.Document) []*assessment.Item {
	var out []*assessment.Item
	sets := []*[]assessment.Item{&d.ReviewedControls, &d.Subjects, &d.Tasks, &d.Results, &d.Observations, &d.Findings, &d.Risks, &d.Attestations, &d.LogEntries, &d.POAMItems}
	for _, set := range sets {
		for i := range *set {
			out = append(out, &(*set)[i])
		}
	}
	return out
}
func applyMappings(d *assessment.Document, fwID string, reqs []store.Requirement, manual []store.AssessmentItemMapping) {
	byKey := map[string]store.AssessmentItemMapping{}
	for _, m := range manual {
		byKey[m.ItemKey] = m
	}
	d.UnmappedItems = []assessment.Item{}
	for _, it := range assessmentItems(d) {
		if m, ok := byKey[it.ItemKey]; ok {
			it.Mapping = assessment.MappingInfo{Source: "manual", RequirementID: m.RequirementID, PartID: m.PartID}
			for _, r := range reqs {
				if r.ID == m.RequirementID {
					it.Mapping.ControlID = r.ControlID
				}
			}
			continue
		}
		// Exact objective/part references are more specific than a control
		// reference. Keep the candidate sets separate so a control and one of
		// its parts do not make the same requirement look ambiguous.
		exactMatches := map[string]assessment.MappingInfo{}
		controlMatches := map[string]assessment.MappingInfo{}
		for _, r := range reqs {
			if r.FrameworkID != fwID || fwID == "" {
				continue
			}
			for _, cid := range it.ControlIDs {
				if strings.EqualFold(cid, r.ControlID) {
					controlMatches[r.ID] = assessment.MappingInfo{Source: "automatic", RequirementID: r.ID, ControlID: r.ControlID}
				}
			}
			for _, target := range append(append([]string{}, it.ObjectiveIDs...), it.PartIDs...) {
				for _, p := range oscal.TrackableParts(r.Parts, r.ControlID) {
					if target == p.ID {
						exactMatches[r.ID+"\x00"+p.ID] = assessment.MappingInfo{Source: "automatic", RequirementID: r.ID, PartID: p.ID, ControlID: r.ControlID}
					}
				}
			}
		}
		matches := controlMatches
		if len(exactMatches) > 0 {
			matches = exactMatches
		}
		if len(matches) == 1 {
			for _, m := range matches {
				it.Mapping = m
			}
		} else {
			it.Mapping = assessment.MappingInfo{Source: "unmapped"}
			if len(it.ControlIDs)+len(it.ObjectiveIDs)+len(it.PartIDs) > 0 {
				d.UnmappedItems = append(d.UnmappedItems, *it)
			}
		}
	}
}
func (a *api) loadAssessment(r *http.Request, id string) (store.OscalUpload, assessment.Document, string, string, error) {
	u, err := a.deps.Store.GetOscalUpload(r.Context(), id)
	if err != nil {
		return u, assessment.Document{}, "", "", err
	}
	if !assessment.ValidType(u.Type) {
		return u, assessment.Document{}, "", "", store.ErrInvalid
	}
	d, err := assessment.ParseJSON(u.Document)
	if err != nil {
		return u, d, "", "", err
	}
	uploads, err := a.deps.Store.ListOscalUploads(r.Context())
	if err != nil {
		return u, d, "", "", err
	}
	fws, err := a.deps.Store.ListFrameworks(r.Context())
	if err != nil {
		return u, d, "", "", err
	}
	fw, source := effectiveFramework(d, u, uploads, fws)
	reqs, err := allRequirements(r, a.deps.Store)
	if err != nil {
		return u, d, "", "", err
	}
	manual, err := a.deps.Store.ListAssessmentMappings(r.Context(), id)
	if err != nil {
		return u, d, "", "", err
	}
	applyMappings(&d, fw, reqs, manual)
	return u, d, fw, source, nil
}
func summary(u store.OscalUpload, d assessment.Document, fw, source string) assessmentSummary {
	return assessmentSummary{u.ID, u.DocumentID, u.Type, u.Title, u.Version, u.LastModified, u.UploadedAt, fw, source, d.Counts()}
}

func (a *api) listAssessments(w http.ResponseWriter, r *http.Request) {
	if !allowedQuery(r, "type", "frameworkId", "limit", "offset") {
		writeError(w, 400, codeInvalidQuery, "")
		return
	}
	typ := r.URL.Query().Get("type")
	if typ != "" && !assessment.ValidType(typ) {
		writeError(w, 400, codeInvalidQuery, "invalid assessment type")
		return
	}
	limit, ok := intQuery(r, "limit", 50, 1, 200)
	if !ok {
		writeError(w, 400, codeInvalidQuery, "")
		return
	}
	offset, ok := intQuery(r, "offset", 0, 0, 1<<30)
	if !ok {
		writeError(w, 400, codeInvalidQuery, "")
		return
	}
	filterFW := r.URL.Query().Get("frameworkId")
	if filterFW != "" && filterFW != "unlinked" && !assessmentUUID(filterFW) {
		writeError(w, 400, codeInvalidQuery, "frameworkId must be a UUID or unlinked")
		return
	}
	items := []assessmentSummary{}
	uploads, err := a.deps.Store.ListOscalUploads(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	for _, u := range uploads {
		if !assessment.ValidType(u.Type) || (typ != "" && u.Type != typ) {
			continue
		}
		_, d, fw, source, err := a.loadAssessment(r, u.ID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if filterFW == "unlinked" && fw != "" {
			continue
		}
		if filterFW != "" && filterFW != "unlinked" && fw != filterFW {
			continue
		}
		items = append(items, summary(u, d, fw, source))
	}
	total := len(items)
	if offset >= len(items) {
		items = []assessmentSummary{}
	} else {
		end := offset + limit
		if end > len(items) {
			end = len(items)
		}
		items = items[offset:end]
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total})
}
func (a *api) getAssessment(w http.ResponseWriter, r *http.Request) {
	if !allowedQuery(r) {
		writeError(w, 400, codeInvalidQuery, "")
		return
	}
	u, d, fw, source, err := a.loadAssessment(r, r.PathValue("uploadId"))
	if errors.Is(err, store.ErrInvalid) {
		writeError(w, 404, codeNotFound, "")
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var out map[string]any
	raw, _ := json.Marshal(d)
	_ = json.Unmarshal(raw, &out)
	s := summary(u, d, fw, source)
	raw, _ = json.Marshal(s)
	var sm map[string]any
	_ = json.Unmarshal(raw, &sm)
	for k, v := range sm {
		out[k] = v
	}
	writeJSON(w, 200, out)
}

type frameworkLinkBody struct {
	FrameworkID json.RawMessage `json:"frameworkId"`
}

func (a *api) patchAssessmentFramework(w http.ResponseWriter, r *http.Request) {
	var b frameworkLinkBody
	if err := decodeJSONBody(w, r, &b, maxJSONBody); err != nil || len(b.FrameworkID) == 0 {
		writeError(w, 400, codeInvalidBody, "frameworkId is required")
		return
	}
	id := ""
	if string(b.FrameworkID) != "null" {
		if err := json.Unmarshal(b.FrameworkID, &id); err != nil || strings.TrimSpace(id) == "" {
			writeError(w, 400, codeInvalidBody, "frameworkId must be a string or null")
			return
		}
	}
	u, err := a.deps.Store.SetAssessmentFramework(r.Context(), r.PathValue("uploadId"), id, actor(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, u)
}

type mappingBody struct {
	ItemKey       string `json:"itemKey"`
	RequirementID string `json:"requirementId"`
	PartID        string `json:"partId,omitempty"`
}

func (a *api) createAssessmentMapping(w http.ResponseWriter, r *http.Request) {
	var b mappingBody
	if err := decodeJSONBody(w, r, &b, maxJSONBody); err != nil {
		writeError(w, 400, codeInvalidBody, "")
		return
	}
	b.ItemKey = strings.TrimSpace(b.ItemKey)
	b.RequirementID = strings.TrimSpace(b.RequirementID)
	b.PartID = strings.TrimSpace(b.PartID)
	if b.ItemKey == "" || b.RequirementID == "" || len(b.ItemKey) > 1000 || len(b.PartID) > store.MaxPartIDLength {
		writeError(w, 400, codeInvalidBody, "")
		return
	}
	uploadID := r.PathValue("uploadId")
	u, err := a.deps.Store.GetOscalUpload(r.Context(), uploadID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !assessment.ValidType(u.Type) {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "upload is not an assessment document")
		return
	}
	d, err := assessment.ParseJSON(u.Document)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "")
		return
	}
	found := false
	for _, item := range assessmentItems(&d) {
		if item.ItemKey == b.ItemKey {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "itemKey does not exist in the uploaded assessment")
		return
	}
	m, err := a.deps.Store.UpsertAssessmentMapping(r.Context(), store.AssessmentItemMapping{UploadID: uploadID, ItemKey: b.ItemKey, RequirementID: b.RequirementID, PartID: b.PartID}, actor(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 201, m)
}
func (a *api) deleteAssessmentMapping(w http.ResponseWriter, r *http.Request) {
	if !allowedQuery(r) {
		writeError(w, 400, codeInvalidQuery, "")
		return
	}
	if err := a.deps.Store.DeleteAssessmentMapping(r.Context(), r.PathValue("uploadId"), r.PathValue("itemKey"), actor(r)); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(204)
}

type requirementAssessmentItem struct {
	UploadID   string `json:"uploadId"`
	DocumentID string `json:"documentId"`
	Title      string `json:"title"`
	Type       string `json:"type"`
	ItemKey    string `json:"itemKey"`
	Kind       string `json:"kind"`
	Summary    string `json:"summary"`
	Risk       string `json:"risk,omitempty"`
	Status     string `json:"status,omitempty"`
	PartID     string `json:"partId,omitempty"`
}

func (a *api) listRequirementAssessments(w http.ResponseWriter, r *http.Request) {
	if !allowedQuery(r) {
		writeError(w, 400, codeInvalidQuery, "")
		return
	}
	rid := r.PathValue("id")
	if _, err := a.deps.Store.GetRequirement(r.Context(), rid); err != nil {
		writeStoreError(w, err)
		return
	}
	uploads, err := a.deps.Store.ListOscalUploads(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := []requirementAssessmentItem{}
	for _, u := range uploads {
		if !assessment.ValidType(u.Type) {
			continue
		}
		_, d, _, _, err := a.loadAssessment(r, u.ID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		for _, it := range assessmentItems(&d) {
			if it.Mapping.RequirementID == rid {
				out = append(out, requirementAssessmentItem{u.ID, u.DocumentID, u.Title, u.Type, it.ItemKey, it.Kind, it.Summary, it.Risk, it.Status, it.Mapping.PartID})
			}
		}
	}
	writeJSON(w, 200, map[string]any{"items": out, "total": len(out)})
}
