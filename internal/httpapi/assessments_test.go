package httpapi_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
)

func TestAssessmentsAutoAndManualMappingRoutes(t *testing.T) {
	e := newEnv(t, nil)
	fw, reqs := importAll(t, e)
	raw := []byte(`{"assessment-results":{"uuid":"ar-1","metadata":{"title":"Assessment","version":"1"},"import-profile":{"href":"#` + catalogID + `"},"results":[{"uuid":"res-1","findings":[{"uuid":"f-1","title":"Finding","target":{"target-id":"AC-1","type":"control-id","status":{"state":"satisfied"}}}]}]}}`)
	docs, err := oscal.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	u, err := e.store.CreateOscalUpload(t.Context(), docs[0], raw, "assessment-sha", int64(len(raw)), "alice")
	if err != nil {
		t.Fatal(err)
	}
	list := e.get(t, "/v1/assessments?type=assessment-results&frameworkId="+fw.ID)
	wantStatus(t, list, 200, "")
	if list.body["total"] != float64(1) {
		t.Fatalf("list=%s", list.raw)
	}
	detail := e.get(t, "/v1/assessments/"+u.ID)
	wantStatus(t, detail, 200, "")
	if detail.body["linkSource"] != "automatic" || detail.body["effectiveFrameworkId"] != fw.ID {
		t.Fatalf("detail=%s", detail.raw)
	}
	findings := detail.body["findings"].([]any)
	finding := findings[0].(map[string]any)
	mapping := finding["mapping"].(map[string]any)
	if mapping["source"] != "automatic" || mapping["requirementId"] != reqs[0].ID {
		t.Fatalf("mapping=%#v", mapping)
	}
	before, _ := e.store.GetRequirement(t.Context(), reqs[0].ID)
	key := finding["itemKey"].(string)
	mapped := e.postJSON(t, "/v1/assessments/"+u.ID+"/mappings", map[string]any{"itemKey": key, "requirementId": reqs[0].ID, "partId": "ac-1_smt"}, nil)
	wantStatus(t, mapped, 201, "")
	detail = e.get(t, "/v1/assessments/"+u.ID)
	mapping = detail.body["findings"].([]any)[0].(map[string]any)["mapping"].(map[string]any)
	if mapping["source"] != "manual" {
		t.Fatalf("manual mapping=%#v", mapping)
	}
	reqItems := e.get(t, "/v1/requirements/"+reqs[0].ID+"/assessments")
	wantStatus(t, reqItems, 200, "")
	if reqItems.body["total"] != float64(1) {
		t.Fatalf("requirement assessments=%s", reqItems.raw)
	}
	after, _ := e.store.GetRequirement(t.Context(), reqs[0].ID)
	if after.Status != before.Status {
		t.Fatalf("status changed %s -> %s", before.Status, after.Status)
	}
	deleted := e.do(t, http.MethodDelete, "/v1/assessments/"+u.ID+"/mappings/"+url.PathEscape(key), nil, nil)
	wantStatus(t, deleted, 204, "")
	clear := e.patchJSON(t, "/v1/assessments/"+u.ID+"/framework", `{"frameworkId":null}`, nil)
	wantStatus(t, clear, 200, "")
	manual := e.patchJSON(t, "/v1/assessments/"+u.ID+"/framework", `{"frameworkId":"`+fw.ID+`"}`, nil)
	wantStatus(t, manual, 200, "")
}

func TestAssessmentAmbiguousReferenceRemainsUnmapped(t *testing.T) {
	e := newEnv(t, nil)
	doc := oscal.Document{ID: "ambiguous-catalog", Type: "catalog", Title: "Ambiguous", Controls: []oscal.Control{
		{ID: "c-1", Title: "One", Parts: []oscal.Part{{ID: "obj-1", Name: "statement"}}},
		{ID: "c-2", Title: "Two", Parts: []oscal.Part{{ID: "obj-1", Name: "statement"}}},
	}}
	fw, err := e.store.ImportFramework(t.Context(), doc, "a")
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"assessment-results":{"uuid":"ar-x","metadata":{"title":"X"},"import-profile":{"href":"#ambiguous-catalog"},"results":[{"findings":[{"uuid":"f","target":{"target-id":"obj-1","type":"objective-id"}}]}]}}`)
	docs, _ := oscal.ParseJSON(raw)
	u, _ := e.store.CreateOscalUpload(t.Context(), docs[0], raw, "sha-x", int64(len(raw)), "a")
	r := e.get(t, "/v1/assessments/"+u.ID)
	wantStatus(t, r, 200, "")
	if r.body["effectiveFrameworkId"] != fw.Framework.ID {
		t.Fatalf("framework=%s", r.raw)
	}
	m := r.body["findings"].([]any)[0].(map[string]any)["mapping"].(map[string]any)
	if m["source"] != "unmapped" {
		t.Fatalf("mapping=%#v", m)
	}
}

func TestAssessmentExactPartWinsOverControlForSameRequirement(t *testing.T) {
	e := newEnv(t, nil)
	doc := oscal.Document{ID: "specific-catalog", Type: "catalog", Title: "Specific", Controls: []oscal.Control{
		{ID: "ac-2", Title: "Accounts", Parts: []oscal.Part{{ID: "ac-2_smt", Name: "statement", Parts: []oscal.Part{{ID: "ac-2_smt.a", Name: "item"}}}}},
	}}
	fw, err := e.store.ImportFramework(t.Context(), doc, "a")
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"assessment-plan":{"uuid":"ap-specific","metadata":{"title":"Specific"},"import-ssp":{"href":"#specific-catalog"},"reviewed-controls":{"control-selections":[{"include-controls":[{"control-id":"ac-2","statement-ids":["ac-2_smt.a"]}]}]}}}`)
	docs, err := oscal.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	u, err := e.store.CreateOscalUpload(t.Context(), docs[0], raw, "sha-specific", int64(len(raw)), "a")
	if err != nil {
		t.Fatal(err)
	}
	r := e.get(t, "/v1/assessments/"+u.ID)
	wantStatus(t, r, 200, "")
	if r.body["effectiveFrameworkId"] != fw.Framework.ID {
		t.Fatalf("framework=%s", r.raw)
	}
	m := r.body["reviewedControls"].([]any)[0].(map[string]any)["mapping"].(map[string]any)
	if m["source"] != "automatic" || m["partId"] != "ac-2_smt.a" {
		t.Fatalf("mapping=%#v", m)
	}
}

func TestAssessmentMappingRejectsUnknownItemKey(t *testing.T) {
	e := newEnv(t, nil)
	_, reqs := importAll(t, e)
	raw := []byte(`{"assessment-results":{"uuid":"ar-manual","metadata":{"title":"Manual"},"results":[{"findings":[{"uuid":"real-finding","title":"Real"}]}]}}`)
	docs, err := oscal.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	u, err := e.store.CreateOscalUpload(t.Context(), docs[0], raw, "sha-manual", int64(len(raw)), "a")
	if err != nil {
		t.Fatal(err)
	}
	r := e.postJSON(t, "/v1/assessments/"+u.ID+"/mappings", map[string]any{
		"itemKey": "assessment-results/finding/fabricated", "requirementId": reqs[0].ID,
	}, nil)
	wantStatus(t, r, 400, "INVALID_BODY")
	mappings, err := e.store.ListAssessmentMappings(t.Context(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(mappings) != 0 {
		t.Fatalf("fabricated mapping was stored: %+v", mappings)
	}
}
