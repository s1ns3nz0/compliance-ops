package oscal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// ---- SSDF task folding ------------------------------------------------------

// TestParse_SSDFFoldsTasksIntoPractices parses a trimmed copy of the NIST
// SP 800-218 catalog (2 groups, 3 practices, 6 tasks, copied verbatim from the
// NIST file). It downloads nothing.
func TestParse_SSDFFoldsTasksIntoPractices(t *testing.T) {
	data, err := os.ReadFile("testdata/ssdf_ver1_trimmed.json")
	if err != nil {
		t.Fatal(err)
	}
	docs, err := ParseJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("docs = %d", len(docs))
	}
	doc := docs[0]
	if got := ShortName(doc.Title, doc.Props()); got != "NIST SP 800-218 SSDF" {
		t.Fatalf("short name = %q", got)
	}
	var ids []string
	for _, c := range doc.Controls {
		ids = append(ids, c.ID)
	}
	if !reflect.DeepEqual(ids, []string{"PO.1", "PO.2", "PW.1"}) {
		t.Fatalf("controls = %v (tasks must not be separate controls)", ids)
	}
	for _, c := range doc.Controls {
		tp := TrackableParts(c.Parts, c.ID)
		if len(tp) != 2 {
			t.Fatalf("%s trackable parts = %+v", c.ID, tp)
		}
		for i, p := range tp {
			want := c.ID + "." + string(rune('1'+i))
			if p.ID != want || p.Name != "item" || p.Label != want || p.Prose == "" {
				t.Fatalf("%s trackable[%d] = %+v", c.ID, i, p)
			}
			if _, ok := FindPart(c.Parts, want+"_ex"); !ok {
				t.Fatalf("%s: examples part for %s missing", c.ID, want)
			}
		}
	}

	po1 := doc.Controls[0]
	if po1.Title != "Define Security Requirements for Software Development" {
		t.Fatalf("title = %q", po1.Title)
	}
	stmt := po1.Parts[0]
	if stmt.ID != "statement_PO.1" || stmt.Name != "statement" || !strings.HasPrefix(stmt.Prose, "Ensure that security requirements for software development are known") {
		t.Fatalf("practice statement = %+v", stmt)
	}
	const task1 = "Identify and document all security requirements for the organization’s software development infrastructures and processes, and maintain the requirements over time."
	const task2 = "Identify and document all security requirements for organization-developed software to meet, and maintain the requirements over time."
	if len(stmt.Parts) != 2 || stmt.Parts[0].Prose != task1 || stmt.Parts[1].Prose != task2 {
		t.Fatalf("statement items = %+v", stmt.Parts)
	}
	if stmt.Parts[0].ID != "PO.1.1" || stmt.Parts[0].Label != "PO.1.1" || stmt.Parts[0].Name != "item" || stmt.Parts[0].Parts != nil {
		t.Fatalf("item = %+v", stmt.Parts[0])
	}
	// Examples become guidance-like parts: one "examples" block per task,
	// holding the verbatim example parts with the title as label.
	ex, ok := FindPart(po1.Parts, "PO.1.1_ex")
	if !ok || ex.Name != "examples" || ex.Label != "PO.1.1" || len(ex.Parts) != 4 {
		t.Fatalf("examples = %+v", ex)
	}
	if e := ex.Parts[0]; e.ID != "PO.1.1.1" || e.Name != "example" || e.Label != "Example 1:" || !strings.HasPrefix(e.Prose, "Define policies for securing software development infrastructures") {
		t.Fatalf("example = %+v", e)
	}
	if ex2, _ := FindPart(po1.Parts, "PO.1.2_ex"); len(ex2.Parts) != 7 {
		t.Fatalf("PO.1.2 examples = %d", len(ex2.Parts))
	}
	// Part order: statement first, then the examples blocks in task order.
	var names []string
	for _, p := range po1.Parts {
		names = append(names, p.Name+":"+p.ID)
	}
	if !reflect.DeepEqual(names, []string{"statement:statement_PO.1", "examples:PO.1.1_ex", "examples:PO.1.2_ex"}) {
		t.Fatalf("part order = %v", names)
	}
	// External references of both tasks (14 + 15 links) are NOT related
	// controls (no rel=related links exist in SSDF)...
	if len(po1.Related) != 0 {
		t.Fatalf("related must hold control ids only, got %d %v", len(po1.Related), po1.Related)
	}
	// ...they become resolved references, deduplicated by (uuid, text,
	// taskId) and tagged with the task that carried them.
	if len(po1.References) == 0 || len(po1.References) >= 30 {
		t.Fatalf("references = %d %+v", len(po1.References), po1.References)
	}
	seen := map[string]bool{}
	byTask := map[string]int{}
	var bsafss, sp80053 *Reference
	for i := range po1.References {
		r := po1.References[i]
		key := r.UUID + "|" + r.Text + "|" + r.TaskID
		if strings.HasPrefix(r.UUID, "#") || seen[key] || r.UUID == "" || r.TaskID == "" {
			t.Fatalf("bad/duplicate reference %+v in %+v", r, po1.References)
		}
		seen[key] = true
		byTask[r.TaskID]++
		if r.UUID == "00b12b29-654e-4fbb-8db0-6a156deb7eb4" && bsafss == nil {
			bsafss = &po1.References[i]
		}
		if r.Title == "SP80053" || r.Title == "SP800-53" {
			sp80053 = &po1.References[i]
		}
	}
	if byTask["PO.1.1"] == 0 || byTask["PO.1.2"] == 0 || len(byTask) != 2 {
		t.Fatalf("references per task = %v", byTask)
	}
	if bsafss == nil || bsafss.Title != "BSAFSS" || !strings.HasPrefix(bsafss.Citation, "BSA (2020)") || !strings.HasPrefix(bsafss.URL, "https://www.bsa.org/") || bsafss.Text != "SM.3, DE.1, IA.1, IA.2" || bsafss.TaskID != "PO.1.1" {
		t.Fatalf("BSAFSS reference not resolved from back-matter: %+v", bsafss)
	}
	if sp80053 == nil || !strings.Contains(sp80053.Text, "SA-") || sp80053.URL == "" {
		t.Fatalf("SP800-53 reference = %+v", sp80053)
	}
	// Serialized form: uuid/title always present, taskId set.
	if b, _ := json.Marshal(po1.References[0]); !strings.Contains(string(b), `"uuid":"`) || !strings.Contains(string(b), `"taskId":"PO.1.1"`) {
		t.Fatalf("reference json = %s", b)
	}
	// Search text covers the task prose (and examples), never placeholders.
	if !strings.Contains(po1.Text, task1) || !strings.Contains(po1.Text, task2) || !strings.Contains(po1.Text, "PO.1.2") || strings.Contains(po1.Text, "{{") {
		t.Fatalf("text = %q", po1.Text)
	}
	if b, _ := json.Marshal(po1.Parts); strings.Contains(string(b), "{{") {
		t.Fatalf("parts contain placeholders: %s", b)
	}
}

// TestParse_TaskFoldingEdgeCases covers a parent without a statement, the
// role=task prop, a case-insensitive class, parameter resolution inside task
// prose, a mixed child list, and deduplication against the parent's own links.
func TestParse_TaskFoldingEdgeCases(t *testing.T) {
	const cat = `{"catalog":{"uuid":"u","metadata":{"title":"Tasks"},"groups":[{"id":"g","title":"G","parts":[{"name":"overview","prose":"ignored"}],"controls":[
	  {"id":"P.1","class":"Practice","title":"Practice one","links":[{"href":"#P.2","rel":"related"},{"href":"#ref-a","rel":"related"}],
	   "params":[{"id":"p1_odp","values":["daily"]}],
	   "parts":[{"id":"P.1_gdn","name":"guidance","prose":"Guide."}],
	   "controls":[
	     {"id":"P.1.1","class":"TASK","title":"P.1.1","links":[{"href":"#ref-a","rel":"external_reference"},{"href":"#ref-b","rel":"external_reference"},{"href":"#ref-a","rel":"external_reference"},{"href":"https://ext","rel":"external_reference","text":"ext"},{"href":"https://x","rel":"reference"}],
	      "parts":[{"id":"statement_P.1.1","name":"statement","prose":"Do it {{ insert: param, p1_odp }}."},{"id":"P.1.1.1","name":"example","title":"Example 1:","prose":"Ex."}]},
	     {"id":"P.1.2","title":"P.1.2","props":[{"name":"role","value":"task"},{"name":"label","value":"P.1.2"}],
	      "parts":[{"id":"statement_P.1.2","name":"statement","prose":"Second."}]},
	     {"id":"P.1.3","class":"SP800-53-enhancement","title":"Not a task","parts":[{"id":"P.1.3_smt","name":"statement","prose":"Own control."}]}
	  ]}
	]}]}}`
	docs, err := ParseJSON([]byte(cat))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range docs[0].Controls {
		ids = append(ids, c.ID)
	}
	if !reflect.DeepEqual(ids, []string{"P.1", "P.1.3"}) {
		t.Fatalf("controls = %v", ids)
	}
	p1 := docs[0].Controls[0]
	stmt, ok := FindPart(p1.Parts, "P.1_smt")
	if !ok || stmt.Name != "statement" || stmt.Prose != "" || len(stmt.Parts) != 2 {
		t.Fatalf("synthetic statement = %+v", stmt)
	}
	if stmt.Parts[0].Prose != "Do it daily." || stmt.Parts[1].Prose != "Second." || stmt.Parts[1].Label != "P.1.2" {
		t.Fatalf("items = %+v", stmt.Parts)
	}
	if len(p1.Parts) != 3 || p1.Parts[0].Name != "guidance" || p1.Parts[1].Name != "statement" || p1.Parts[2].ID != "P.1.1_ex" {
		t.Fatalf("part order = %+v", p1.Parts)
	}
	if !reflect.DeepEqual(p1.Related, []string{"P.2", "ref-a"}) {
		t.Fatalf("related = %v (rel=related only)", p1.Related)
	}
	// external_reference links become references: '#uuid' hrefs keep the
	// uuid (unresolved: no back-matter, so title stays empty), plain URLs
	// keep the URL; rel=reference with a non-'#' href is ignored.
	wantRefs := []Reference{
		{UUID: "ref-a", TaskID: "P.1.1"},
		{UUID: "ref-b", TaskID: "P.1.1"},
		{URL: "https://ext", Text: "ext", TaskID: "P.1.1"},
	}
	if !reflect.DeepEqual(p1.References, wantRefs) {
		t.Fatalf("references = %+v", p1.References)
	}
	tp := TrackableParts(p1.Parts, p1.ID)
	if len(tp) != 2 || tp[0].ID != "P.1.1" || tp[1].ID != "P.1.2" {
		t.Fatalf("trackable = %+v", tp)
	}
	if !strings.Contains(p1.Text, "Do it daily.") || !strings.Contains(p1.Text, "Second.") || strings.Contains(p1.Text, "Own control.") {
		t.Fatalf("text = %q", p1.Text)
	}
}

// TestParse_EnhancementsStaySeparateControls is the NIST 800-53 regression:
// nested controls with class SP800-53-enhancement remain their own controls
// (at any depth) and the parent keeps its own parts untouched.
func TestParse_EnhancementsStaySeparateControls(t *testing.T) {
	const cat = `{"catalog":{"uuid":"u","metadata":{"title":"NIST SP 800-53"},"groups":[{"id":"ac","class":"family","title":"Access Control","controls":[
	  {"id":"ac-2","class":"SP800-53","title":"Account Management",
	   "parts":[{"id":"ac-2_smt","name":"statement","parts":[{"id":"ac-2_smt.a","name":"item","props":[{"name":"label","value":"a."}],"prose":"Define."}]},{"id":"ac-2_gdn","name":"guidance","prose":"G."}],
	   "controls":[
	     {"id":"ac-2.1","class":"SP800-53-enhancement","title":"Automated System Account Management","parts":[{"id":"ac-2.1_smt","name":"statement","prose":"Automate."}]},
	     {"id":"ac-2.2","class":"SP800-53-enhancement","title":"Automated Temporary Accounts","parts":[{"id":"ac-2.2_smt","name":"statement","prose":"Remove."}],
	      "controls":[{"id":"ac-2.2.1","class":"SP800-53-enhancement","title":"Deep","parts":[{"id":"ac-2.2.1_smt","name":"statement","prose":"Deep."}]}]}
	  ]}
	]}]}}`
	docs, err := ParseJSON([]byte(cat))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range docs[0].Controls {
		ids = append(ids, c.ID)
	}
	if !reflect.DeepEqual(ids, []string{"ac-2", "ac-2.1", "ac-2.2", "ac-2.2.1"}) {
		t.Fatalf("controls = %v", ids)
	}
	ac2 := docs[0].Controls[0]
	if len(ac2.Parts) != 2 || len(ac2.Parts[0].Parts) != 1 || ac2.Related != nil || ac2.References != nil {
		t.Fatalf("parent changed: %+v", ac2.Parts)
	}
	if tp := TrackableParts(ac2.Parts, ac2.ID); len(tp) != 1 || tp[0].ID != "ac-2_smt.a" {
		t.Fatalf("trackable = %+v", tp)
	}
	if !strings.Contains(ac2.Text, "ac-2 Account Management") || strings.Contains(ac2.Text, "Automate.") {
		t.Fatalf("text leaked enhancement prose: %q", ac2.Text)
	}
}

// ---- multiple HTTP sources --------------------------------------------------

const catA = `{"catalog":{"uuid":"aaaaaaaa-0000-0000-0000-000000000001","metadata":{"title":"Catalog A"},"controls":[{"id":"a-1","title":"A"}]}}`
const catB = `[{"catalog":{"uuid":"bbbbbbbb-0000-0000-0000-000000000002","metadata":{"title":"Catalog B"},"controls":[{"id":"b-1","title":"B"}]}}]`

func TestHTTPRepository_ListsEverySourceInOrder(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/a.json":
			_, _ = w.Write([]byte(catA))
		case "/b.json":
			_, _ = w.Write([]byte(catB))
		case "/down.json":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	repo := NewHTTPRepository(srv.URL+"/a.json", srv.URL+"/b.json")
	if !reflect.DeepEqual(repo.SourceURLs, []string{srv.URL + "/a.json", srv.URL + "/b.json"}) {
		t.Fatalf("SourceURLs = %v", repo.SourceURLs)
	}
	docs, err := repo.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || docs[0].Title != "Catalog A" || docs[1].Title != "Catalog B" || hits.Load() != 2 {
		t.Fatalf("docs = %+v hits = %d", docs, hits.Load())
	}

	// Any failing source fails the whole listing (fail closed).
	for _, bad := range []string{"/down.json", "/missing.json"} {
		repo = NewHTTPRepository(srv.URL+"/a.json", srv.URL+bad)
		if docs, err := repo.List(context.Background()); err == nil || docs != nil {
			t.Fatalf("%s: expected failure, got %v / %v", bad, docs, err)
		}
	}
	// The size limit applies per source.
	repo = NewHTTPRepository(srv.URL + "/a.json")
	repo.MaxBytes = 10
	if _, err := repo.List(context.Background()); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("size limit error = %v", err)
	}
	// No sources configured is an error, not an empty list.
	if _, err := NewHTTPRepository().List(context.Background()); err == nil {
		t.Fatal("empty repository must fail")
	}
}
