package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
	"github.com/s1ns3nz0/compliance-ops/internal/store"
)

const maxJSONBody = 1 << 20

type importRequest struct {
	DocumentID string `json:"documentId"`
	UploadID   string `json:"uploadId"`
}

func importable(typ string) bool { return typ == "catalog" || typ == "profile" }

const profileResolutionMessage = "profile resolution requires an available imported catalog"

func resolveImportDocument(doc oscal.Document, catalogs []oscal.Document) (oscal.Document, error) {
	if doc.Type != "profile" {
		return doc, nil
	}
	return oscal.ResolveProfile(doc, catalogs)
}

func (a *api) withUploadedCatalogs(r *http.Request, candidates []oscal.Document) []oscal.Document {
	catalogs := []oscal.Document{}
	for _, doc := range candidates {
		if doc.Type == "catalog" {
			catalogs = append(catalogs, doc)
		}
	}
	uploads, err := a.deps.Store.ListOscalUploads(r.Context())
	if err != nil {
		return catalogs
	}
	for _, upload := range uploads {
		if upload.Type != "catalog" {
			continue
		}
		detail, err := a.deps.Store.GetOscalUpload(r.Context(), upload.ID)
		if err != nil {
			continue
		}
		docs, err := oscal.ParseJSON(detail.Document)
		if err == nil && len(docs) == 1 {
			catalogs = append(catalogs, docs[0])
		}
	}
	return catalogs
}

func (a *api) availableCatalogs(r *http.Request) []oscal.Document {
	if a.deps.Oscal == nil {
		return a.withUploadedCatalogs(r, nil)
	}
	docs, err := a.deps.Oscal.List(r.Context())
	if err != nil {
		return a.withUploadedCatalogs(r, nil)
	}
	return a.withUploadedCatalogs(r, docs)
}

// importFrameworks imports every catalog/profile (or one document by id).
func (a *api) importFrameworks(w http.ResponseWriter, r *http.Request) {
	var req importRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSONBody(w, r, &req, maxJSONBody); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, codeInvalidBody, "body must be a JSON object with an optional documentId")
			return
		}
	}
	req.DocumentID = strings.TrimSpace(req.DocumentID)
	req.UploadID = strings.TrimSpace(req.UploadID)
	if req.DocumentID != "" && req.UploadID != "" {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "exactly one of documentId or uploadId may be supplied")
		return
	}
	act := actor(r)
	items := []store.ImportResult{}
	if req.UploadID != "" {
		u, err := a.deps.Store.GetOscalUpload(r.Context(), req.UploadID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if !importable(u.Type) {
			writeError(w, http.StatusBadRequest, codeInvalidBody, "only catalog and profile uploads create requirements")
			return
		}
		docs, err := oscal.ParseJSON(u.Document)
		if err != nil || len(docs) != 1 {
			writeError(w, http.StatusInternalServerError, codeInternal, "")
			return
		}
		doc, err := resolveImportDocument(docs[0], a.availableCatalogs(r))
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidBody, profileResolutionMessage)
			return
		}
		res, err := a.deps.Store.ImportUploadedFramework(r.Context(), req.UploadID, doc, act)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": []store.ImportResult{res}})
		return
	}
	docs, ok := a.oscalDocuments(w, r)
	if !ok {
		return
	}
	catalogs := a.withUploadedCatalogs(r, docs)
	if req.DocumentID != "" {
		found := false
		for _, d := range docs {
			if d.ID != req.DocumentID {
				continue
			}
			found = true
			if !importable(d.Type) {
				writeError(w, http.StatusBadRequest, codeInvalidBody, "only catalog and profile documents can be imported")
				return
			}
			doc, err := resolveImportDocument(d, catalogs)
			if err != nil {
				writeError(w, http.StatusBadRequest, codeInvalidBody, profileResolutionMessage)
				return
			}
			res, err := a.deps.Store.ImportFramework(r.Context(), doc, act)
			if err != nil {
				writeStoreError(w, err)
				return
			}
			items = append(items, res)
		}
		if !found {
			writeError(w, http.StatusNotFound, codeNotFound, "")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return
	}
	importDocs := make([]oscal.Document, 0, len(docs))
	for _, d := range docs {
		if !importable(d.Type) {
			continue
		}
		doc, err := resolveImportDocument(d, catalogs)
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidBody, profileResolutionMessage)
			return
		}
		importDocs = append(importDocs, doc)
	}
	for _, doc := range importDocs {
		res, err := a.deps.Store.ImportFramework(r.Context(), doc, act)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		items = append(items, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *api) listFrameworks(w http.ResponseWriter, r *http.Request) {
	if !allowedQuery(r) {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "")
		return
	}
	items, err := a.deps.Store.ListFrameworks(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if items == nil {
		items = []store.Framework{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *api) getFramework(w http.ResponseWriter, r *http.Request) {
	fw, err := a.deps.Store.GetFramework(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fw)
}

type frameworkPatchBody struct {
	ShortName *string `json:"shortName"`
}

// patchFramework updates the user-editable short name.
func (a *api) patchFramework(w http.ResponseWriter, r *http.Request) {
	var body frameworkPatchBody
	if err := decodeJSONBody(w, r, &body, maxJSONBody); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "body must be a JSON object with shortName")
		return
	}
	if body.ShortName == nil {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "shortName is required")
		return
	}
	shortName := strings.TrimSpace(*body.ShortName)
	if !store.ValidShortName(shortName) {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "shortName must be 1..60 characters")
		return
	}
	fw, err := a.deps.Store.UpdateFramework(r.Context(), r.PathValue("id"), shortName, actor(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fw)
}

// RequirementDetail is a requirement with its structured parts and
// parameters, per-part tracking and linked evidence.
type RequirementDetail struct {
	store.Requirement
	// Parts and Params shadow the embedded omitempty fields so the detail
	// always renders arrays.
	Parts  []oscal.Part  `json:"parts"`
	Params []oscal.Param `json:"params"`
	// References shadows the embedded omitempty field: the detail always
	// renders the resolved external references as an array.
	References []oscal.Reference `json:"references"`
	// TrackableParts are the statement items (or the statement itself) with
	// their tracking merged in; parts without a stored row read as planned.
	TrackableParts []TrackablePart  `json:"trackableParts"`
	Evidence       []store.Evidence `json:"evidence"`
}

// TrackablePart is one trackable statement part with its tracking values.
type TrackablePart struct {
	PartID      string                `json:"partId"`
	Label       string                `json:"label,omitempty"`
	Prose       string                `json:"prose,omitempty"`
	Status      string                `json:"status"`
	Owner       string                `json:"owner"`
	DueDate     *time.Time            `json:"dueDate,omitempty"`
	Description string                `json:"description"`
	Scopes      []store.ScopeCategory `json:"scopes"`
	// UpdatedAt is omitted while the part has no stored row.
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
}

// mergeTrackableParts pairs the requirement's trackable parts with their
// stored rows, in part order.
func mergeTrackableParts(req store.Requirement, rows []store.PartTracking) []TrackablePart {
	byID := make(map[string]store.PartTracking, len(rows))
	for _, pt := range rows {
		byID[pt.PartID] = pt
	}
	parts := oscal.TrackableParts(req.Parts, req.ControlID)
	out := make([]TrackablePart, 0, len(parts))
	for _, p := range parts {
		tp := TrackablePart{PartID: p.ID, Label: p.Label, Prose: p.Prose, Status: store.StatusPlanned, Scopes: []store.ScopeCategory{}}
		if pt, ok := byID[p.ID]; ok {
			tp.Status, tp.Owner, tp.DueDate, tp.Description, tp.Scopes = pt.Status, pt.Owner, pt.DueDate, pt.Description, pt.Scopes
			u := pt.UpdatedAt
			tp.UpdatedAt = &u
		}
		out = append(out, tp)
	}
	return out
}

// trackablePart returns the merged view of one part of the requirement.
func trackablePart(req store.Requirement, pt store.PartTracking) TrackablePart {
	for _, tp := range mergeTrackableParts(req, []store.PartTracking{pt}) {
		if tp.PartID == pt.PartID {
			return tp
		}
	}
	u := pt.UpdatedAt
	return TrackablePart{PartID: pt.PartID, Status: pt.Status, Owner: pt.Owner, DueDate: pt.DueDate, Description: pt.Description, Scopes: pt.Scopes, UpdatedAt: &u}
}

func (a *api) listRequirements(w http.ResponseWriter, r *http.Request) {
	if !allowedQuery(r, "frameworkId", "status", "q", "scopeCategoryId", "limit", "offset") {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "unknown query parameter")
		return
	}
	q := r.URL.Query()
	f := store.RequirementFilter{FrameworkID: q.Get("frameworkId"), Status: q.Get("status"), Query: q.Get("q")}
	for _, raw := range q["scopeCategoryId"] {
		for _, id := range strings.Split(raw, ",") {
			id = strings.TrimSpace(id)
			if !store.ValidUUID(id) {
				writeError(w, http.StatusBadRequest, codeInvalidQuery, "scopeCategoryId must contain UUIDs")
				return
			}
			f.ScopeCategoryIDs = append(f.ScopeCategoryIDs, id)
		}
	}
	if f.Status != "" && !store.ValidStatus(f.Status) {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "invalid status")
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
	items, total, err := a.deps.Store.ListRequirements(r.Context(), f)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if items == nil {
		items = []store.Requirement{}
	}
	for i := range items {
		// The list omits the parts tree, params and references to keep the
		// payload small; the detail route returns them.
		items[i].Parts, items[i].Params, items[i].References = nil, nil, nil
		if items[i].Related == nil {
			items[i].Related = []string{}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (a *api) getRequirement(w http.ResponseWriter, r *http.Request) {
	req, err := a.deps.Store.GetRequirement(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	ev, _, err := a.deps.Store.ListEvidence(r.Context(), store.EvidenceFilter{RequirementID: req.ID, Limit: 200})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if ev == nil {
		ev = []store.Evidence{}
	}
	rows, err := a.deps.Store.GetPartTracking(r.Context(), req.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	parts := req.Parts
	if parts == nil {
		parts = []oscal.Part{}
	}
	params := req.Params
	if params == nil {
		params = []oscal.Param{}
	}
	if req.Related == nil {
		req.Related = []string{}
	}
	refs := req.References
	if refs == nil {
		refs = []oscal.Reference{}
	}
	writeJSON(w, http.StatusOK, RequirementDetail{Requirement: req, Parts: parts, Params: params, References: refs, TrackableParts: mergeTrackableParts(req, rows), Evidence: ev})
}

// ---- part tracking ---------------------------------------------------------

// partPatchBody distinguishes absent, null, and present dueDate.
type partPatchBody struct {
	Status            *string         `json:"status"`
	Owner             *string         `json:"owner"`
	DueDate           json.RawMessage `json:"dueDate"`
	Description       *string         `json:"description"`
	ScopeCategoryIDs  json.RawMessage `json:"scopeCategoryIds"`
	ExpectedUpdatedAt json.RawMessage `json:"expectedUpdatedAt"`
}

func parseUpdatedAtPrecondition(raw json.RawMessage, allowNull bool) (store.UpdatedAtPrecondition, bool) {
	if len(raw) == 0 {
		return store.UpdatedAtPrecondition{}, true
	}
	if string(raw) == "null" {
		if allowNull {
			return store.UpdatedAtPrecondition{State: store.UpdatedAtNull}, true
		}
		return store.UpdatedAtPrecondition{}, false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return store.UpdatedAtPrecondition{}, false
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return store.UpdatedAtPrecondition{}, false
	}
	return store.UpdatedAtPrecondition{State: store.UpdatedAtValue, Value: parsed}, true
}

// parseDueDate turns the raw dueDate field into patch values: absent →
// unchanged, null → clear, "YYYY-MM-DD" → set. Returns false when malformed.
func parseDueDate(raw json.RawMessage) (due *time.Time, clear bool, ok bool) {
	if len(raw) == 0 {
		return nil, false, true
	}
	if string(raw) == "null" {
		return nil, true, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false, false
	}
	d, err := parseDate(s)
	if err != nil {
		return nil, false, false
	}
	return &d, false, true
}

// PartPatchResult is the response of PATCH /v1/requirements/{id}/parts/{partId}.
type PartPatchResult struct {
	Part TrackablePart `json:"part"`
	// Requirement carries the recomputed derived values (parts/params omitted).
	Requirement store.Requirement `json:"requirement"`
}

func (a *api) patchPart(w http.ResponseWriter, r *http.Request) {
	var body partPatchBody
	if err := decodeJSONBody(w, r, &body, maxJSONBody); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "body must be a JSON object with optional status, owner, dueDate, description, scopeCategoryIds and expectedUpdatedAt")
		return
	}
	expected, ok := parseUpdatedAtPrecondition(body.ExpectedUpdatedAt, true)
	if !ok {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "expectedUpdatedAt must be an RFC3339 string or null")
		return
	}
	if body.Status != nil && !store.ValidStatus(*body.Status) {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "invalid status")
		return
	}
	if body.Owner != nil {
		trimmed := strings.TrimSpace(*body.Owner)
		if len(trimmed) > store.MaxOwnerLength {
			writeError(w, http.StatusBadRequest, codeInvalidBody, "owner must be at most 200 characters")
			return
		}
		body.Owner = &trimmed
	}
	if body.Description != nil && len(*body.Description) > store.MaxPartDescription {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "description must be at most 20000 characters")
		return
	}
	var scopeIDs *[]string
	if len(body.ScopeCategoryIDs) > 0 {
		if string(body.ScopeCategoryIDs) == "null" {
			writeError(w, http.StatusBadRequest, codeInvalidBody, "scopeCategoryIds must be an array")
			return
		}
		var ids []string
		if err := json.Unmarshal(body.ScopeCategoryIDs, &ids); err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidBody, "scopeCategoryIds must be an array of UUIDs")
			return
		}
		for _, id := range ids {
			if !store.ValidUUID(id) {
				writeError(w, http.StatusBadRequest, codeInvalidBody, "scopeCategoryIds must be an array of UUIDs")
				return
			}
		}
		scopeIDs = &ids
	}
	due, clear, ok := parseDueDate(body.DueDate)
	if !ok {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "dueDate must be a YYYY-MM-DD string or null")
		return
	}
	partID := r.PathValue("partId")
	if partID == "" || len(partID) > store.MaxPartIDLength {
		writeError(w, http.StatusNotFound, codeNotFound, "")
		return
	}
	patch := store.PartPatch{Status: body.Status, Owner: body.Owner, DueDate: due, ClearDueDate: clear, Description: body.Description, ScopeCategoryIDs: scopeIDs, ExpectedUpdatedAt: expected}
	pt, err := a.deps.Store.UpsertPartTracking(r.Context(), r.PathValue("id"), partID, patch, actor(r))
	if err != nil {
		if errors.Is(err, store.ErrPartNotTrackable) {
			writeError(w, http.StatusNotFound, codeNotFound, "partId is not a trackable part of this requirement")
			return
		}
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, codeConflict, "resource was modified")
			return
		}
		if errors.Is(err, store.ErrInvalid) {
			writeError(w, http.StatusBadRequest, codeInvalidBody, "at least one scope category is required for implemented parts")
			return
		}
		writeStoreError(w, err)
		return
	}
	req, err := a.deps.Store.GetRequirement(r.Context(), pt.RequirementID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	part := trackablePart(req, pt)
	req.Parts, req.Params, req.References = nil, nil, nil
	if req.Related == nil {
		req.Related = []string{}
	}
	writeJSON(w, http.StatusOK, PartPatchResult{Part: part, Requirement: req})
}

// requirementPatchBody distinguishes absent, null, and present
// statusOverride. status/owner/dueDate are decoded only to reject them with a
// helpful message (they are derived from parts).
type requirementPatchBody struct {
	Notes             *string         `json:"notes"`
	StatusOverride    json.RawMessage `json:"statusOverride"`
	Status            json.RawMessage `json:"status"`
	Owner             json.RawMessage `json:"owner"`
	DueDate           json.RawMessage `json:"dueDate"`
	ExpectedUpdatedAt json.RawMessage `json:"expectedUpdatedAt"`
}

func (a *api) patchRequirement(w http.ResponseWriter, r *http.Request) {
	var body requirementPatchBody
	if err := decodeJSONBody(w, r, &body, maxJSONBody); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "body must be a JSON object with notes, statusOverride and optional expectedUpdatedAt")
		return
	}
	expected, ok := parseUpdatedAtPrecondition(body.ExpectedUpdatedAt, false)
	if !ok {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "expectedUpdatedAt must be an RFC3339 string")
		return
	}
	if len(body.Status) > 0 || len(body.Owner) > 0 || len(body.DueDate) > 0 {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "status, owner and dueDate are derived from parts")
		return
	}
	patch := store.RequirementPatch{Notes: body.Notes, ExpectedUpdatedAt: expected}
	if body.Notes != nil && len(*body.Notes) > store.MaxNotesLength {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "notes must be at most 10000 characters")
		return
	}
	if len(body.StatusOverride) > 0 {
		if string(body.StatusOverride) == "null" {
			patch.ClearStatusOverride = true
		} else {
			var s string
			if err := json.Unmarshal(body.StatusOverride, &s); err != nil || !store.ValidStatus(s) {
				writeError(w, http.StatusBadRequest, codeInvalidBody, "statusOverride must be an implementation status or null")
				return
			}
			patch.StatusOverride = &s
		}
	}
	updated, err := a.deps.Store.UpdateRequirement(r.Context(), r.PathValue("id"), patch, actor(r))
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, codeConflict, "resource was modified")
			return
		}
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (a *api) listAudit(w http.ResponseWriter, r *http.Request) {
	if !allowedQuery(r, "limit") {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "unknown query parameter")
		return
	}
	limit, ok := intQuery(r, "limit", 50, 1, 200)
	if !ok {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "limit must be an integer in 1..200")
		return
	}
	items, err := a.deps.Store.ListAudit(r.Context(), limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if items == nil {
		items = []store.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
