package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
)

const testCatalog = `{
  "catalog": {
    "uuid": "11111111-2222-3333-4444-555555555555",
    "metadata": {"title": "Test Catalog", "last-modified": "2026-01-01T00:00:00Z"},
    "groups": [
      {"controls": [
        {"id": "ac-1", "title": "Access Control Policy", "parts": [{"name": "statement", "prose": "Develop an access control policy."}],
         "controls": [{"id": "ac-1.1", "title": "Policy Review"}]},
        {"id": "ac-2", "title": "Account Management"}
      ]}
    ]
  }
}`

func testDoc(t *testing.T) oscal.Document {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(testCatalog), &v); err != nil {
		t.Fatal(err)
	}
	docs, err := oscal.Parse(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || len(docs[0].Controls) != 3 {
		t.Fatalf("unexpected parse result: %+v", docs)
	}
	return docs[0]
}

func openTestPostgres(t *testing.T) (*Postgres, context.Context) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Second run must be a no-op.
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate twice: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE audit_log, assessment_item_mappings, evidence_requirements, evidence, part_scope_categories, part_tracking, scope_categories, requirements, oscal_uploads, frameworks`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return NewPostgres(pool), ctx
}

func TestPostgresEvidenceDigestLockSerializesAcrossInstances(t *testing.T) {
	pool, ctx := scratchDatabase(t)
	first := NewPostgres(pool)
	second := NewPostgres(pool)
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	unlockFirst, err := first.AcquireEvidenceDigestLock(ctx, digest)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}

	acquired := make(chan func(context.Context) error, 1)
	errs := make(chan error, 1)
	go func() {
		unlock, err := second.AcquireEvidenceDigestLock(ctx, digest)
		if err != nil {
			errs <- err
			return
		}
		acquired <- unlock
	}()

	select {
	case <-acquired:
		t.Fatal("second instance acquired digest lock before release")
	case err := <-errs:
		t.Fatalf("second lock failed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := unlockFirst(ctx); err != nil {
		t.Fatalf("release first lock: %v", err)
	}
	select {
	case unlockSecond := <-acquired:
		if err := unlockSecond(ctx); err != nil {
			t.Fatalf("release second lock: %v", err)
		}
	case err := <-errs:
		t.Fatalf("second lock failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("second instance did not acquire digest lock after release")
	}
}

func TestPostgresImportIdempotent(t *testing.T) {
	p, ctx := openTestPostgres(t)
	doc := testDoc(t)

	first, err := p.ImportFramework(ctx, doc, "importer")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if first.Created != 3 || first.Updated != 0 || first.Framework.RequirementCount != 3 {
		t.Fatalf("first import = %+v", first)
	}
	if first.Framework.OscalDocumentID != doc.ID || first.Framework.Title != "Test Catalog" || first.Framework.Type != "catalog" {
		t.Fatalf("framework = %+v", first.Framework)
	}

	doc.Controls[0].Title = "Access Control Policy (rev 2)"
	second, err := p.ImportFramework(ctx, doc, "importer")
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if second.Created != 0 || second.Updated != 3 || second.Framework.ID != first.Framework.ID || second.Framework.RequirementCount != 3 {
		t.Fatalf("second import = %+v", second)
	}

	fws, err := p.ListFrameworks(ctx)
	if err != nil || len(fws) != 1 || fws[0].RequirementCount != 3 {
		t.Fatalf("ListFrameworks = %+v, %v", fws, err)
	}
	fw, err := p.GetFramework(ctx, first.Framework.ID)
	if err != nil || fw.ID != first.Framework.ID {
		t.Fatalf("GetFramework = %+v, %v", fw, err)
	}

	reqs, total, err := p.ListRequirements(ctx, RequirementFilter{FrameworkID: fw.ID})
	if err != nil || total != 3 || len(reqs) != 3 {
		t.Fatalf("ListRequirements = %d rows, total %d, %v", len(reqs), total, err)
	}
	// Sorted by controlId.
	if reqs[0].ControlID != "ac-1" || reqs[1].ControlID != "ac-1.1" || reqs[2].ControlID != "ac-2" {
		t.Fatalf("order = %q %q %q", reqs[0].ControlID, reqs[1].ControlID, reqs[2].ControlID)
	}
	if reqs[0].Title != "Access Control Policy (rev 2)" || reqs[0].Status != StatusPlanned {
		t.Fatalf("ac-1 = %+v", reqs[0])
	}

	audit, err := p.ListAudit(ctx, 10)
	if err != nil || len(audit) != 2 {
		t.Fatalf("audit = %d entries, %v", len(audit), err)
	}
	if audit[0].Action != "framework.import" || audit[0].Actor != "importer" || audit[0].EntityID != fw.ID {
		t.Fatalf("audit[0] = %+v", audit[0])
	}
	if got := audit[0].Detail["updated"]; got != float64(3) {
		t.Fatalf("audit[0].Detail = %+v", audit[0].Detail)
	}
}

func TestPostgresUpdateRequirement(t *testing.T) {
	p, ctx := openTestPostgres(t)
	res, err := p.ImportFramework(ctx, testDoc(t), "importer")
	if err != nil {
		t.Fatal(err)
	}
	reqs, _, err := p.ListRequirements(ctx, RequirementFilter{FrameworkID: res.Framework.ID})
	if err != nil {
		t.Fatal(err)
	}
	r := reqs[0]
	if r.Status != StatusPlanned || r.DerivedStatus != StatusPlanned || r.StatusOverride != "" {
		t.Fatalf("fresh requirement = %+v", r)
	}

	override := StatusImplemented
	notes := "kick-off"
	updated, err := p.UpdateRequirement(ctx, r.ID, RequirementPatch{StatusOverride: &override, Notes: &notes}, "alice")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Status != StatusImplemented || updated.DerivedStatus != StatusPlanned || updated.StatusOverride != StatusImplemented || updated.Notes != "kick-off" {
		t.Fatalf("updated = %+v", updated)
	}
	if !updated.UpdatedAt.After(r.UpdatedAt) {
		t.Fatalf("updatedAt not advanced: %v -> %v", r.UpdatedAt, updated.UpdatedAt)
	}
	got, err := p.GetRequirement(ctx, r.ID)
	if err != nil || got.Status != StatusImplemented || got.StatusOverride != StatusImplemented {
		t.Fatalf("GetRequirement = %+v, %v", got, err)
	}

	audit, err := p.ListAudit(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	var updates []AuditEntry
	for _, a := range audit {
		if a.Action == "requirement.update" {
			updates = append(updates, a)
		}
	}
	if len(updates) != 1 {
		t.Fatalf("want exactly one requirement.update audit row, got %d", len(updates))
	}
	a := updates[0]
	if a.Actor != "alice" || a.EntityType != "requirement" || a.EntityID != r.ID {
		t.Fatalf("audit = %+v", a)
	}
	st, _ := a.Detail["statusOverride"].(map[string]any)
	if st["from"] != nil || st["to"] != StatusImplemented || a.Detail["notes"] != true {
		t.Fatalf("audit detail = %+v", a.Detail)
	}

	// The effective status drives the filter.
	_, total, err := p.ListRequirements(ctx, RequirementFilter{Status: StatusImplemented})
	if err != nil || total != 1 {
		t.Fatalf("status filter total = %d, %v", total, err)
	}
	_, total, err = p.ListRequirements(ctx, RequirementFilter{Status: StatusPlanned})
	if err != nil || total != 2 {
		t.Fatalf("planned filter total = %d, %v", total, err)
	}

	// Clear the override: back to the derived status.
	cleared, err := p.UpdateRequirement(ctx, r.ID, RequirementPatch{ClearStatusOverride: true}, "alice")
	if err != nil || cleared.Status != StatusPlanned || cleared.StatusOverride != "" {
		t.Fatalf("clear = %+v, %v", cleared, err)
	}
	audit, _ = p.ListAudit(ctx, 1)
	st, _ = audit[0].Detail["statusOverride"].(map[string]any)
	if st["from"] != StatusImplemented || st["to"] != nil {
		t.Fatalf("clear audit detail = %+v", audit[0].Detail)
	}

	// Invalid override (including the retired vocabulary).
	for _, bad := range []string{"bogus", "not_started", "in_progress"} {
		if _, err := p.UpdateRequirement(ctx, r.ID, RequirementPatch{StatusOverride: &bad}, "alice"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid status %q err = %v", bad, err)
		}
	}
	for _, s := range Statuses {
		if _, err := p.UpdateRequirement(ctx, r.ID, RequirementPatch{StatusOverride: &s}, "alice"); err != nil {
			t.Fatalf("status %q rejected: %v", s, err)
		}
	}
	// Query filter (ILIKE on title).
	_, total, err = p.ListRequirements(ctx, RequirementFilter{Query: "account MANAGEMENT"})
	if err != nil || total != 1 {
		t.Fatalf("query filter total = %d, %v", total, err)
	}
}

func TestPostgresEvidence(t *testing.T) {
	p, ctx := openTestPostgres(t)
	res, err := p.ImportFramework(ctx, testDoc(t), "importer")
	if err != nil {
		t.Fatal(err)
	}
	reqs, _, err := p.ListRequirements(ctx, RequirementFilter{FrameworkID: res.Framework.ID})
	if err != nil {
		t.Fatal(err)
	}
	r1, r2 := reqs[0], reqs[1]

	// Unknown requirement id → ErrNotFound.
	_, err = p.CreateEvidence(ctx, Evidence{Title: "x", RequirementIDs: []string{"00000000-0000-0000-0000-000000000000"}}, "bob")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown requirement err = %v", err)
	}
	_, err = p.CreateEvidence(ctx, Evidence{Title: "x", RequirementIDs: []string{"not-a-uuid"}}, "bob")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("malformed requirement err = %v", err)
	}

	until := time.Date(2027, 6, 30, 0, 0, 0, 0, time.UTC)
	e1, err := p.CreateEvidence(ctx, Evidence{
		Title: "Policy PDF", Description: "signed policy", FileName: "policy.pdf", ContentType: "application/pdf",
		SizeBytes: 1234, Sha256: "abc123", ValidUntil: &until, RequirementIDs: []string{r1.ID},
	}, "bob")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if e1.ID == "" || e1.UploadedBy != "bob" || len(e1.RequirementIDs) != 1 || e1.RequirementIDs[0] != r1.ID {
		t.Fatalf("e1 = %+v", e1)
	}
	if e1.Kind != EvidenceKindFile || e1.URL != "" {
		t.Fatalf("default kind = %q url = %q", e1.Kind, e1.URL)
	}
	if e1.ValidUntil == nil || !e1.ValidUntil.Equal(until) || e1.ValidFrom != nil {
		t.Fatalf("e1 validity = %v / %v", e1.ValidFrom, e1.ValidUntil)
	}

	time.Sleep(5 * time.Millisecond) // ensure distinct created_at ordering
	e2, err := p.CreateEvidence(ctx, Evidence{Title: "Screenshot", FileName: "shot.png", Sha256: "def456"}, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if e2.RequirementIDs == nil || len(e2.RequirementIDs) != 0 {
		t.Fatalf("e2.RequirementIDs = %#v; want empty non-nil", e2.RequirementIDs)
	}

	// List: newest first.
	all, total, err := p.ListEvidence(ctx, EvidenceFilter{})
	if err != nil || total != 2 || len(all) != 2 || all[0].ID != e2.ID || all[1].ID != e1.ID {
		t.Fatalf("ListEvidence = %+v total %d, %v", all, total, err)
	}
	// By requirement id.
	byReq, total, err := p.ListEvidence(ctx, EvidenceFilter{RequirementID: r1.ID})
	if err != nil || total != 1 || len(byReq) != 1 || byReq[0].ID != e1.ID {
		t.Fatalf("by requirement = %+v total %d, %v", byReq, total, err)
	}
	_, total, err = p.ListEvidence(ctx, EvidenceFilter{RequirementID: r2.ID})
	if err != nil || total != 0 {
		t.Fatalf("by unrelated requirement total = %d, %v", total, err)
	}
	// Query.
	_, total, err = p.ListEvidence(ctx, EvidenceFilter{Query: "SHOT.png"})
	if err != nil || total != 1 {
		t.Fatalf("query total = %d, %v", total, err)
	}

	got, err := p.GetEvidence(ctx, e1.ID)
	if err != nil || len(got.RequirementIDs) != 1 {
		t.Fatalf("GetEvidence = %+v, %v", got, err)
	}

	audit, err := p.ListAudit(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if audit[0].Action != "evidence.create" || audit[1].Action != "evidence.create" || audit[2].Action != "framework.import" {
		t.Fatalf("audit order = %s %s %s", audit[0].Action, audit[1].Action, audit[2].Action)
	}
	ids, _ := audit[1].Detail["requirementIds"].([]any)
	if len(ids) != 1 || ids[0] != r1.ID {
		t.Fatalf("evidence.create detail = %+v", audit[1].Detail)
	}
	if _, has := audit[1].Detail["activityIds"]; has {
		t.Fatalf("evidence.create detail must not carry activityIds: %+v", audit[1].Detail)
	}
}

func TestPostgresPagination(t *testing.T) {
	p, ctx := openTestPostgres(t)
	res, err := p.ImportFramework(ctx, testDoc(t), "importer")
	if err != nil {
		t.Fatal(err)
	}
	fw := res.Framework.ID

	page1, total, err := p.ListRequirements(ctx, RequirementFilter{FrameworkID: fw, Limit: 2})
	if err != nil || total != 3 || len(page1) != 2 {
		t.Fatalf("page1 = %d rows, total %d, %v", len(page1), total, err)
	}
	page2, total, err := p.ListRequirements(ctx, RequirementFilter{FrameworkID: fw, Limit: 2, Offset: 2})
	if err != nil || total != 3 || len(page2) != 1 || page2[0].ControlID != "ac-2" {
		t.Fatalf("page2 = %+v, total %d, %v", page2, total, err)
	}
	page3, total, err := p.ListRequirements(ctx, RequirementFilter{FrameworkID: fw, Limit: 2, Offset: 10})
	if err != nil || total != 3 || len(page3) != 0 || page3 == nil {
		t.Fatalf("page3 = %#v, total %d, %v", page3, total, err)
	}
	// Default limit when zero.
	all, total, err := p.ListRequirements(ctx, RequirementFilter{})
	if err != nil || total != 3 || len(all) != 3 {
		t.Fatalf("all = %d rows, total %d, %v", len(all), total, err)
	}
}

func TestPostgresNotFound(t *testing.T) {
	p, ctx := openTestPostgres(t)
	const missing = "00000000-0000-0000-0000-000000000000"
	checks := map[string]error{}
	_, err := p.GetFramework(ctx, missing)
	checks["GetFramework"] = err
	_, err = p.GetFramework(ctx, "garbage")
	checks["GetFramework(garbage)"] = err
	_, err = p.GetRequirement(ctx, missing)
	checks["GetRequirement"] = err
	_, err = p.GetRequirement(ctx, "garbage")
	checks["GetRequirement(garbage)"] = err
	s := StatusImplemented
	_, err = p.UpdateRequirement(ctx, missing, RequirementPatch{StatusOverride: &s}, "x")
	checks["UpdateRequirement"] = err
	_, err = p.GetEvidence(ctx, missing)
	checks["GetEvidence"] = err
	_, err = p.GetEvidence(ctx, "garbage")
	checks["GetEvidence(garbage)"] = err
	_, err = p.GetPartTracking(ctx, missing)
	checks["GetPartTracking"] = err
	_, err = p.GetPartTracking(ctx, "garbage")
	checks["GetPartTracking(garbage)"] = err
	_, err = p.UpsertPartTracking(ctx, missing, "ac-1_smt", PartPatch{Status: &s}, "x")
	checks["UpsertPartTracking"] = err
	_, err = p.UpsertPartTracking(ctx, "garbage", "ac-1_smt", PartPatch{Status: &s}, "x")
	checks["UpsertPartTracking(garbage)"] = err
	for name, err := range checks {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s err = %v; want ErrNotFound", name, err)
		}
	}
	// Filters with malformed ids yield empty results, not errors.
	rows, total, err := p.ListRequirements(ctx, RequirementFilter{FrameworkID: "garbage"})
	if err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("ListRequirements(garbage) = %v, %d, %v", rows, total, err)
	}
	ev, total, err := p.ListEvidence(ctx, EvidenceFilter{RequirementID: "garbage"})
	if err != nil || total != 0 || len(ev) != 0 {
		t.Fatalf("ListEvidence(garbage) = %v, %d, %v", ev, total, err)
	}
}
