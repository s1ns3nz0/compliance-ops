package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
)

const partsCatalog = `{
  "catalog": {
    "uuid": "22222222-3333-4444-5555-666666666666",
    "metadata": {"title": "NIST Special Publication 800-53 Revision 5: Security and Privacy Controls"},
    "controls": [
      {"id": "ac-2", "title": "Account Management",
       "links": [{"href": "#ac-3", "rel": "related"}, {"href": "#ia-2", "rel": "related"}, {"href": "#res-1", "rel": "external_reference", "text": "SA-11"}],
       "parts": [
         {"id": "ac-2_smt", "name": "statement", "parts": [
           {"id": "ac-2_smt.a", "name": "item", "props": [{"name": "label", "value": "a."}], "prose": "Define account types."},
           {"id": "ac-2_smt.b", "name": "item", "props": [{"name": "label", "value": "b."}], "prose": "Assign managers.", "parts": [
             {"id": "ac-2_smt.b.1", "name": "item", "props": [{"name": "label", "value": "1."}], "prose": "Nested."}
           ]}
         ]},
         {"id": "ac-2_gdn", "name": "guidance", "prose": "Guidance."}
       ]},
      {"id": "ac-3", "title": "Access Enforcement"}
    ],
    "back-matter": {"resources": [{"uuid": "res-1", "title": "SP800-53", "citation": {"text": "JTF (2020)"}, "rlinks": [{"href": "https://doi.org/x"}]}]}
  }
}`

// allVersions is every embedded migration version, in order.
var allVersions = []string{"0001_init", "0002_oscal_alignment", "0003_params_activity_evidence", "0004_part_tracking", "0005_references", "0006_oscal_uploads", "0007_assessments_scope", "0008_scope_categories"}

func partsDoc(t *testing.T) oscal.Document {
	t.Helper()
	docs, err := oscal.ParseJSON([]byte(partsCatalog))
	if err != nil {
		t.Fatal(err)
	}
	return docs[0]
}

// scratchDatabase creates a throwaway database next to DATABASE_URL so a
// migration can be exercised from a clean 0001 state; dropped on cleanup.
func scratchDatabase(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	raw := os.Getenv("DATABASE_URL")
	if raw == "" {
		t.Skip("DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(admin.Close)
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	name := "compliance_migtest_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatalf("create scratch database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`)
	})
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatalf("connect scratch: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, ctx
}

// TestPostgresMigration0002FromLegacyData applies 0001 alone, seeds rows with
// the retired status vocabulary, then runs Migrate and checks the data
// migration and new schema.
func TestPostgresMigration0002FromLegacyData(t *testing.T) {
	pool, ctx := scratchDatabase(t)
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(ctx, pool, "0001_init.sql"); err != nil {
		t.Fatalf("apply 0001: %v", err)
	}
	var fwID string
	if err := pool.QueryRow(ctx, `INSERT INTO frameworks (oscal_document_id, title) VALUES ('legacy-doc', 'Legacy') RETURNING id::text`).Scan(&fwID); err != nil {
		t.Fatal(err)
	}
	legacy := map[string]string{"c-1": "not_started", "c-2": "in_progress", "c-3": "implemented", "c-4": "not_applicable", "c-5": "not_started"}
	for cid, st := range legacy {
		if _, err := pool.Exec(ctx, `INSERT INTO requirements (framework_id, control_id, title, status) VALUES ($1, $2, $2, $3)`, fwID, cid, st); err != nil {
			t.Fatalf("seed %s: %v", cid, err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO evidence (title, file_name, sha256) VALUES ('old file', 'f.pdf', 'abc')`); err != nil {
		t.Fatal(err)
	}
	// Legacy schema must reject the new vocabulary (sanity check of the fixture).
	if _, err := pool.Exec(ctx, `INSERT INTO requirements (framework_id, control_id, status) VALUES ($1, 'c-x', 'planned')`, fwID); err == nil {
		t.Fatalf("0001 accepted 'planned'; fixture is not a legacy schema")
	}

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate twice: %v", err)
	}
	var versions []string
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var v string
		_ = rows.Scan(&v)
		versions = append(versions, v)
	}
	rows.Close()
	if !reflect.DeepEqual(versions, allVersions) {
		t.Fatalf("versions = %v", versions)
	}

	var stale int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM requirements WHERE status NOT IN ('implemented','partial','planned','alternative','not_applicable')`).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Fatalf("%d requirements still carry a legacy status", stale)
	}
	counts := map[string]int{}
	rows, err = pool.Query(ctx, `SELECT status, count(*)::int FROM requirements GROUP BY status`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		var n int
		_ = rows.Scan(&s, &n)
		counts[s] = n
	}
	rows.Close()
	if !reflect.DeepEqual(counts, map[string]int{"planned": 2, "partial": 1, "implemented": 1, "not_applicable": 1}) {
		t.Fatalf("status counts after migration = %v", counts)
	}
	// New default and constraint.
	var def string
	if err := pool.QueryRow(ctx, `INSERT INTO requirements (framework_id, control_id) VALUES ($1, 'c-new') RETURNING status`, fwID).Scan(&def); err != nil || def != "planned" {
		t.Fatalf("default status = %q, %v", def, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO requirements (framework_id, control_id, status) VALUES ($1, 'c-bad', 'not_started')`, fwID); err == nil {
		t.Fatalf("new constraint accepted not_started")
	}
	for _, s := range Statuses {
		if _, err := pool.Exec(ctx, `INSERT INTO requirements (framework_id, control_id, status) VALUES ($1, $2, $3)`, fwID, "ok-"+s, s); err != nil {
			t.Fatalf("new constraint rejected %q: %v", s, err)
		}
	}
	// Existing evidence defaults to kind=file; short_name/parts/related default.
	var kind, u, shortName string
	var parts, related string
	if err := pool.QueryRow(ctx, `SELECT kind, url FROM evidence`).Scan(&kind, &u); err != nil || kind != "file" || u != "" {
		t.Fatalf("legacy evidence kind=%q url=%q err=%v", kind, u, err)
	}
	if err := pool.QueryRow(ctx, `SELECT short_name FROM frameworks WHERE id = $1`, fwID).Scan(&shortName); err != nil || shortName != "" {
		t.Fatalf("short_name = %q, %v", shortName, err)
	}
	if err := pool.QueryRow(ctx, `SELECT parts::text, related::text FROM requirements WHERE control_id = 'c-1'`).Scan(&parts, &related); err != nil || parts != "[]" || related != "[]" {
		t.Fatalf("parts=%q related=%q err=%v", parts, related, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO evidence (title, kind) VALUES ('bad', 'blob')`); err == nil {
		t.Fatalf("evidence kind check missing")
	}
	// The store works on the migrated schema, and re-import fills short_name only when empty.
	p := NewPostgres(pool)
	fw, err := p.GetFramework(ctx, fwID)
	if err != nil || fw.ShortName != "" {
		t.Fatalf("GetFramework = %+v, %v", fw, err)
	}
	if _, err := p.GetPartTracking(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("part_tracking table missing? %v", err)
	}
	// 0003 ran as part of Migrate: params column present, review columns gone.
	var hasParams, hasReview bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'requirements' AND column_name = 'params')`).Scan(&hasParams); err != nil || !hasParams {
		t.Fatalf("params column missing: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'evidence' AND column_name IN ('review_state','reviewer','review_comment','reviewed_at'))`).Scan(&hasReview); err != nil || hasReview {
		t.Fatalf("review columns still present: %v", err)
	}
}

// TestPostgresMigration0003FromReviewedEvidence applies 0001+0002, seeds
// evidence with populated review columns plus activities, then runs Migrate
// and checks that the review columns are gone while every other value and
// row survives, and that the new link table cascades.
func TestPostgresMigration0003FromReviewedEvidence(t *testing.T) {
	pool, ctx := scratchDatabase(t)
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"0001_init.sql", "0002_oscal_alignment.sql"} {
		if err := applyMigration(ctx, pool, m); err != nil {
			t.Fatalf("apply %s: %v", m, err)
		}
	}
	var fwID, reqID, actID, evID string
	if err := pool.QueryRow(ctx, `INSERT INTO frameworks (oscal_document_id, title) VALUES ('doc-3', 'Three') RETURNING id::text`).Scan(&fwID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO requirements (framework_id, control_id, title, parts) VALUES ($1, 'ac-1', 'Policy', '[{"id":"ac-1_smt","name":"statement"}]') RETURNING id::text`, fwID).Scan(&reqID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO implementation_activities (requirement_id, part_id, body) VALUES ($1, 'ac-1_smt', 'done') RETURNING id::text`, reqID).Scan(&actID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO evidence (title, description, file_name, content_type, size_bytes, sha256, valid_until, review_state, reviewer, review_comment, reviewed_at, uploaded_by, kind)
		VALUES ('Reviewed', 'desc', 'a.pdf', 'application/pdf', 42, 'deadbeef', '2027-01-31', 'approved', 'carol', 'ok', now(), 'bob', 'file') RETURNING id::text`).Scan(&evID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO evidence (title, kind, url, review_state) VALUES ('Rejected link', 'link', 'https://x.example/a', 'rejected')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO evidence_requirements (evidence_id, requirement_id) VALUES ($1, $2)`, evID, reqID); err != nil {
		t.Fatal(err)
	}
	// Sanity: the 0002 schema still has the review columns and no params / link table.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM information_schema.columns WHERE table_name = 'evidence' AND column_name IN ('review_state','reviewer','review_comment','reviewed_at')`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("fixture review columns = %d, %v", n, err)
	}
	if _, err := pool.Exec(ctx, `SELECT params FROM requirements`); err == nil {
		t.Fatalf("0002 already has params; fixture is not a 0002 schema")
	}

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate twice: %v", err)
	}
	var versions []string
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var v string
		_ = rows.Scan(&v)
		versions = append(versions, v)
	}
	rows.Close()
	if !reflect.DeepEqual(versions, allVersions) {
		t.Fatalf("versions = %v", versions)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM information_schema.columns WHERE table_name = 'evidence' AND column_name IN ('review_state','reviewer','review_comment','reviewed_at')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("review columns after migration = %d, %v", n, err)
	}
	var idx bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'evidence_review_state_idx')`).Scan(&idx); err != nil || idx {
		t.Fatalf("review index still present: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM evidence`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("evidence rows after migration = %d, %v", n, err)
	}
	var params string
	if err := pool.QueryRow(ctx, `SELECT params::text FROM requirements WHERE id = $1`, reqID).Scan(&params); err != nil || params != "[]" {
		t.Fatalf("params default = %q, %v", params, err)
	}

	// Data kept and readable through the store; the activity became the
	// description of the (single trackable) statement part.
	p := NewPostgres(pool)
	ev, err := p.GetEvidence(ctx, evID)
	if err != nil || ev.Title != "Reviewed" || ev.Description != "desc" || ev.FileName != "a.pdf" || ev.SizeBytes != 42 || ev.Sha256 != "deadbeef" || ev.UploadedBy != "bob" || ev.ValidUntil == nil {
		t.Fatalf("migrated evidence = %+v, %v", ev, err)
	}
	if !reflect.DeepEqual(ev.RequirementIDs, []string{reqID}) {
		t.Fatalf("migrated links = %+v", ev)
	}
	parts, err := p.GetPartTracking(ctx, reqID)
	if err != nil || len(parts) != 1 || parts[0].PartID != "ac-1_smt" || parts[0].Status != StatusPlanned || !strings.Contains(parts[0].Description, "done") {
		t.Fatalf("migrated part tracking = %+v, %v", parts, err)
	}
	_ = actID
}

func TestPostgresFrameworkShortName(t *testing.T) {
	p, ctx := openTestPostgres(t)
	res, err := p.ImportFramework(ctx, partsDoc(t), "importer")
	if err != nil {
		t.Fatal(err)
	}
	if res.Framework.ShortName != "NIST SP 800-53" {
		t.Fatalf("derived shortName = %q", res.Framework.ShortName)
	}
	if _, err := p.UpdateFramework(ctx, res.Framework.ID, "", "alice"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty shortName err = %v", err)
	}
	if _, err := p.UpdateFramework(ctx, "00000000-0000-0000-0000-000000000000", "X", "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing framework err = %v", err)
	}
	if _, err := p.UpdateFramework(ctx, "garbage", "X", "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("garbage id err = %v", err)
	}
	fw, err := p.UpdateFramework(ctx, res.Framework.ID, "800-53r5", "alice")
	if err != nil || fw.ShortName != "800-53r5" || fw.RequirementCount != 2 {
		t.Fatalf("UpdateFramework = %+v, %v", fw, err)
	}
	// Re-import keeps the edit.
	again, err := p.ImportFramework(ctx, partsDoc(t), "importer")
	if err != nil || again.Framework.ShortName != "800-53r5" {
		t.Fatalf("re-import = %+v, %v", again.Framework, err)
	}
	list, _ := p.ListFrameworks(ctx)
	if len(list) != 1 || list[0].ShortName != "800-53r5" {
		t.Fatalf("ListFrameworks = %+v", list)
	}
	audit, _ := p.ListAudit(ctx, 10)
	if audit[1].Action != "framework.update" || audit[1].Actor != "alice" {
		t.Fatalf("audit = %+v", audit)
	}
	change, _ := audit[1].Detail["shortName"].(map[string]any)
	if change["from"] != "NIST SP 800-53" || change["to"] != "800-53r5" {
		t.Fatalf("audit detail = %+v", audit[1].Detail)
	}
}

func TestPostgresPartsRoundtrip(t *testing.T) {
	p, ctx := openTestPostgres(t)
	doc := partsDoc(t)
	res, err := p.ImportFramework(ctx, doc, "importer")
	if err != nil {
		t.Fatal(err)
	}
	reqs, _, err := p.ListRequirements(ctx, RequirementFilter{FrameworkID: res.Framework.ID})
	if err != nil || len(reqs) != 2 {
		t.Fatalf("list = %+v, %v", reqs, err)
	}
	ac2, err := p.GetRequirement(ctx, reqs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ac2.Parts, doc.Controls[0].Parts) {
		t.Fatalf("parts roundtrip mismatch:\n got %+v\nwant %+v", ac2.Parts, doc.Controls[0].Parts)
	}
	if !reflect.DeepEqual(ac2.Related, []string{"ac-3", "ia-2"}) {
		t.Fatalf("related = %v", ac2.Related)
	}
	wantRefs := []oscal.Reference{{UUID: "res-1", Title: "SP800-53", Citation: "JTF (2020)", URL: "https://doi.org/x", Text: "SA-11"}}
	if !reflect.DeepEqual(ac2.References, wantRefs) {
		t.Fatalf("references roundtrip = %+v", ac2.References)
	}
	ac3 := reqs[1]
	if ac3.Parts == nil || len(ac3.Parts) != 0 || ac3.Related == nil || len(ac3.Related) != 0 || ac3.References == nil || len(ac3.References) != 0 {
		t.Fatalf("ac-3 must have empty non-nil parts/related/references: %#v %#v %#v", ac3.Parts, ac3.Related, ac3.References)
	}
	if ac2.Params == nil || len(ac2.Params) != 0 || ac3.Params == nil {
		t.Fatalf("params must be empty non-nil: %#v %#v", ac2.Params, ac3.Params)
	}
	// Re-import updates parts and params.
	doc.Controls[0].Parts = doc.Controls[0].Parts[:1]
	doc.Controls[0].Related = nil
	doc.Controls[0].References = nil
	doc.Controls[0].Params = []oscal.Param{
		{ID: "ac-2_prm_1", Label: "personnel or roles"},
		{ID: "ac-2_odp.02", Choices: []string{"disable", "remove"}, HowMany: "one-or-more"},
		{ID: "ac-2_odp.03", Values: []string{"30 days"}},
	}
	if _, err := p.ImportFramework(ctx, doc, "importer"); err != nil {
		t.Fatal(err)
	}
	ac2, _ = p.GetRequirement(ctx, ac2.ID)
	if len(ac2.Parts) != 1 || len(ac2.Related) != 0 || ac2.Related == nil || len(ac2.References) != 0 || ac2.References == nil {
		t.Fatalf("after re-import parts=%d related=%#v references=%#v", len(ac2.Parts), ac2.Related, ac2.References)
	}
	if !reflect.DeepEqual(ac2.Params, doc.Controls[0].Params) {
		t.Fatalf("params roundtrip mismatch:\n got %+v\nwant %+v", ac2.Params, doc.Controls[0].Params)
	}
	// List rows carry params too (the API strips them alongside parts).
	list, _, _ := p.ListRequirements(ctx, RequirementFilter{FrameworkID: res.Framework.ID})
	if len(list[0].Params) != 3 {
		t.Fatalf("list params = %+v", list[0].Params)
	}
}

func TestPostgresLinkEvidence(t *testing.T) {
	p, ctx := openTestPostgres(t)
	res, err := p.ImportFramework(ctx, partsDoc(t), "importer")
	if err != nil {
		t.Fatal(err)
	}
	reqs, _, _ := p.ListRequirements(ctx, RequirementFilter{FrameworkID: res.Framework.ID})
	r1 := reqs[0]

	for _, bad := range []string{"", "ftp://x.example/a", "https://u:p@x.example/a", "https:///nohost", "not a url"} {
		if _, err := p.CreateEvidence(ctx, Evidence{Kind: EvidenceKindLink, URL: bad, Title: "x", RequirementIDs: []string{r1.ID}}, "bob"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("url %q err = %v", bad, err)
		}
	}
	if _, err := p.CreateEvidence(ctx, Evidence{Kind: "blob", Title: "x"}, "bob"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad kind err = %v", err)
	}
	link, err := p.CreateEvidence(ctx, Evidence{Kind: EvidenceKindLink, URL: "https://example.atlassian.net/browse/SEC-1", Title: "Ticket",
		FileName: "ignored.pdf", Sha256: "ignored", SizeBytes: 9, RequirementIDs: []string{r1.ID}}, "bob")
	if err != nil {
		t.Fatalf("create link: %v", err)
	}
	if link.Kind != EvidenceKindLink || link.URL != "https://example.atlassian.net/browse/SEC-1" || link.FileName != "" || link.Sha256 != "" || link.SizeBytes != 0 || link.ContentType != "" {
		t.Fatalf("link = %+v", link)
	}
	time.Sleep(5 * time.Millisecond)
	file, err := p.CreateEvidence(ctx, Evidence{Title: "File", FileName: "a.pdf", Sha256: "abc", URL: "https://should-be-dropped.example", RequirementIDs: []string{r1.ID}}, "bob")
	if err != nil || file.Kind != EvidenceKindFile || file.URL != "" {
		t.Fatalf("file = %+v, %v", file, err)
	}

	got, err := p.GetEvidence(ctx, link.ID)
	if err != nil || got.Kind != EvidenceKindLink || got.URL != link.URL {
		t.Fatalf("GetEvidence = %+v, %v", got, err)
	}
	links, total, err := p.ListEvidence(ctx, EvidenceFilter{Kind: EvidenceKindLink})
	if err != nil || total != 1 || len(links) != 1 || links[0].ID != link.ID {
		t.Fatalf("links = %+v total %d, %v", links, total, err)
	}
	_, total, err = p.ListEvidence(ctx, EvidenceFilter{Kind: EvidenceKindFile})
	if err != nil || total != 1 {
		t.Fatalf("files total = %d, %v", total, err)
	}
	_, total, err = p.ListEvidence(ctx, EvidenceFilter{Query: "atlassian"})
	if err != nil || total != 1 {
		t.Fatalf("url query total = %d, %v", total, err)
	}
	_, total, err = p.ListEvidence(ctx, EvidenceFilter{RequirementID: r1.ID})
	if err != nil || total != 2 {
		t.Fatalf("by requirement total = %d, %v", total, err)
	}
	audit, _ := p.ListAudit(ctx, 10)
	// audit[0] is the file create, audit[1] the link create.
	if audit[1].Action != "evidence.create" || audit[1].Detail["kind"] != EvidenceKindLink || audit[1].Detail["url"] != link.URL {
		t.Fatalf("link audit = %+v", audit[1])
	}
	if audit[0].Detail["kind"] != EvidenceKindFile || audit[0].Detail["sha256"] != "abc" {
		t.Fatalf("file audit = %+v", audit[0])
	}
}

// TestPostgresMigration0004FromActivities applies 0001..0003, seeds a tracked
// requirement with two activities on statement items plus a second
// requirement with activities on non-trackable parts, then runs Migrate and
// checks the part_tracking seeding, activity → description conversion,
// derived values and the dropped tables.
func TestPostgresMigration0004FromActivities(t *testing.T) {
	pool, ctx := scratchDatabase(t)
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"0001_init.sql", "0002_oscal_alignment.sql", "0003_params_activity_evidence.sql"} {
		if err := applyMigration(ctx, pool, m); err != nil {
			t.Fatalf("apply %s: %v", m, err)
		}
	}
	var fwID, ac2ID, ac3ID, ac4ID, evID string
	if err := pool.QueryRow(ctx, `INSERT INTO frameworks (oscal_document_id, title) VALUES ('doc-4', 'Four') RETURNING id::text`).Scan(&fwID); err != nil {
		t.Fatal(err)
	}
	const ac2Parts = `[{"id":"ac-2_smt","name":"statement","parts":[{"id":"ac-2_smt.a","name":"item","label":"a.","prose":"A"},{"id":"ac-2_smt.b","name":"item","label":"b.","prose":"B"}]},{"id":"ac-2_gdn","name":"guidance","prose":"G"}]`
	if err := pool.QueryRow(ctx, `INSERT INTO requirements (framework_id, control_id, title, parts, status, owner, due_date, notes)
		VALUES ($1, 'ac-2', 'Account Management', $2, 'implemented', 'alice', '2026-12-31', 'keep me') RETURNING id::text`, fwID, ac2Parts).Scan(&ac2ID); err != nil {
		t.Fatal(err)
	}
	// ac-3: statement without items, partial with a due date, one activity on the statement.
	if err := pool.QueryRow(ctx, `INSERT INTO requirements (framework_id, control_id, title, parts, status, owner, due_date)
		VALUES ($1, 'ac-3', 'Enforcement', '[{"id":"ac-3_smt","name":"statement","prose":"E"}]', 'partial', 'bob', '2026-09-25') RETURNING id::text`, fwID).Scan(&ac3ID); err != nil {
		t.Fatal(err)
	}
	// ac-4: untouched requirement with activities on non-trackable parts (statement with items, guidance).
	if err := pool.QueryRow(ctx, `INSERT INTO requirements (framework_id, control_id, title, parts)
		VALUES ($1, 'ac-4', 'Flow', '[{"id":"ac-4_smt","name":"statement","parts":[{"id":"ac-4_smt.a","name":"item"}]},{"id":"ac-4_gdn","name":"guidance"}]') RETURNING id::text`, fwID).Scan(&ac4ID); err != nil {
		t.Fatal(err)
	}
	// ac-5: untouched, no activities → must stay without rows.
	if _, err := pool.Exec(ctx, `INSERT INTO requirements (framework_id, control_id, title) VALUES ($1, 'ac-5', 'Idle')`, fwID); err != nil {
		t.Fatal(err)
	}
	acts := []struct{ req, part, actor, body, at string }{
		{ac2ID, "ac-2_smt.a", "alice", "Types defined in IAM-POL-02.", "2026-08-12 09:00:00+00"},
		{ac2ID, "ac-2_smt.a", "bob", "Reviewed by security.", "2026-08-13 09:00:00+00"},
		{ac2ID, "ac-2_smt.b", "alice", "Managers assigned.", "2026-08-14 09:00:00+00"},
		{ac3ID, "ac-3_smt", "bob", "RBAC via OPA.", "2026-09-01 09:00:00+00"},
		{ac4ID, "ac-4_smt", "carol", "General note.", "2026-09-02 09:00:00+00"},
		{ac4ID, "ac-4_gdn", "carol", "Guidance note.", "2026-09-03 09:00:00+00"},
	}
	var firstAct string
	for i, a := range acts {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO implementation_activities (requirement_id, part_id, actor, body, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $5) RETURNING id::text`, a.req, a.part, a.actor, a.body, a.at).Scan(&id); err != nil {
			t.Fatalf("seed activity %d: %v", i, err)
		}
		if i == 0 {
			firstAct = id
		}
	}
	if err := pool.QueryRow(ctx, `INSERT INTO evidence (title, kind, url) VALUES ('Linked', 'link', 'https://x.example/a') RETURNING id::text`).Scan(&evID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO evidence_requirements (evidence_id, requirement_id) VALUES ($1, $2)`, evID, ac2ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO evidence_activities (evidence_id, activity_id) VALUES ($1, $2)`, evID, firstAct); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate twice: %v", err)
	}
	var versions []string
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var v string
		_ = rows.Scan(&v)
		versions = append(versions, v)
	}
	rows.Close()
	if !reflect.DeepEqual(versions, allVersions) {
		t.Fatalf("versions = %v", versions)
	}
	// Activity tables are gone.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM information_schema.tables WHERE table_name IN ('implementation_activities', 'evidence_activities')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("activity tables after migration = %d, %v", n, err)
	}
	// status_override column with its check.
	if _, err := pool.Exec(ctx, `UPDATE requirements SET status_override = 'bogus' WHERE id = $1`, ac2ID); err == nil {
		t.Fatalf("status_override check missing")
	}

	p := NewPostgres(pool)
	// ac-2: rows a and b seeded with implemented/alice/due and the activity text.
	parts, err := p.GetPartTracking(ctx, ac2ID)
	if err != nil || len(parts) != 2 {
		t.Fatalf("ac-2 parts = %+v, %v", parts, err)
	}
	a, b := parts[0], parts[1]
	if a.PartID != "ac-2_smt.a" || a.Status != StatusImplemented || a.Owner != "alice" || a.DueDate == nil || !a.DueDate.Equal(time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("a = %+v", a)
	}
	if a.Description != "- 2026-08-12 (alice): Types defined in IAM-POL-02.\n- 2026-08-13 (bob): Reviewed by security." {
		t.Fatalf("a.Description = %q", a.Description)
	}
	if b.PartID != "ac-2_smt.b" || b.Status != StatusImplemented || b.Owner != "alice" || b.Description != "- 2026-08-14 (alice): Managers assigned." {
		t.Fatalf("b = %+v", b)
	}
	r2, err := p.GetRequirement(ctx, ac2ID)
	if err != nil || r2.Status != StatusImplemented || r2.DerivedStatus != StatusImplemented || r2.StatusOverride != "" || r2.Owner != "alice" || r2.Notes != "keep me" {
		t.Fatalf("ac-2 = %+v, %v", r2, err)
	}
	// The due date of an implemented requirement is not derived (open parts only).
	if r2.DueDate != nil {
		t.Fatalf("ac-2 due = %v, want nil (all parts implemented)", r2.DueDate)
	}
	// ac-3: single statement part keeps partial/bob/due; derived due survives because the part is open.
	parts, _ = p.GetPartTracking(ctx, ac3ID)
	if len(parts) != 1 || parts[0].PartID != "ac-3_smt" || parts[0].Status != StatusPartial || parts[0].Owner != "bob" || parts[0].Description != "- 2026-09-01 (bob): RBAC via OPA." {
		t.Fatalf("ac-3 parts = %+v", parts)
	}
	r3, _ := p.GetRequirement(ctx, ac3ID)
	if r3.Status != StatusPartial || r3.Owner != "bob" || r3.DueDate == nil || !r3.DueDate.Equal(time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("ac-3 = %+v", r3)
	}
	// ac-4: activities on non-trackable parts folded into the first trackable part, status planned.
	parts, _ = p.GetPartTracking(ctx, ac4ID)
	if len(parts) != 1 || parts[0].PartID != "ac-4_smt.a" || parts[0].Status != StatusPlanned {
		t.Fatalf("ac-4 parts = %+v", parts)
	}
	if d := parts[0].Description; !strings.Contains(d, "_Migrated from ac-4_smt:_\n- 2026-09-02 (carol): General note.") || !strings.Contains(d, "_Migrated from ac-4_gdn:_\n- 2026-09-03 (carol): Guidance note.") {
		t.Fatalf("ac-4 description = %q", d)
	}
	r4, _ := p.GetRequirement(ctx, ac4ID)
	if r4.Status != StatusPlanned || r4.Owner != "" || r4.DueDate != nil {
		t.Fatalf("ac-4 = %+v", r4)
	}
	// ac-5 has no rows.
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM part_tracking pt JOIN requirements r ON r.id = pt.requirement_id WHERE r.control_id = 'ac-5'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("ac-5 rows = %d, %v", n, err)
	}
	// Evidence survives on the requirement.
	ev, err := p.GetEvidence(ctx, evID)
	if err != nil || !reflect.DeepEqual(ev.RequirementIDs, []string{ac2ID}) {
		t.Fatalf("evidence = %+v, %v", ev, err)
	}
	// The migrated schema is fully usable.
	if _, err := p.UpsertPartTracking(ctx, ac2ID, "ac-2_smt.b", PartPatch{Status: strp(StatusPlanned)}, "x"); err != nil {
		t.Fatal(err)
	}
	if r2, _ = p.GetRequirement(ctx, ac2ID); r2.Status != StatusPartial {
		t.Fatalf("ac-2 after part change = %+v", r2)
	}
}
