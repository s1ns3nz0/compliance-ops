package store

import (
	"reflect"
	"testing"
)

func TestPostgresMigration0008ScopeCategories(t *testing.T) {
	pool, ctx := scratchDatabase(t)
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"0001_init.sql", "0002_oscal_alignment.sql", "0003_params_activity_evidence.sql", "0004_part_tracking.sql", "0005_references.sql", "0006_oscal_uploads.sql", "0007_assessments_scope.sql"} {
		if err := applyMigration(ctx, pool, m); err != nil {
			t.Fatalf("apply %s: %v", m, err)
		}
	}
	var fwID, reqID string
	if err := pool.QueryRow(ctx, `INSERT INTO frameworks(oscal_document_id,title,short_name) VALUES('scope-migration','Scope','Other') RETURNING id::text`).Scan(&fwID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO requirements(framework_id,control_id,title,parts,status) VALUES($1,'c-1','One','[{"id":"c-1_smt","name":"statement","parts":[{"id":"c-1_smt.a","name":"item"},{"id":"c-1_smt.b","name":"item"},{"id":"c-1_smt.c","name":"item"}]}]','implemented') RETURNING id::text`, fwID).Scan(&reqID); err != nil {
		t.Fatal(err)
	}
	legacy := []struct{ part, scope, status string }{{"c-1_smt.a", "DEMO: fictional production service scope", "implemented"}, {"c-1_smt.b", "node-operator repository at commit 8f4121f", "partial"}, {"c-1_smt.c", "  Arbitrary Area  ", "planned"}}
	for _, row := range legacy {
		if _, err := pool.Exec(ctx, `INSERT INTO part_tracking(requirement_id,part_id,status,scope) VALUES($1,$2,$3,$4)`, reqID, row.part, row.status, row.scope); err != nil {
			t.Fatal(err)
		}
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT pt.part_id,pt.status,sc.name FROM part_tracking pt JOIN part_scope_categories psc USING(requirement_id,part_id) JOIN scope_categories sc ON sc.id=psc.scope_category_id WHERE pt.requirement_id=$1 ORDER BY pt.part_id`, reqID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var p, s, n string
		if err := rows.Scan(&p, &s, &n); err != nil {
			t.Fatal(err)
		}
		got = append(got, p+"|"+s+"|"+n)
	}
	rows.Close()
	want := []string{"c-1_smt.a|implemented|Demo production service", "c-1_smt.b|partial|node-operator", "c-1_smt.c|planned|Arbitrary Area"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%v", got)
	}
	var hasScope bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='part_tracking' AND column_name='scope')`).Scan(&hasScope); err != nil || hasScope {
		t.Fatalf("scope column remains: %v", err)
	}
}
