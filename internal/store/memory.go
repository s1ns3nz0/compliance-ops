package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
)

// Memory is an in-memory Store for tests and local experiments.
type Memory struct {
	mu           sync.Mutex
	digestMu     sync.Mutex
	digestLocks  map[string]*memoryDigestLock
	Now          func() time.Time
	frameworks   map[string]*Framework
	requirements map[string]*Requirement
	evidence     map[string]*Evidence
	uploads      map[string]*OscalUpload
	mappings     map[string]map[string]*AssessmentItemMapping
	// parts holds part tracking rows keyed by requirement id then part id.
	parts           map[string]map[string]*PartTracking
	scopeCategories map[string]*ScopeCategory
	audit           []AuditEntry
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{Now: func() time.Time { return time.Now().UTC() }, digestLocks: map[string]*memoryDigestLock{}, frameworks: map[string]*Framework{}, requirements: map[string]*Requirement{}, evidence: map[string]*Evidence{}, uploads: map[string]*OscalUpload{}, mappings: map[string]map[string]*AssessmentItemMapping{}, parts: map[string]map[string]*PartTracking{}, scopeCategories: map[string]*ScopeCategory{}}
}

type memoryDigestLock struct {
	token chan struct{}
	refs  int
}

func (m *Memory) AcquireEvidenceDigestLock(ctx context.Context, digest string) (func(context.Context) error, error) {
	if _, err := evidenceDigestLockKey(digest); err != nil {
		return nil, err
	}
	digest = strings.ToLower(digest)
	m.digestMu.Lock()
	l := m.digestLocks[digest]
	if l == nil {
		l = &memoryDigestLock{token: make(chan struct{}, 1)}
		l.token <- struct{}{}
		m.digestLocks[digest] = l
	}
	l.refs++
	m.digestMu.Unlock()

	select {
	case <-ctx.Done():
		m.releaseDigestRef(digest, l)
		return nil, ctx.Err()
	case <-l.token:
	}
	var once sync.Once
	return func(context.Context) error {
		once.Do(func() {
			l.token <- struct{}{}
			m.releaseDigestRef(digest, l)
		})
		return nil
	}, nil
}

func (m *Memory) releaseDigestRef(digest string, l *memoryDigestLock) {
	m.digestMu.Lock()
	defer m.digestMu.Unlock()
	l.refs--
	if l.refs == 0 {
		delete(m.digestLocks, digest)
	}
}

func cloneUpload(u OscalUpload, detail bool) OscalUpload {
	c := u
	if detail {
		c.Document = append([]byte(nil), u.Document...)
	} else {
		c.Document = nil
	}
	return c
}

func (m *Memory) CreateOscalUpload(_ context.Context, d oscal.Document, raw []byte, sha string, size int64, actor string) (OscalUpload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.uploads {
		if u.Sha256 == sha {
			return OscalUpload{}, &ConflictError{Existing: cloneUpload(*u, false)}
		}
	}
	u := OscalUpload{ID: newID(), DocumentID: d.ID, Type: d.Type, Title: d.Title, Version: d.Version, LastModified: d.LastModified, Sha256: sha, SizeBytes: size, UploadedBy: actor, UploadedAt: m.Now(), Document: append([]byte(nil), raw...)}
	m.uploads[u.ID] = &u
	m.log(actor, "oscal.upload", "oscal_upload", u.ID, map[string]any{"documentId": u.DocumentID, "type": u.Type, "title": u.Title, "sha256": u.Sha256, "sizeBytes": u.SizeBytes})
	return cloneUpload(u, false), nil
}

func (m *Memory) ListOscalUploads(context.Context) ([]OscalUpload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]OscalUpload, 0, len(m.uploads))
	for _, u := range m.uploads {
		out = append(out, cloneUpload(*u, false))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UploadedAt.After(out[j].UploadedAt) })
	return out, nil
}

func (m *Memory) GetOscalUpload(_ context.Context, id string) (OscalUpload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.uploads[id]
	if !ok {
		return OscalUpload{}, ErrNotFound
	}
	return cloneUpload(*u, true), nil
}

func (m *Memory) DeleteOscalUpload(_ context.Context, id, actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.uploads[id]
	if !ok {
		return ErrNotFound
	}
	if u.ImportedFrameworkID != "" {
		return ErrConflict
	}
	m.log(actor, "oscal.delete", "oscal_upload", id, map[string]any{"documentId": u.DocumentID, "sha256": u.Sha256})
	delete(m.uploads, id)
	return nil
}

func (m *Memory) MarkOscalUploadImported(_ context.Context, id, frameworkID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.uploads[id]
	if !ok {
		return ErrNotFound
	}
	if u.ImportedFrameworkID == "" {
		u.ImportedFrameworkID = frameworkID
	}
	return nil
}

func assessmentUploadType(t string) bool {
	return t == "assessment-plan" || t == "assessment-results" || t == "plan-of-action-and-milestones"
}

func (m *Memory) SetAssessmentFramework(_ context.Context, uploadID, frameworkID, actor string) (OscalUpload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.uploads[uploadID]
	if !ok {
		return OscalUpload{}, ErrNotFound
	}
	if !assessmentUploadType(u.Type) {
		return OscalUpload{}, ErrInvalid
	}
	if frameworkID != "" {
		if _, ok := m.frameworks[frameworkID]; !ok {
			return OscalUpload{}, ErrNotFound
		}
	}
	u.LinkedFrameworkID = frameworkID
	m.log(actor, "assessment.framework_link", "oscal_upload", uploadID, map[string]any{"frameworkId": frameworkID})
	return cloneUpload(*u, false), nil
}

func (m *Memory) UpsertAssessmentMapping(_ context.Context, v AssessmentItemMapping, actor string) (AssessmentItemMapping, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.uploads[v.UploadID]
	if !ok {
		return AssessmentItemMapping{}, ErrNotFound
	}
	if !assessmentUploadType(u.Type) || strings.TrimSpace(v.ItemKey) == "" {
		return AssessmentItemMapping{}, ErrInvalid
	}
	r, ok := m.requirements[v.RequirementID]
	if !ok {
		return AssessmentItemMapping{}, ErrNotFound
	}
	if v.PartID != "" && !isTrackable(r, v.PartID) {
		return AssessmentItemMapping{}, ErrInvalid
	}
	v.MappedBy, v.MappedAt = actor, m.Now()
	if m.mappings[v.UploadID] == nil {
		m.mappings[v.UploadID] = map[string]*AssessmentItemMapping{}
	}
	c := v
	m.mappings[v.UploadID][v.ItemKey] = &c
	m.log(actor, "assessment.item_map", "oscal_upload", v.UploadID, map[string]any{"itemKey": v.ItemKey, "requirementId": v.RequirementID, "partId": v.PartID})
	return v, nil
}

func (m *Memory) DeleteAssessmentMapping(_ context.Context, uploadID, itemKey, actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.uploads[uploadID]; !ok {
		return ErrNotFound
	}
	if _, ok := m.mappings[uploadID][itemKey]; !ok {
		return ErrNotFound
	}
	delete(m.mappings[uploadID], itemKey)
	m.log(actor, "assessment.item_unmap", "oscal_upload", uploadID, map[string]any{"itemKey": itemKey})
	return nil
}
func (m *Memory) ListAssessmentMappings(_ context.Context, uploadID string) ([]AssessmentItemMapping, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.uploads[uploadID]; !ok {
		return nil, ErrNotFound
	}
	out := []AssessmentItemMapping{}
	for _, v := range m.mappings[uploadID] {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ItemKey < out[j].ItemKey })
	return out, nil
}
func (m *Memory) ListRequirementAssessments(_ context.Context, requirementID string) ([]AssessmentItemMapping, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.requirements[requirementID]; !ok {
		return nil, ErrNotFound
	}
	out := []AssessmentItemMapping{}
	for _, by := range m.mappings {
		for _, v := range by {
			if v.RequirementID == requirementID {
				out = append(out, *v)
			}
		}
	}
	return out, nil
}

func cloneParts(in []oscal.Part) []oscal.Part {
	if in == nil {
		return []oscal.Part{}
	}
	out := make([]oscal.Part, len(in))
	for i, p := range in {
		out[i] = p
		if p.Parts != nil {
			out[i].Parts = cloneParts(p.Parts)
		}
	}
	return out
}

func cloneStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return append([]string{}, in...)
}

func cloneParams(in []oscal.Param) []oscal.Param {
	if in == nil {
		return []oscal.Param{}
	}
	out := make([]oscal.Param, len(in))
	for i, p := range in {
		out[i] = p
		out[i].Values = append([]string(nil), p.Values...)
		out[i].Choices = append([]string(nil), p.Choices...)
	}
	return out
}

func cloneRefs(in []oscal.Reference) []oscal.Reference {
	if in == nil {
		return []oscal.Reference{}
	}
	return append([]oscal.Reference{}, in...)
}

func (r Requirement) clone() Requirement {
	c := r
	c.Parts = cloneParts(r.Parts)
	c.Params = cloneParams(r.Params)
	c.Related = cloneStrings(r.Related)
	c.References = cloneRefs(r.References)
	if r.DueDate != nil {
		d := *r.DueDate
		c.DueDate = &d
	}
	return c
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func (m *Memory) log(actor, action, entityType, entityID string, detail map[string]any) {
	m.audit = append(m.audit, AuditEntry{ID: newID(), At: m.Now(), Actor: actor, Action: action, EntityType: entityType, EntityID: entityID, Detail: detail})
}

// ImportFramework implements Store.
func (m *Memory) ImportFramework(_ context.Context, doc oscal.Document, actor string) (ImportResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.importFrameworkLocked(doc, actor)
}

// ImportUploadedFramework imports and marks an upload under one lock.
func (m *Memory) ImportUploadedFramework(_ context.Context, uploadID string, doc oscal.Document, actor string) (ImportResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.uploads[uploadID]
	if !ok {
		return ImportResult{}, ErrNotFound
	}
	if u.DocumentID != doc.ID || u.Type != doc.Type {
		return ImportResult{}, ErrInvalid
	}
	res, err := m.importFrameworkLocked(doc, actor)
	if err != nil {
		return ImportResult{}, err
	}
	if u.ImportedFrameworkID == "" {
		u.ImportedFrameworkID = res.Framework.ID
	}
	return res, nil
}

func (m *Memory) importFrameworkLocked(doc oscal.Document, actor string) (ImportResult, error) {
	var fw *Framework
	for _, f := range m.frameworks {
		if f.OscalDocumentID == doc.ID {
			fw = f
		}
	}
	if fw == nil {
		fw = &Framework{ID: newID(), OscalDocumentID: doc.ID}
		m.frameworks[fw.ID] = fw
	}
	fw.Type, fw.Title, fw.LastModified, fw.ImportedAt = doc.Type, doc.Title, doc.LastModified, m.Now()
	if fw.ShortName == "" {
		fw.ShortName = oscal.ShortName(doc.Title, doc.Props())
	}
	res := ImportResult{}
	for _, c := range doc.Controls {
		var existing *Requirement
		for _, r := range m.requirements {
			if r.FrameworkID == fw.ID && r.ControlID == c.ID {
				existing = r
			}
		}
		if existing == nil {
			r := &Requirement{ID: newID(), FrameworkID: fw.ID, ControlID: c.ID, Title: c.Title, Text: c.Text, UpdatedAt: normalizeUpdatedAt(m.Now()), Parts: cloneParts(c.Parts), Params: cloneParams(c.Params), Related: cloneStrings(c.Related), References: cloneRefs(c.References)}
			m.requirements[r.ID] = r
			m.recompute(r)
			res.Created++
		} else {
			existing.Title, existing.Text = c.Title, c.Text
			existing.Parts, existing.Params, existing.Related, existing.References = cloneParts(c.Parts), cloneParams(c.Params), cloneStrings(c.Related), cloneRefs(c.References)
			m.recompute(existing)
			res.Updated++
		}
	}
	fw.RequirementCount = m.countRequirements(fw.ID)
	res.Framework = *fw
	m.log(actor, "framework.import", "framework", fw.ID, map[string]any{"created": res.Created, "updated": res.Updated})
	return res, nil
}

func (m *Memory) countRequirements(frameworkID string) int {
	n := 0
	for _, r := range m.requirements {
		if r.FrameworkID == frameworkID {
			n++
		}
	}
	return n
}

// ListFrameworks implements Store.
func (m *Memory) ListFrameworks(context.Context) ([]Framework, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Framework, 0, len(m.frameworks))
	for _, f := range m.frameworks {
		c := *f
		c.RequirementCount = m.countRequirements(f.ID)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out, nil
}

// GetFramework implements Store.
func (m *Memory) GetFramework(_ context.Context, id string) (Framework, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.frameworks[id]
	if !ok {
		return Framework{}, ErrNotFound
	}
	c := *f
	c.RequirementCount = m.countRequirements(f.ID)
	return c, nil
}

// UpdateFramework implements Store.
func (m *Memory) UpdateFramework(_ context.Context, id, shortName, actor string) (Framework, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !ValidShortName(shortName) {
		return Framework{}, ErrInvalid
	}
	f, ok := m.frameworks[id]
	if !ok {
		return Framework{}, ErrNotFound
	}
	detail := map[string]any{"shortName": map[string]any{"from": f.ShortName, "to": shortName}}
	f.ShortName = shortName
	m.log(actor, "framework.update", "framework", f.ID, detail)
	c := *f
	c.RequirementCount = m.countRequirements(f.ID)
	return c, nil
}

func (m *Memory) CreateScopeCategory(_ context.Context, name, actor string) (ScopeCategory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	name = strings.TrimSpace(name)
	if !ValidScopeCategoryName(name) {
		return ScopeCategory{}, ErrInvalid
	}
	for _, c := range m.scopeCategories {
		if strings.EqualFold(c.Name, name) {
			return ScopeCategory{}, ErrConflict
		}
	}
	now := normalizeUpdatedAt(m.Now())
	c := ScopeCategory{ID: newID(), Name: name, CreatedAt: now, UpdatedAt: now}
	m.scopeCategories[c.ID] = &c
	m.log(actor, "scope-category.create", "scope_category", c.ID, map[string]any{"changed": true})
	return c, nil
}

func (m *Memory) ListScopeCategories(_ context.Context, query string, limit int) ([]ScopeCategory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := strings.ToLower(strings.TrimSpace(query))
	out := []ScopeCategory{}
	for _, c := range m.scopeCategories {
		if q == "" || strings.Contains(strings.ToLower(c.Name), q) {
			out = append(out, *c)
		}
	}
	sortScopes(out)
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) UpdateScopeCategory(_ context.Context, id, name string, expected UpdatedAtPrecondition, actor string) (ScopeCategory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	name = strings.TrimSpace(name)
	if !ValidScopeCategoryName(name) {
		return ScopeCategory{}, ErrInvalid
	}
	c, ok := m.scopeCategories[id]
	if !ok {
		return ScopeCategory{}, ErrNotFound
	}
	if err := checkUpdatedAtPrecondition(expected, true, c.UpdatedAt); err != nil {
		return ScopeCategory{}, err
	}
	for oid, other := range m.scopeCategories {
		if oid != id && strings.EqualFold(other.Name, name) {
			return ScopeCategory{}, ErrConflict
		}
	}
	c.Name, c.UpdatedAt = name, nextUpdatedAt(m.Now(), c.UpdatedAt)
	for _, byPart := range m.parts {
		for _, pt := range byPart {
			for i := range pt.Scopes {
				if pt.Scopes[i].ID == id {
					pt.Scopes[i] = *c
				}
			}
			sortScopes(pt.Scopes)
		}
	}
	m.log(actor, "scope-category.update", "scope_category", id, map[string]any{"changed": true})
	return *c, nil
}

func (m *Memory) DeleteScopeCategory(_ context.Context, id string, expected UpdatedAtPrecondition, actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.scopeCategories[id]
	if !ok {
		return ErrNotFound
	}
	if err := checkUpdatedAtPrecondition(expected, true, c.UpdatedAt); err != nil {
		return err
	}
	for _, byPart := range m.parts {
		for _, pt := range byPart {
			for _, c := range pt.Scopes {
				if c.ID == id {
					return ErrConflict
				}
			}
		}
	}
	delete(m.scopeCategories, id)
	m.log(actor, "scope-category.delete", "scope_category", id, map[string]any{"changed": true})
	return nil
}

// ListRequirements implements Store.
func (m *Memory) ListRequirements(_ context.Context, f RequirementFilter) ([]Requirement, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := strings.ToLower(strings.TrimSpace(f.Query))
	var all []Requirement
	for _, r := range m.requirements {
		if f.FrameworkID != "" && r.FrameworkID != f.FrameworkID {
			continue
		}
		if f.Status != "" && r.Status != f.Status {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(r.ControlID+" "+r.Title+" "+r.Text), q) {
			continue
		}
		if len(f.ScopeCategoryIDs) > 0 {
			found := false
			for _, pt := range m.parts[r.ID] {
				for _, c := range pt.Scopes {
					if contains(f.ScopeCategoryIDs, c.ID) {
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			if !found {
				continue
			}
		}
		all = append(all, r.clone())
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].FrameworkID != all[j].FrameworkID {
			return all[i].FrameworkID < all[j].FrameworkID
		}
		return all[i].ControlID < all[j].ControlID
	})
	return page(all, f.Offset, ClampLimit(f.Limit)), len(all), nil
}

func page[T any](all []T, offset, limit int) []T {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(all) {
		return []T{}
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return all[offset:end]
}

// GetRequirement implements Store.
func (m *Memory) GetRequirement(_ context.Context, id string) (Requirement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.requirements[id]
	if !ok {
		return Requirement{}, ErrNotFound
	}
	return r.clone(), nil
}

// recompute refreshes the derived fields of r from its part tracking rows.
func (m *Memory) recompute(r *Requirement) {
	rows := make([]PartTracking, 0, len(m.parts[r.ID]))
	for _, pt := range m.parts[r.ID] {
		rows = append(rows, *pt)
	}
	r.DerivedStatus, r.Owner, r.DueDate = DeriveRequirement(r.Parts, r.ControlID, rows)
	r.Status = effectiveStatus(r.DerivedStatus, r.StatusOverride)
}

// UpdateRequirement implements Store.
func (m *Memory) UpdateRequirement(_ context.Context, id string, p RequirementPatch, actor string) (Requirement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.requirements[id]
	if !ok {
		return Requirement{}, ErrNotFound
	}
	if err := checkUpdatedAtPrecondition(p.ExpectedUpdatedAt, true, r.UpdatedAt); err != nil {
		return Requirement{}, err
	}
	if p.StatusOverride != nil && !ValidStatus(*p.StatusOverride) {
		return Requirement{}, ErrInvalid
	}
	detail := map[string]any{}
	if p.ClearStatusOverride {
		if r.StatusOverride != "" {
			detail["statusOverride"] = map[string]any{"from": r.StatusOverride, "to": nil}
		}
		r.StatusOverride = ""
	} else if p.StatusOverride != nil && *p.StatusOverride != r.StatusOverride {
		var from any
		if r.StatusOverride != "" {
			from = r.StatusOverride
		}
		detail["statusOverride"] = map[string]any{"from": from, "to": *p.StatusOverride}
		r.StatusOverride = *p.StatusOverride
	}
	if p.Notes != nil && *p.Notes != r.Notes {
		detail["notes"] = true
		r.Notes = *p.Notes
	}
	m.recompute(r)
	r.UpdatedAt = nextUpdatedAt(m.Now(), r.UpdatedAt)
	m.log(actor, "requirement.update", "requirement", r.ID, detail)
	return r.clone(), nil
}

// ---- part tracking ---------------------------------------------------------

// GetPartTracking implements Store.
func (m *Memory) GetPartTracking(_ context.Context, requirementID string) ([]PartTracking, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.requirements[requirementID]; !ok {
		return nil, ErrNotFound
	}
	out := make([]PartTracking, 0, len(m.parts[requirementID]))
	for _, pt := range m.parts[requirementID] {
		out = append(out, pt.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PartID < out[j].PartID })
	return out, nil
}

func (pt PartTracking) clone() PartTracking {
	c := pt
	c.Scopes = append([]ScopeCategory{}, pt.Scopes...)
	if pt.DueDate != nil {
		d := *pt.DueDate
		c.DueDate = &d
	}
	return c
}

// isTrackable reports whether partID is a trackable part of the requirement.
func isTrackable(r *Requirement, partID string) bool {
	for _, p := range oscal.TrackableParts(r.Parts, r.ControlID) {
		if p.ID == partID {
			return true
		}
	}
	return false
}

// applyPartPatch mutates pt per patch and returns the audit detail of the
// changed fields (empty when nothing changed). Validation happens first.
func applyPartPatch(pt *PartTracking, patch PartPatch) (map[string]any, error) {
	if patch.Status != nil && !ValidStatus(*patch.Status) {
		return nil, ErrInvalid
	}
	detail := map[string]any{"partId": pt.PartID}
	if patch.Status != nil && *patch.Status != pt.Status {
		detail["status"] = map[string]any{"from": pt.Status, "to": *patch.Status}
		pt.Status = *patch.Status
	}
	if patch.Owner != nil && *patch.Owner != pt.Owner {
		detail["owner"] = map[string]any{"from": pt.Owner, "to": *patch.Owner}
		pt.Owner = *patch.Owner
	}
	if patch.ClearDueDate {
		if pt.DueDate != nil {
			detail["dueDate"] = map[string]any{"from": pt.DueDate, "to": nil}
		}
		pt.DueDate = nil
	} else if patch.DueDate != nil {
		d := dateArg(patch.DueDate)
		if pt.DueDate == nil || !pt.DueDate.Equal(*d) {
			detail["dueDate"] = map[string]any{"from": pt.DueDate, "to": d}
		}
		pt.DueDate = d
	}
	if patch.Description != nil && *patch.Description != pt.Description {
		detail["description"] = true
		pt.Description = *patch.Description
	}
	if patch.ScopeCategoryIDs != nil {
		detail["scopes"] = map[string]any{"changed": true}
	}
	if pt.Status == StatusImplemented && len(pt.Scopes) == 0 {
		return nil, ErrInvalid
	}
	return detail, nil
}

// UpsertPartTracking implements Store.
func (m *Memory) UpsertPartTracking(_ context.Context, requirementID, partID string, patch PartPatch, actor string) (PartTracking, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.requirements[requirementID]
	if !ok {
		return PartTracking{}, ErrNotFound
	}
	if !isTrackable(r, partID) {
		return PartTracking{}, partNotTrackable
	}
	byPart := m.parts[requirementID]
	pt, exists := byPart[partID]
	currentUpdatedAt := time.Time{}
	if exists {
		currentUpdatedAt = pt.UpdatedAt
	}
	if err := checkUpdatedAtPrecondition(patch.ExpectedUpdatedAt, exists, currentUpdatedAt); err != nil {
		return PartTracking{}, err
	}
	if !exists {
		pt = &PartTracking{RequirementID: requirementID, PartID: partID, Status: StatusPlanned}
	}
	next := pt.clone()
	if patch.ScopeCategoryIDs != nil {
		ids := dedupe(*patch.ScopeCategoryIDs)
		next.Scopes = make([]ScopeCategory, 0, len(ids))
		for _, id := range ids {
			c, ok := m.scopeCategories[id]
			if !ok {
				return PartTracking{}, ErrNotFound
			}
			next.Scopes = append(next.Scopes, *c)
		}
		sortScopes(next.Scopes)
	}
	detail, err := applyPartPatch(&next, patch)
	if err != nil {
		return PartTracking{}, err
	}
	now := m.Now()
	next.UpdatedAt = nextUpdatedAt(now, pt.UpdatedAt)
	if byPart == nil {
		byPart = map[string]*PartTracking{}
		m.parts[requirementID] = byPart
	}
	byPart[partID] = &next
	m.recompute(r)
	r.UpdatedAt = nextUpdatedAt(now, r.UpdatedAt)
	m.log(actor, "part.update", "requirement", requirementID, detail)
	return next.clone(), nil
}

func sortScopes(scopes []ScopeCategory) {
	sort.Slice(scopes, func(i, j int) bool { return strings.ToLower(scopes[i].Name) < strings.ToLower(scopes[j].Name) })
}

// CreateEvidence implements Store.
func (m *Memory) CreateEvidence(_ context.Context, e Evidence, actor string) (Evidence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rid := range e.RequirementIDs {
		if _, ok := m.requirements[rid]; !ok {
			return Evidence{}, ErrNotFound
		}
	}
	e.RequirementIDs = dedupe(e.RequirementIDs)
	if err := normalizeEvidence(&e); err != nil {
		return Evidence{}, err
	}
	e.ID = newID()
	e.CreatedAt = m.Now()
	e.UploadedBy = actor
	c := e
	m.evidence[e.ID] = &c
	m.log(actor, "evidence.create", "evidence", e.ID, evidenceAuditDetail(e))
	return e, nil
}

// normalizeEvidence applies the kind invariants shared by both stores:
// kind defaults to file; link evidence needs a valid URL and carries no file
// metadata; file evidence carries no URL.
func normalizeEvidence(e *Evidence) error {
	if e.Kind == "" {
		e.Kind = EvidenceKindFile
	}
	switch e.Kind {
	case EvidenceKindFile:
		e.URL = ""
	case EvidenceKindLink:
		if !ValidEvidenceURL(e.URL) {
			return ErrInvalid
		}
		e.FileName, e.ContentType, e.Sha256, e.SizeBytes = "", "", "", 0
	default:
		return ErrInvalid
	}
	return nil
}

func evidenceAuditDetail(e Evidence) map[string]any {
	d := map[string]any{"kind": e.Kind, "requirementIds": e.RequirementIDs}
	if e.Kind == EvidenceKindLink {
		d["url"] = e.URL
	} else {
		d["sha256"] = e.Sha256
	}
	return d
}

// ListEvidence implements Store.
func (m *Memory) ListEvidence(_ context.Context, f EvidenceFilter) ([]Evidence, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := strings.ToLower(strings.TrimSpace(f.Query))
	var all []Evidence
	for _, e := range m.evidence {
		if f.Kind != "" && e.Kind != f.Kind {
			continue
		}
		if f.RequirementID != "" && !contains(e.RequirementIDs, f.RequirementID) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(e.Title+" "+e.Description+" "+e.FileName+" "+e.URL), q) {
			continue
		}
		all = append(all, e.clone())
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	return page(all, f.Offset, ClampLimit(f.Limit)), len(all), nil
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// GetEvidence implements Store.
func (m *Memory) GetEvidence(_ context.Context, id string) (Evidence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.evidence[id]
	if !ok {
		return Evidence{}, ErrNotFound
	}
	return e.clone(), nil
}

func (e Evidence) clone() Evidence {
	c := e
	c.RequirementIDs = cloneStrings(e.RequirementIDs)
	return c
}

// ListAudit implements Store.
func (m *Memory) ListAudit(_ context.Context, limit int) ([]AuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	limit = ClampLimit(limit)
	out := make([]AuditEntry, 0, limit)
	for i := len(m.audit) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, m.audit[i])
	}
	return out, nil
}
