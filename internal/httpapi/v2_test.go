package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
	"github.com/s1ns3nz0/compliance-ops/internal/store"
)

// partsCatalogJSON has a NIST-shaped control with labelled statement items
// and related links.
const partsCatalogJSON = `{
  "catalog": {
    "uuid": "5a1c2f7e-0000-4000-8000-000000000001",
    "metadata": { "title": "NIST Special Publication 800-53 Revision 5: Security and Privacy Controls", "last-modified": "2026-09-03T00:00:00Z" },
    "groups": [{ "id": "ac", "title": "Access Control", "controls": [
      { "id": "ac-2", "title": "Account Management",
        "links": [{"href": "#ac-3", "rel": "related"}, {"href": "#ac-5", "rel": "related"}, {"href": "#res-1", "rel": "external_reference", "text": "SA-11"}],
        "params": [
          {"id": "ac-02_odp.01", "label": "prerequisites and criteria"},
          {"id": "ac-02_odp.02", "select": {"how-many": "one-or-more", "choice": ["disable", "remove"]}}
        ],
        "parts": [
          {"id": "ac-2_smt", "name": "statement", "parts": [
            {"id": "ac-2_smt.a", "name": "item", "props": [{"name": "label", "value": "a."}], "prose": "Define and document the types of accounts."},
            {"id": "ac-2_smt.b", "name": "item", "props": [{"name": "label", "value": "b."}], "prose": "Assign account managers using {{ insert: param, ac-02_odp.01 }}; {{ insert: param, ac-02_odp.02 }} others."}
          ]},
          {"id": "ac-2_gdn", "name": "guidance", "prose": "Examples of system account types include individual, shared, group."}
        ]},
      { "id": "ac-3", "title": "Access Enforcement" }
    ]}],
    "back-matter": { "resources": [{"uuid": "res-1", "title": "BSIMM", "citation": {"text": "BSIMM 12"}, "rlinks": [{"href": "https://www.bsimm.com"}]}] }
  }
}`

func partsEnv(t *testing.T) (*env, map[string]store.Requirement) {
	t.Helper()
	docs, err := oscal.ParseJSON([]byte(partsCatalogJSON))
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, oscal.StaticRepository{Documents: docs})
	_, reqs := importAll(t, e)
	byControl := map[string]store.Requirement{}
	for _, r := range reqs {
		byControl[r.ControlID] = r
	}
	return e, byControl
}

func TestFrameworks_ShortNameDerivedAndEditable(t *testing.T) {
	e, _ := partsEnv(t)
	list := e.get(t, "/v1/frameworks")
	wantStatus(t, list, 200, "")
	fw := items(t, list)[0]
	if fw["shortName"] != "NIST SP 800-53" {
		t.Fatalf("derived shortName = %v", fw["shortName"])
	}
	id := fw["id"].(string)

	wantStatus(t, e.patchJSON(t, "/v1/frameworks/"+id, `{"shortName":""}`, nil), 400, "INVALID_BODY")
	wantStatus(t, e.patchJSON(t, "/v1/frameworks/"+id, `{"shortName":"`+strings.Repeat("x", 61)+`"}`, nil), 400, "INVALID_BODY")
	wantStatus(t, e.patchJSON(t, "/v1/frameworks/"+id, `{"title":"nope"}`, nil), 400, "INVALID_BODY")
	wantStatus(t, e.patchJSON(t, "/v1/frameworks/"+id, `{}`, nil), 400, "INVALID_BODY")
	wantStatus(t, e.patchJSON(t, "/v1/frameworks/missing", `{"shortName":"X"}`, nil), 404, "NOT_FOUND")

	r := e.patchJSON(t, "/v1/frameworks/"+id, `{"shortName":"  800-53 r5 "}`, map[string]string{"X-Actor": "editor"})
	wantStatus(t, r, 200, "")
	if r.body["shortName"] != "800-53 r5" || r.body["id"] != id || r.body["requirementCount"] != float64(2) {
		t.Fatalf("patched framework = %s", r.raw)
	}
	// Re-import keeps the user edit.
	wantStatus(t, e.postJSON(t, "/v1/frameworks/import", map[string]any{}, nil), 200, "")
	one := e.get(t, "/v1/frameworks/"+id)
	if one.body["shortName"] != "800-53 r5" {
		t.Fatalf("shortName after re-import = %v", one.body["shortName"])
	}
	audit, _ := e.store.ListAudit(context.Background(), 50)
	found := false
	for _, a := range audit {
		if a.Action == "framework.update" && a.Actor == "test-operator" && a.EntityID == id {
			found = true
			change := a.Detail["shortName"].(map[string]any)
			if change["from"] != "NIST SP 800-53" || change["to"] != "800-53 r5" {
				t.Fatalf("audit detail = %v", a.Detail)
			}
		}
	}
	if !found {
		t.Fatalf("framework.update audit row missing")
	}
}

func TestRequirements_DetailHasPartsRelatedTrackableParts(t *testing.T) {
	e, reqs := partsEnv(t)
	ac2 := reqs["ac-2"]
	d := e.get(t, "/v1/requirements/"+ac2.ID)
	wantStatus(t, d, 200, "")
	parts, ok := d.body["parts"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("parts = %v", d.body["parts"])
	}
	stmt := parts[0].(map[string]any)
	if stmt["id"] != "ac-2_smt" || stmt["name"] != "statement" {
		t.Fatalf("statement = %v", stmt)
	}
	sub := stmt["parts"].([]any)
	if len(sub) != 2 || sub[0].(map[string]any)["label"] != "a." || sub[1].(map[string]any)["id"] != "ac-2_smt.b" {
		t.Fatalf("statement items = %v", sub)
	}
	// Placeholders are resolved at import; params are exported on the detail.
	if got := sub[1].(map[string]any)["prose"]; got != "Assign account managers using [Assignment: organization-defined prerequisites and criteria]; [Selection (one or more): disable; remove] others." {
		t.Fatalf("resolved prose = %v", got)
	}
	params, ok := d.body["params"].([]any)
	if !ok || len(params) != 2 {
		t.Fatalf("params = %v", d.body["params"])
	}
	if p0 := params[0].(map[string]any); p0["id"] != "ac-02_odp.01" || p0["label"] != "prerequisites and criteria" {
		t.Fatalf("params[0] = %v", p0)
	}
	if p1 := params[1].(map[string]any); p1["howMany"] != "one-or-more" || len(p1["choices"].([]any)) != 2 {
		t.Fatalf("params[1] = %v", p1)
	}
	if list := e.get(t, "/v1/requirements?q=account"); len(items(t, list)) == 0 {
		t.Fatalf("list = %s", list.raw)
	} else if _, has := items(t, list)[0]["params"]; has {
		t.Fatalf("list rows must omit params: %s", list.raw)
	}
	if rel := d.body["related"].([]any); len(rel) != 2 || rel[0] != "ac-3" || rel[1] != "ac-5" {
		t.Fatalf("related = %v", d.body["related"])
	}
	// References are resolved against back-matter and always an array on
	// the detail; list rows omit them.
	refs, ok := d.body["references"].([]any)
	if !ok || len(refs) != 1 {
		t.Fatalf("references = %v", d.body["references"])
	}
	if r0 := refs[0].(map[string]any); r0["uuid"] != "res-1" || r0["title"] != "BSIMM" || r0["citation"] != "BSIMM 12" || r0["url"] != "https://www.bsimm.com" || r0["text"] != "SA-11" || r0["taskId"] != nil {
		t.Fatalf("references[0] = %v", r0)
	}
	if list := e.get(t, "/v1/requirements?q=account"); len(items(t, list)) == 0 {
		t.Fatalf("list = %s", list.raw)
	} else if _, has := items(t, list)[0]["references"]; has {
		t.Fatalf("list rows must omit references: %s", list.raw)
	}
	d3 := e.get(t, "/v1/requirements/"+reqs["ac-3"].ID)
	if refs3, ok := d3.body["references"].([]any); !ok || len(refs3) != 0 {
		t.Fatalf("ac-3 references must be an empty array: %s", d3.raw)
	}
	for _, gone := range []string{"activities", "activityCount"} {
		if _, has := d.body[gone]; has {
			t.Fatalf("%s must be gone: %s", gone, d.raw)
		}
	}
	// Trackable parts are the statement items, lazily planned.
	tps, ok := d.body["trackableParts"].([]any)
	if !ok || len(tps) != 2 {
		t.Fatalf("trackableParts = %v", d.body["trackableParts"])
	}
	tp0 := tps[0].(map[string]any)
	if tp0["partId"] != "ac-2_smt.a" || tp0["label"] != "a." || tp0["prose"] != "Define and document the types of accounts." || tp0["status"] != "planned" || tp0["owner"] != "" || tp0["description"] != "" {
		t.Fatalf("trackableParts[0] = %v", tp0)
	}
	for _, absent := range []string{"dueDate", "updatedAt"} {
		if _, has := tp0[absent]; has {
			t.Fatalf("%s must be omitted for an untracked part: %v", absent, tp0)
		}
	}
	if d.body["derivedStatus"] != "planned" {
		t.Fatalf("derivedStatus = %v", d.body["derivedStatus"])
	}
	if _, has := d.body["statusOverride"]; has {
		t.Fatalf("statusOverride must be omitted when unset: %s", d.raw)
	}
	// List rows carry derivedStatus too.
	if row := items(t, e.get(t, "/v1/requirements?q=account"))[0]; row["derivedStatus"] != "planned" || row["status"] != "planned" {
		t.Fatalf("list row = %v", row)
	}
	// A control without parts still renders arrays and a synthetic trackable part.
	ac3 := e.get(t, "/v1/requirements/"+reqs["ac-3"].ID)
	if tps := ac3.body["trackableParts"].([]any); len(tps) != 1 || tps[0].(map[string]any)["partId"] != "ac-3_smt" {
		t.Fatalf("ac-3 trackableParts = %v", ac3.body["trackableParts"])
	}
	if p, ok := ac3.body["parts"].([]any); !ok || len(p) != 0 {
		t.Fatalf("ac-3 parts = %v", ac3.body["parts"])
	}
	if p, ok := ac3.body["params"].([]any); !ok || len(p) != 0 {
		t.Fatalf("ac-3 params = %v", ac3.body["params"])
	}
	if rel, ok := ac3.body["related"].([]any); !ok || len(rel) != 0 {
		t.Fatalf("ac-3 related = %v", ac3.body["related"])
	}
	if d.body["status"] != "planned" {
		t.Fatalf("default status = %v", d.body["status"])
	}
}

func TestParts_PatchDerivesRequirementAndAudits(t *testing.T) {
	e, reqs := partsEnv(t)
	ac2 := reqs["ac-2"]
	base := "/v1/requirements/" + ac2.ID + "/parts/"
	scope, err := e.store.CreateScopeCategory(context.Background(), "production service", "alice")
	if err != nil {
		t.Fatal(err)
	}

	// Validation.
	wantStatus(t, e.patchJSON(t, base+"ac-2_smt.a", `{"nope":1}`, nil), 400, "INVALID_BODY")
	wantStatus(t, e.patchJSON(t, base+"ac-2_smt.a", `{"status":"done"}`, nil), 400, "INVALID_BODY")
	wantStatus(t, e.patchJSON(t, base+"ac-2_smt.a", `{"owner":"`+strings.Repeat("o", 201)+`"}`, nil), 400, "INVALID_BODY")
	wantStatus(t, e.patchJSON(t, base+"ac-2_smt.a", `{"description":"`+strings.Repeat("d", 20001)+`"}`, nil), 400, "INVALID_BODY")
	wantStatus(t, e.patchJSON(t, base+"ac-2_smt.a", `{"dueDate":"10/01/2026"}`, nil), 400, "INVALID_BODY")
	wantStatus(t, e.patchJSON(t, base+"ac-2_smt.a", `{"dueDate":1}`, nil), 400, "INVALID_BODY")
	wantStatus(t, e.patchJSON(t, base+"ac-2_smt.a", `not json`, nil), 400, "INVALID_BODY")
	// Unknown part / requirement → 404.
	for _, bad := range []string{"ac-2_smt", "ac-2_gdn", "ac-2_smt.z", "ac-9_smt.a"} {
		nf := e.patchJSON(t, base+bad, `{"status":"implemented"}`, nil)
		wantStatus(t, nf, 404, "NOT_FOUND")
	}
	wantStatus(t, e.patchJSON(t, "/v1/requirements/missing/parts/ac-2_smt.a", `{"status":"implemented"}`, nil), 404, "NOT_FOUND")
	if d := e.get(t, "/v1/requirements/"+ac2.ID); d.body["status"] != "planned" {
		t.Fatalf("rejected patches must not change anything: %s", d.raw)
	}

	// Set part a: implemented, owner, due, markdown description.
	r := e.patchJSON(t, base+"ac-2_smt.a", `{"status":"implemented","scopeCategoryIds":["`+scope.ID+`"],"owner":" alice ","dueDate":"2026-10-01","description":"## Done\n\n- IAM standard v3"}`, map[string]string{"X-Actor": "alice"})
	wantStatus(t, r, 200, "")
	part := r.body["part"].(map[string]any)
	if part["partId"] != "ac-2_smt.a" || part["label"] != "a." || part["status"] != "implemented" || part["owner"] != "alice" || part["description"] != "## Done\n\n- IAM standard v3" || part["updatedAt"] == nil {
		t.Fatalf("part = %v", part)
	}
	if !strings.HasPrefix(part["dueDate"].(string), "2026-10-01") || part["prose"] != "Define and document the types of accounts." {
		t.Fatalf("part = %v", part)
	}
	req := r.body["requirement"].(map[string]any)
	// a implemented + b planned → partial; due comes from open parts only.
	if req["id"] != ac2.ID || req["status"] != "partial" || req["derivedStatus"] != "partial" || req["owner"] != "alice" {
		t.Fatalf("requirement = %v", req)
	}
	if _, has := req["dueDate"]; has {
		t.Fatalf("implemented part must not contribute a due date: %v", req)
	}
	if _, has := req["parts"]; has {
		t.Fatalf("requirement in part response must omit parts: %v", req)
	}
	// Set part b with a due date: still partial, requirement due = b's.
	r = e.patchJSON(t, base+"ac-2_smt.b", `{"status":"partial","dueDate":"2026-11-15"}`, nil)
	wantStatus(t, r, 200, "")
	req = r.body["requirement"].(map[string]any)
	if req["status"] != "partial" || !strings.HasPrefix(req["dueDate"].(string), "2026-11-15") {
		t.Fatalf("requirement after b = %v", req)
	}
	// Detail merges the rows into trackableParts (in part order) and keeps the derived values.
	d := e.get(t, "/v1/requirements/"+ac2.ID)
	tps := d.body["trackableParts"].([]any)
	if len(tps) != 2 || tps[0].(map[string]any)["status"] != "implemented" || tps[1].(map[string]any)["status"] != "partial" || tps[1].(map[string]any)["owner"] != "" {
		t.Fatalf("trackableParts = %v", tps)
	}
	if d.body["status"] != "partial" || d.body["owner"] != "alice" {
		t.Fatalf("detail = %s", d.raw)
	}
	// Clear b's due date and finish it → requirement implemented, no due date.
	r = e.patchJSON(t, base+"ac-2_smt.b", `{"status":"implemented","scopeCategoryIds":["`+scope.ID+`"],"dueDate":null}`, nil)
	wantStatus(t, r, 200, "")
	part = r.body["part"].(map[string]any)
	if _, has := part["dueDate"]; has || part["status"] != "implemented" {
		t.Fatalf("part b = %v", part)
	}
	req = r.body["requirement"].(map[string]any)
	if req["status"] != "implemented" || req["derivedStatus"] != "implemented" {
		t.Fatalf("requirement after both = %v", req)
	}
	if _, has := req["dueDate"]; has {
		t.Fatalf("requirement due must be cleared: %v", req)
	}
	// Override wins over the derivation and survives part changes.
	wantStatus(t, e.patchJSON(t, "/v1/requirements/"+ac2.ID, `{"statusOverride":"alternative"}`, nil), 200, "")
	r = e.patchJSON(t, base+"ac-2_smt.a", `{"status":"planned"}`, nil)
	wantStatus(t, r, 200, "")
	req = r.body["requirement"].(map[string]any)
	if req["status"] != "alternative" || req["derivedStatus"] != "partial" || req["statusOverride"] != "alternative" {
		t.Fatalf("requirement with override = %v", req)
	}
	if list := e.get(t, "/v1/requirements?status=alternative"); list.body["total"] != float64(1) {
		t.Fatalf("effective status filter = %s", list.raw)
	}
	// Synthetic part on a control without parts.
	r = e.patchJSON(t, "/v1/requirements/"+reqs["ac-3"].ID+"/parts/ac-3_smt", `{"status":"not_applicable"}`, nil)
	wantStatus(t, r, 200, "")
	if r.body["requirement"].(map[string]any)["status"] != "not_applicable" {
		t.Fatalf("ac-3 = %s", r.raw)
	}

	// Audit: part.update rows on the requirement, with actor and changed fields.
	audit, _ := e.store.ListAudit(context.Background(), 50)
	var partUpdates []store.AuditEntry
	for _, a := range audit {
		if strings.HasPrefix(a.Action, "implementation.") || a.EntityType == "activity" {
			t.Fatalf("legacy audit row = %+v", a)
		}
		if a.Action == "part.update" {
			partUpdates = append(partUpdates, a)
		}
	}
	if len(partUpdates) != 5 {
		t.Fatalf("part.update rows = %d", len(partUpdates))
	}
	first := partUpdates[len(partUpdates)-1]
	if first.Actor != "test-operator" || first.EntityType != "requirement" || first.EntityID != ac2.ID || first.Detail["partId"] != "ac-2_smt.a" {
		t.Fatalf("first part.update = %+v", first)
	}
	if st := first.Detail["status"].(map[string]any); st["from"] != "planned" || st["to"] != "implemented" {
		t.Fatalf("first part.update detail = %+v", first.Detail)
	}
	if first.Detail["description"] != true || first.Detail["owner"] == nil || first.Detail["dueDate"] == nil {
		t.Fatalf("first part.update detail = %+v", first.Detail)
	}
	// Dashboard recentActivity surfaces them.
	dash := e.get(t, "/v1/dashboard")
	wantStatus(t, dash, 200, "")
	if recent := dash.body["recentActivity"].([]any); recent[0].(map[string]any)["action"] != "part.update" {
		t.Fatalf("recentActivity[0] = %v", recent[0])
	}
}

func TestEvidence_LinkJSONAndMultipart(t *testing.T) {
	e, reqs := partsEnv(t)
	ac2 := reqs["ac-2"]
	body := map[string]any{"kind": "link", "url": "https://example.atlassian.net/browse/SEC-1", "title": "Jira ticket", "description": "Tracking", "validUntil": "2026-12-31", "requirementIds": []string{ac2.ID}}

	r := e.postJSON(t, "/v1/evidence", body, map[string]string{"X-Actor": "linker"})
	wantStatus(t, r, 201, "")
	if r.body["kind"] != "link" || r.body["url"] != "https://example.atlassian.net/browse/SEC-1" || r.body["uploadedBy"] != "test-operator" {
		t.Fatalf("link evidence = %s", r.raw)
	}
	if _, has := r.body["reviewState"]; has {
		t.Fatalf("reviewState must be gone: %s", r.raw)
	}
	if r.body["fileName"] != "" || r.body["sha256"] != "" || r.body["sizeBytes"] != float64(0) || r.body["contentType"] != "" {
		t.Fatalf("link evidence must not carry file metadata: %s", r.raw)
	}
	id := r.body["id"].(string)
	if e.blobs.puts.Load() != 0 {
		t.Fatalf("link evidence touched blob storage")
	}

	dl := e.get(t, "/v1/evidence/"+id+"/download")
	wantStatus(t, dl, 404, "NOT_FOUND")
	if dl.body["message"] != "link evidence has no file" || dl.header.Get("Location") != "" {
		t.Fatalf("download = %s", dl.raw)
	}

	// Validation.
	bad := func(mut func(m map[string]any)) {
		t.Helper()
		m := map[string]any{}
		for k, v := range body {
			m[k] = v
		}
		mut(m)
		wantStatus(t, e.postJSON(t, "/v1/evidence", m, nil), 400, "INVALID_BODY")
	}
	bad(func(m map[string]any) { m["kind"] = "file" })
	bad(func(m map[string]any) { delete(m, "kind") })
	bad(func(m map[string]any) { m["url"] = "ftp://example.com/x" })
	bad(func(m map[string]any) { m["url"] = "https://user:pw@example.com/x" })
	bad(func(m map[string]any) { m["url"] = "https:///nohost" })
	bad(func(m map[string]any) { m["url"] = "not a url" })
	bad(func(m map[string]any) { m["url"] = "https://example.com/" + strings.Repeat("a", 2048) })
	bad(func(m map[string]any) { delete(m, "url") })
	bad(func(m map[string]any) { m["title"] = "" })
	bad(func(m map[string]any) { m["requirementIds"] = []string{} })
	bad(func(m map[string]any) { m["activityIds"] = []string{"x"} })
	bad(func(m map[string]any) { m["extra"] = 1 })
	bad(func(m map[string]any) { m["validUntil"] = "tomorrow" })
	nf := map[string]any{}
	for k, v := range body {
		nf[k] = v
	}
	nf["requirementIds"] = []string{"missing"}
	wantStatus(t, e.postJSON(t, "/v1/evidence", nf, nil), 404, "NOT_FOUND")

	// Multipart with url and no file is link evidence too.
	mp := upload(t, e, map[string][]string{"title": {"Wiki page"}, "url": {"http://wiki.internal/page"}, "requirementIds": {ac2.ID}}, nil, nil)
	wantStatus(t, mp, 201, "")
	if mp.body["kind"] != "link" || mp.body["url"] != "http://wiki.internal/page" {
		t.Fatalf("multipart link = %s", mp.raw)
	}
	wantStatus(t, upload(t, e, map[string][]string{"title": {"Bad"}, "url": {"mailto:x@y"}, "requirementIds": {ac2.ID}}, nil, nil), 400, "INVALID_BODY")

	// A file upload alongside for filtering.
	f := upload(t, e, map[string][]string{"title": {"PDF"}, "requirementIds": {ac2.ID}}, &filePart{name: "a.pdf", contentType: "application/pdf", data: []byte("pdf")}, nil)
	wantStatus(t, f, 201, "")

	all := e.get(t, "/v1/evidence")
	if all.body["total"] != float64(3) {
		t.Fatalf("total = %v", all.body["total"])
	}
	links := e.get(t, "/v1/evidence?kind=link")
	wantStatus(t, links, 200, "")
	if links.body["total"] != float64(2) {
		t.Fatalf("link total = %s", links.raw)
	}
	files := e.get(t, "/v1/evidence?kind=file")
	if files.body["total"] != float64(1) || items(t, files)[0]["kind"] != "file" {
		t.Fatalf("file total = %s", files.raw)
	}
	wantStatus(t, e.get(t, "/v1/evidence?kind=blob"), 400, "INVALID_QUERY")
	// Query matches the URL.
	q := e.get(t, "/v1/evidence?q=atlassian")
	if q.body["total"] != float64(1) {
		t.Fatalf("q total = %s", q.raw)
	}
	// The review route is gone (404 like any unknown /v1 path).
	wantStatus(t, e.postJSON(t, "/v1/evidence/"+id+"/review", map[string]any{"state": "approved"}, nil), 404, "NOT_FOUND")
	// Detail lists both kinds; validUntil is plain metadata.
	detail := e.get(t, "/v1/requirements/"+ac2.ID)
	if len(detail.body["evidence"].([]any)) != 3 {
		t.Fatalf("requirement evidence = %s", detail.raw)
	}
}

func TestEvidence_ActivityInputsAreGone(t *testing.T) {
	e, reqs := partsEnv(t)
	ac2 := reqs["ac-2"]
	// Multipart activityIds is an unknown form field; the activityId filter is an unknown query parameter.
	small := &filePart{name: "s.txt", contentType: "text/plain", data: []byte("ok")}
	wantStatus(t, upload(t, e, map[string][]string{"title": {"T"}, "requirementIds": {ac2.ID}, "activityIds": {"x"}}, small, nil), 400, "INVALID_BODY")
	if e.blobs.puts.Load() != 0 {
		t.Fatalf("invalid upload reached blob store")
	}
	wantStatus(t, e.get(t, "/v1/evidence?activityId=x"), 400, "INVALID_QUERY")
	// Activity routes are gone.
	wantStatus(t, e.get(t, "/v1/requirements/"+ac2.ID+"/activities"), 404, "NOT_FOUND")
	wantStatus(t, e.postJSON(t, "/v1/requirements/"+ac2.ID+"/activities", map[string]any{"body": "x", "partId": "ac-2_smt.a"}, nil), 404, "NOT_FOUND")
	wantStatus(t, e.patchJSON(t, "/v1/activities/abc", `{"body":"x"}`, nil), 404, "NOT_FOUND")
	wantStatus(t, e.do(t, http.MethodDelete, "/v1/activities/abc", nil, nil), 404, "NOT_FOUND")
	// Evidence carries requirementIds only.
	r := upload(t, e, map[string][]string{"title": {"T"}, "requirementIds": {ac2.ID}}, small, nil)
	wantStatus(t, r, 201, "")
	if _, has := r.body["activityIds"]; has {
		t.Fatalf("activityIds must be gone: %s", r.raw)
	}
}

func TestRouter_NewRoutesAllowHeaders(t *testing.T) {
	e := newEnv(t, nil)
	cases := []struct{ method, path, allow string }{
		{http.MethodDelete, "/v1/frameworks/abc", "GET, HEAD, PATCH"},
		{http.MethodGet, "/v1/requirements/abc/parts/ac-1_smt.a", "PATCH"},
		{http.MethodDelete, "/v1/requirements/abc/parts/ac-1_smt.a", "PATCH"},
	}
	for _, c := range cases {
		r := e.do(t, c.method, c.path, nil, nil)
		wantStatus(t, r, 405, "METHOD_NOT_ALLOWED")
		if got := r.header.Get("Allow"); got != c.allow {
			t.Fatalf("%s %s: Allow = %q, want %q", c.method, c.path, got, c.allow)
		}
	}
}
