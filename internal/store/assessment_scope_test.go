package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
)

func exerciseScopeCategories(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	doc := oscal.Document{ID: "cat", Type: "catalog", Title: "Catalog", Controls: []oscal.Control{{ID: "AC-2", Title: "Accounts", Parts: []oscal.Part{{ID: "ac-2_smt", Name: "statement"}}}}}
	res, err := s.ImportFramework(ctx, doc, "a")
	if err != nil {
		t.Fatal(err)
	}
	reqs, _, _ := s.ListRequirements(ctx, RequirementFilter{FrameworkID: res.Framework.ID})
	r := reqs[0]

	production, err := s.CreateScopeCategory(ctx, "  Production 2  ", "alice")
	if err != nil || production.Name != "Production 2" {
		t.Fatalf("create category: %+v %v", production, err)
	}
	company, err := s.CreateScopeCategory(ctx, "Entire company", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateScopeCategory(ctx, "production 2", "alice"); !errors.Is(err, ErrConflict) {
		t.Fatalf("case-insensitive duplicate: %v", err)
	}
	if _, err = s.UpdateScopeCategory(ctx, company.ID, "PRODUCTION 2", UpdatedAtPrecondition{}, "alice"); !errors.Is(err, ErrConflict) {
		t.Fatalf("case-insensitive duplicate rename: %v", err)
	}
	items, err := s.ListScopeCategories(ctx, "COMP", 100)
	if err != nil || len(items) != 1 || items[0].ID != company.ID {
		t.Fatalf("search categories: %+v %v", items, err)
	}

	implemented := StatusImplemented
	if _, err = s.UpsertPartTracking(ctx, r.ID, "ac-2_smt", PartPatch{Status: &implemented}, "a"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("implemented without scopes: %v", err)
	}
	ids := []string{production.ID, company.ID, production.ID}
	pt, err := s.UpsertPartTracking(ctx, r.ID, "ac-2_smt", PartPatch{Status: &implemented, ScopeCategoryIDs: &ids}, "a")
	if err != nil || len(pt.Scopes) != 2 || pt.Scopes[0].Name != "Entire company" || pt.Scopes[1].Name != "Production 2" {
		t.Fatalf("multi scopes=%+v err=%v", pt.Scopes, err)
	}
	if _, n, _ := s.ListRequirements(ctx, RequirementFilter{ScopeCategoryIDs: []string{production.ID}}); n != 1 {
		t.Fatalf("scope filter=%d", n)
	}
	if _, n, _ := s.ListRequirements(ctx, RequirementFilter{ScopeCategoryIDs: []string{newID()}}); n != 0 {
		t.Fatalf("unknown scope filter=%d", n)
	}
	empty := []string{}
	if _, err = s.UpsertPartTracking(ctx, r.ID, "ac-2_smt", PartPatch{ScopeCategoryIDs: &empty}, "a"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cleared implemented scopes: %v", err)
	}
	unknown := []string{newID()}
	if _, err = s.UpsertPartTracking(ctx, r.ID, "ac-2_smt", PartPatch{ScopeCategoryIDs: &unknown}, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown category: %v", err)
	}
	productionExpected := UpdatedAtPrecondition{State: UpdatedAtValue, Value: production.UpdatedAt}
	if err = s.DeleteScopeCategory(ctx, production.ID, productionExpected, "alice"); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete in-use category with exact precondition: %v", err)
	}
	planned := StatusPlanned
	if _, err = s.UpsertPartTracking(ctx, r.ID, "ac-2_smt", PartPatch{Status: &planned, ScopeCategoryIDs: &empty}, "a"); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteScopeCategory(ctx, production.ID, UpdatedAtPrecondition{}, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateScopeCategory(ctx, company.ID, "production 2", UpdatedAtPrecondition{}, "alice"); err != nil {
		t.Fatal(err)
	}
	audit, _ := s.ListAudit(ctx, 20)
	for _, a := range audit {
		if strings.HasPrefix(a.Action, "scope-category.") && !reflect.DeepEqual(a.Detail, map[string]any{"changed": true}) {
			t.Fatalf("scope category audit leaked detail: %#v", a)
		}
	}
}

func TestMemoryScopeCategories(t *testing.T) { exerciseScopeCategories(t, NewMemory()) }

func TestPostgresScopeCategories(t *testing.T) {
	p, _ := openTestPostgres(t)
	exerciseScopeCategories(t, p)
}

func TestMemoryAssessmentMappingDoesNotChangeStatus(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	doc := oscal.Document{ID: "cat", Type: "catalog", Title: "Catalog", Controls: []oscal.Control{{ID: "AC-2", Title: "Accounts", Parts: []oscal.Part{{ID: "ac-2_smt", Name: "statement"}}}}}
	res, _ := m.ImportFramework(ctx, doc, "a")
	reqs, _, _ := m.ListRequirements(ctx, RequirementFilter{})
	before := reqs[0].Status
	raw := []byte(`{"assessment-results":{"uuid":"ar","metadata":{"title":"Assessment"},"results":[]}}`)
	u, err := m.CreateOscalUpload(ctx, oscal.Document{ID: "ar", Type: "assessment-results", Title: "Assessment"}, raw, "sha", int64(len(raw)), "a")
	if err != nil {
		t.Fatal(err)
	}
	linked, err := m.SetAssessmentFramework(ctx, u.ID, res.Framework.ID, "a")
	if err != nil || linked.LinkedFrameworkID != res.Framework.ID {
		t.Fatalf("link %#v %v", linked, err)
	}
	mapping, err := m.UpsertAssessmentMapping(ctx, AssessmentItemMapping{UploadID: u.ID, ItemKey: "assessment-results/result/finding/f", RequirementID: reqs[0].ID, PartID: "ac-2_smt"}, "a")
	if err != nil {
		t.Fatal(err)
	}
	if mapping.MappedBy != "a" {
		t.Fatal("mapper missing")
	}
	after, _ := m.GetRequirement(ctx, reqs[0].ID)
	if after.Status != before {
		t.Fatalf("status changed %s -> %s", before, after.Status)
	}
	if err = m.DeleteAssessmentMapping(ctx, u.ID, mapping.ItemKey, "a"); err != nil {
		t.Fatal(err)
	}
	list, _ := m.ListAssessmentMappings(ctx, u.ID)
	if len(list) != 0 {
		t.Fatal("mapping not deleted")
	}
}
