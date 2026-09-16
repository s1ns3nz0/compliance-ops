package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
)

// forEachStore runs fn against the in-memory store and, when DATABASE_URL is
// set, the Postgres store.
func forEachStore(t *testing.T, fn func(t *testing.T, s Store, ctx context.Context)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) {
		fn(t, NewMemory(), context.Background())
	})
	t.Run("postgres", func(t *testing.T) {
		p, ctx := openTestPostgres(t)
		fn(t, p, ctx)
	})
}

func strp(s string) *string { return &s }

func TestStore_PartTrackingDerivesRequirement(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store, ctx context.Context) {
		res, err := s.ImportFramework(ctx, partsDoc(t), "importer")
		if err != nil {
			t.Fatal(err)
		}
		reqs, _, _ := s.ListRequirements(ctx, RequirementFilter{FrameworkID: res.Framework.ID})
		ac2, ac3 := reqs[0], reqs[1]
		scope, err := s.CreateScopeCategory(ctx, "production service", "alice")
		if err != nil {
			t.Fatal(err)
		}
		scopeIDs := []string{scope.ID}
		if ac2.ControlID != "ac-2" || ac3.ControlID != "ac-3" {
			t.Fatalf("order = %s %s", ac2.ControlID, ac3.ControlID)
		}

		// Lazy rows: nothing stored yet.
		rows, err := s.GetPartTracking(ctx, ac2.ID)
		if err != nil || rows == nil || len(rows) != 0 {
			t.Fatalf("initial rows = %#v, %v", rows, err)
		}
		// Invalid part ids: the statement itself (it has items), nested items, guidance, unknown.
		for _, bad := range []string{"ac-2_smt", "ac-2_smt.b.1", "ac-2_gdn", "nope", ""} {
			if _, err := s.UpsertPartTracking(ctx, ac2.ID, bad, PartPatch{Status: strp(StatusImplemented)}, "alice"); !errors.Is(err, ErrInvalid) {
				t.Fatalf("part %q err = %v, want ErrInvalid", bad, err)
			}
		}
		if _, err := s.UpsertPartTracking(ctx, ac2.ID, "ac-2_smt.a", PartPatch{Status: strp("done")}, "alice"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("bad status err = %v", err)
		}
		if rows, _ := s.GetPartTracking(ctx, ac2.ID); len(rows) != 0 {
			t.Fatalf("rejected upserts must not create rows: %+v", rows)
		}

		due := time.Date(2026, 10, 1, 15, 0, 0, 0, time.FixedZone("KST", 9*3600))
		a, err := s.UpsertPartTracking(ctx, ac2.ID, "ac-2_smt.a", PartPatch{Status: strp(StatusImplemented), Owner: strp("alice"), DueDate: &due, Description: strp("# Done\n\nTypes documented."), ScopeCategoryIDs: &scopeIDs}, "alice")
		if err != nil {
			t.Fatalf("upsert a: %v", err)
		}
		if a.RequirementID != ac2.ID || a.PartID != "ac-2_smt.a" || a.Status != StatusImplemented || a.Owner != "alice" || a.Description != "# Done\n\nTypes documented." || a.UpdatedAt.IsZero() {
			t.Fatalf("a = %+v", a)
		}
		if a.DueDate == nil || !a.DueDate.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
			t.Fatalf("a.DueDate = %v (want UTC midnight)", a.DueDate)
		}
		r, _ := s.GetRequirement(ctx, ac2.ID)
		// a implemented, b missing (planned) → partial; owner alice; due nil (a is implemented).
		if r.Status != StatusPartial || r.DerivedStatus != StatusPartial || r.Owner != "alice" || r.DueDate != nil {
			t.Fatalf("after a: %+v", r)
		}
		if !r.UpdatedAt.After(ac2.UpdatedAt) {
			t.Fatalf("requirement updatedAt not advanced")
		}

		bDue := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
		b, err := s.UpsertPartTracking(ctx, ac2.ID, "ac-2_smt.b", PartPatch{Status: strp(StatusPartial), Owner: strp("bob"), DueDate: &bDue}, "bob")
		if err != nil || b.Status != StatusPartial || b.Description != "" {
			t.Fatalf("upsert b = %+v, %v", b, err)
		}
		r, _ = s.GetRequirement(ctx, ac2.ID)
		// Owners tie (alice, bob) → first in part order (a: alice); due = b's.
		if r.Status != StatusPartial || r.Owner != "alice" || r.DueDate == nil || !r.DueDate.Equal(bDue) {
			t.Fatalf("after b: %+v", r)
		}
		// Partial patch keeps other fields; clearing the due date.
		b, err = s.UpsertPartTracking(ctx, ac2.ID, "ac-2_smt.b", PartPatch{Status: strp(StatusImplemented), ScopeCategoryIDs: &scopeIDs, ClearDueDate: true}, "bob")
		if err != nil || b.Status != StatusImplemented || b.Owner != "bob" || b.DueDate != nil {
			t.Fatalf("patch b = %+v, %v", b, err)
		}
		r, _ = s.GetRequirement(ctx, ac2.ID)
		if r.Status != StatusImplemented || r.DerivedStatus != StatusImplemented || r.DueDate != nil {
			t.Fatalf("after both implemented: %+v", r)
		}
		rows, _ = s.GetPartTracking(ctx, ac2.ID)
		if len(rows) != 2 {
			t.Fatalf("rows = %+v", rows)
		}
		// Effective status drives the list filter; the override wins over the derivation.
		if _, total, _ := s.ListRequirements(ctx, RequirementFilter{Status: StatusImplemented}); total != 1 {
			t.Fatalf("implemented total = %d", total)
		}
		if _, err := s.UpdateRequirement(ctx, ac2.ID, RequirementPatch{StatusOverride: strp(StatusAlternative)}, "carol"); err != nil {
			t.Fatal(err)
		}
		r, _ = s.GetRequirement(ctx, ac2.ID)
		if r.Status != StatusAlternative || r.DerivedStatus != StatusImplemented || r.StatusOverride != StatusAlternative {
			t.Fatalf("override = %+v", r)
		}
		if _, total, _ := s.ListRequirements(ctx, RequirementFilter{Status: StatusAlternative}); total != 1 {
			t.Fatalf("alternative total = %d", total)
		}
		// Part updates keep the override in place and still refresh the derivation.
		if _, err := s.UpsertPartTracking(ctx, ac2.ID, "ac-2_smt.a", PartPatch{Status: strp(StatusPlanned)}, "alice"); err != nil {
			t.Fatal(err)
		}
		r, _ = s.GetRequirement(ctx, ac2.ID)
		if r.Status != StatusAlternative || r.DerivedStatus != StatusPartial {
			t.Fatalf("override after part change = %+v", r)
		}
		if _, err := s.UpdateRequirement(ctx, ac2.ID, RequirementPatch{ClearStatusOverride: true}, "carol"); err != nil {
			t.Fatal(err)
		}
		r, _ = s.GetRequirement(ctx, ac2.ID)
		if r.Status != StatusPartial || r.StatusOverride != "" {
			t.Fatalf("cleared override = %+v", r)
		}

		// ac-3 has no parts: the synthetic <control>_smt part is trackable.
		if _, err := s.UpsertPartTracking(ctx, ac3.ID, "ac-3_smt", PartPatch{Status: strp(StatusNotApplicable), Owner: strp("eve")}, "eve"); err != nil {
			t.Fatalf("synthetic part: %v", err)
		}
		r3, _ := s.GetRequirement(ctx, ac3.ID)
		if r3.Status != StatusNotApplicable || r3.Owner != "eve" {
			t.Fatalf("ac-3 = %+v", r3)
		}

		// Audit: part.update rows on the requirement with the changed fields.
		audit, _ := s.ListAudit(ctx, 50)
		var partUpdates []AuditEntry
		for _, e := range audit {
			if e.Action == "part.update" {
				partUpdates = append(partUpdates, e)
			}
		}
		if len(partUpdates) != 5 {
			t.Fatalf("part.update rows = %d, want 5", len(partUpdates))
		}
		first := partUpdates[len(partUpdates)-1]
		if first.Actor != "alice" || first.EntityType != "requirement" || first.EntityID != ac2.ID || first.Detail["partId"] != "ac-2_smt.a" {
			t.Fatalf("first part.update = %+v", first)
		}
		st, _ := first.Detail["status"].(map[string]any)
		if st["from"] != StatusPlanned || st["to"] != StatusImplemented || first.Detail["description"] != true || first.Detail["owner"] == nil || first.Detail["dueDate"] == nil {
			t.Fatalf("first part.update detail = %+v", first.Detail)
		}
		for _, e := range audit {
			if strings.HasPrefix(e.Action, "implementation.") || e.EntityType == "activity" {
				t.Fatalf("legacy activity audit row: %+v", e)
			}
		}
	})
}

func TestStore_ReimportKeepsPartTrackingAndRecomputes(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store, ctx context.Context) {
		doc := partsDoc(t)
		res, err := s.ImportFramework(ctx, doc, "importer")
		if err != nil {
			t.Fatal(err)
		}
		reqs, _, _ := s.ListRequirements(ctx, RequirementFilter{FrameworkID: res.Framework.ID})
		ac2 := reqs[0]
		scope, err := s.CreateScopeCategory(ctx, "production service", "alice")
		if err != nil {
			t.Fatal(err)
		}
		scopeIDs := []string{scope.ID}
		for _, part := range []string{"ac-2_smt.a", "ac-2_smt.b"} {
			if _, err := s.UpsertPartTracking(ctx, ac2.ID, part, PartPatch{Status: strp(StatusImplemented), Owner: strp("alice"), ScopeCategoryIDs: &scopeIDs}, "alice"); err != nil {
				t.Fatal(err)
			}
		}
		if r, _ := s.GetRequirement(ctx, ac2.ID); r.Status != StatusImplemented {
			t.Fatalf("before re-import = %+v", r)
		}
		// Re-import with a third statement item: the rows survive, the new
		// item counts as planned → partial.
		doc.Controls[0].Parts[0].Parts = append(doc.Controls[0].Parts[0].Parts, partsDoc(t).Controls[0].Parts[0].Parts[0])
		doc.Controls[0].Parts[0].Parts[2].ID = "ac-2_smt.c"
		if _, err := s.ImportFramework(ctx, doc, "importer"); err != nil {
			t.Fatal(err)
		}
		r, _ := s.GetRequirement(ctx, ac2.ID)
		if r.Status != StatusPartial || r.Owner != "alice" {
			t.Fatalf("after re-import = %+v", r)
		}
		rows, _ := s.GetPartTracking(ctx, ac2.ID)
		if len(rows) != 2 {
			t.Fatalf("rows after re-import = %+v", rows)
		}
		var ids []string
		for _, pt := range rows {
			ids = append(ids, pt.PartID)
		}
		if !reflect.DeepEqual(ids, []string{"ac-2_smt.a", "ac-2_smt.b"}) {
			t.Fatalf("row ids = %v", ids)
		}
	})
}

func TestStore_ReimportRetainsControlsMissingFromSource(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store, ctx context.Context) {
		doc := oscal.Document{ID: "snapshot-catalog", Type: "catalog", Title: "Snapshot", Controls: []oscal.Control{
			{ID: "a-1", Title: "Retained title"},
			{ID: "a-2", Title: "Still present"},
		}}
		first, err := s.ImportFramework(ctx, doc, "importer")
		if err != nil {
			t.Fatal(err)
		}
		doc.Controls = doc.Controls[1:]
		if _, err := s.ImportFramework(ctx, doc, "importer"); err != nil {
			t.Fatal(err)
		}
		reqs, total, err := s.ListRequirements(ctx, RequirementFilter{FrameworkID: first.Framework.ID, Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		if total != 2 || len(reqs) != 2 {
			t.Fatalf("reimport removed retained snapshot: total=%d reqs=%+v", total, reqs)
		}
		found := false
		for _, req := range reqs {
			if req.ControlID == "a-1" {
				found = true
				if req.Title != "Retained title" {
					t.Fatalf("retained snapshot changed: %+v", req)
				}
			}
		}
		if !found {
			t.Fatal("source-missing control snapshot was not retained")
		}
	})
}

func TestStore_EvidenceHasNoActivityLinks(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store, ctx context.Context) {
		res, err := s.ImportFramework(ctx, partsDoc(t), "importer")
		if err != nil {
			t.Fatal(err)
		}
		reqs, _, _ := s.ListRequirements(ctx, RequirementFilter{FrameworkID: res.Framework.ID})
		ev, err := s.CreateEvidence(ctx, Evidence{Kind: EvidenceKindLink, URL: "https://x.example/1", Title: "One", RequirementIDs: []string{reqs[0].ID, reqs[0].ID}}, "bob")
		if err != nil || !reflect.DeepEqual(ev.RequirementIDs, []string{reqs[0].ID}) {
			t.Fatalf("evidence = %+v, %v", ev, err)
		}
		audit, _ := s.ListAudit(ctx, 1)
		if _, has := audit[0].Detail["activityIds"]; has || audit[0].Action != "evidence.create" {
			t.Fatalf("audit = %+v", audit[0])
		}
	})
}
