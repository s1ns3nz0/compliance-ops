package httpapi_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
)

func minimalOscal(root, id string) []byte {
	controls := ""
	if root == "catalog" {
		controls = `,"controls":[{"id":"ac-1","title":"Access","parts":[{"name":"statement","prose":"Do it"}]}]`
	}
	return []byte(fmt.Sprintf(`{"%s":{"uuid":"%s","metadata":{"title":"%s title","version":"1.0"}%s}}`, root, id, root, controls))
}

func uploadOscal(t *testing.T, e *env, data []byte) resp {
	t.Helper()
	body, ct := multipartBody(t, nil, &filePart{name: "anything.bin", contentType: "application/octet-stream", data: data})
	return e.do(t, http.MethodPost, "/v1/oscal/uploads", body, map[string]string{"Content-Type": ct, "X-Actor": "alice"})
}

func TestOscalUploadSupportedRootsAndLifecycle(t *testing.T) {
	e := newEnv(t, nil)
	roots := []string{"catalog", "profile", "component-definition", "system-security-plan", "assessment-plan", "assessment-results", "plan-of-action-and-milestones"}
	var first map[string]any
	for i, root := range roots {
		r := uploadOscal(t, e, minimalOscal(root, fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)))
		wantStatus(t, r, 201, "")
		if r.body["type"] != root || r.body["document"] != nil {
			t.Fatalf("%s upload = %s", root, r.raw)
		}
		if i == 0 {
			first = r.body
		}
	}
	list := e.get(t, "/v1/oscal/uploads")
	wantStatus(t, list, 200, "")
	if len(items(t, list)) != 7 {
		t.Fatalf("list = %s", list.raw)
	}
	id := first["id"].(string)
	detail := e.get(t, "/v1/oscal/uploads/"+id)
	wantStatus(t, detail, 200, "")
	if detail.body["document"] == nil {
		t.Fatalf("detail = %s", detail.raw)
	}
	del := e.do(t, http.MethodDelete, "/v1/oscal/uploads/"+id, nil, map[string]string{"X-Actor": "bob"})
	wantStatus(t, del, 204, "")
	wantStatus(t, e.get(t, "/v1/oscal/uploads/"+id), 404, "NOT_FOUND")
}

func TestOscalUploadValidationDuplicateAndImport(t *testing.T) {
	e := newEnv(t, nil)
	wantStatus(t, uploadOscal(t, e, []byte(`not json`)), 400, "INVALID_BODY")
	wantStatus(t, uploadOscal(t, e, []byte(`[{}]`)), 400, "INVALID_BODY")
	data := minimalOscal("catalog", "11111111-1111-1111-1111-111111111111")
	one := uploadOscal(t, e, data)
	wantStatus(t, one, 201, "")
	dup := uploadOscal(t, e, data)
	wantStatus(t, dup, 409, "CONFLICT")
	if dup.body["existing"] == nil {
		t.Fatalf("duplicate = %s", dup.raw)
	}
	imp := e.postJSON(t, "/v1/frameworks/import", map[string]any{"uploadId": one.body["id"]}, nil)
	wantStatus(t, imp, 200, "")
	blocked := e.do(t, http.MethodDelete, "/v1/oscal/uploads/"+one.body["id"].(string), nil, nil)
	wantStatus(t, blocked, 409, "CONFLICT")
	wantStatus(t, e.postJSON(t, "/v1/frameworks/import", map[string]any{"documentId": catalogID, "uploadId": one.body["id"]}, nil), 400, "INVALID_BODY")
	non := uploadOscal(t, e, minimalOscal("assessment-plan", "22222222-2222-2222-2222-222222222222"))
	wantStatus(t, non, 201, "")
	rej := e.postJSON(t, "/v1/frameworks/import", map[string]any{"uploadId": non.body["id"]}, nil)
	wantStatus(t, rej, 400, "INVALID_BODY")
	if rej.body["message"] != "only catalog and profile uploads create requirements" {
		t.Fatalf("reject = %s", rej.raw)
	}
}

func TestFrameworkImportResolvesConfiguredAndUploadedProfiles(t *testing.T) {
	catalogRaw := []byte(`{"catalog":{"uuid":"profile-catalog","metadata":{"title":"Catalog"},"controls":[{"id":"ac-1","title":"One"},{"id":"ac-2","title":"Two"}]}}`)
	profileRaw := []byte(`{"profile":{"uuid":"profile-baseline","metadata":{"title":"Baseline"},"imports":[{"href":"#profile-catalog","include-controls":[{"with-ids":["ac-2"]}]}]}}`)
	catalogDocs, err := oscal.ParseJSON(catalogRaw)
	if err != nil {
		t.Fatal(err)
	}
	profileDocs, err := oscal.ParseJSON(profileRaw)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("configured documents", func(t *testing.T) {
		e := newEnv(t, oscal.StaticRepository{Documents: append(catalogDocs, profileDocs...)})
		r := e.postJSON(t, "/v1/frameworks/import", map[string]any{"documentId": "profile-baseline"}, nil)
		wantStatus(t, r, 200, "")
		if got := items(t, r)[0]["framework"].(map[string]any)["requirementCount"]; got != float64(1) {
			t.Fatalf("profile import = %s", r.raw)
		}
	})

	t.Run("uploaded documents", func(t *testing.T) {
		e := newEnv(t, oscal.StaticRepository{Documents: []oscal.Document{}})
		catalog := uploadOscal(t, e, catalogRaw)
		wantStatus(t, catalog, 201, "")
		profile := uploadOscal(t, e, profileRaw)
		wantStatus(t, profile, 201, "")
		r := e.postJSON(t, "/v1/frameworks/import", map[string]any{"uploadId": profile.body["id"]}, nil)
		wantStatus(t, r, 200, "")
		if got := items(t, r)[0]["framework"].(map[string]any)["requirementCount"]; got != float64(1) {
			t.Fatalf("uploaded profile import = %s", r.raw)
		}
		blocked := e.do(t, http.MethodDelete, "/v1/oscal/uploads/"+profile.body["id"].(string), nil, nil)
		wantStatus(t, blocked, 409, "CONFLICT")
	})
}

func TestFrameworkImportRejectsUnresolvedProfile(t *testing.T) {
	profiles, err := oscal.ParseJSON([]byte(`{"profile":{"uuid":"missing-baseline","metadata":{"title":"Missing"},"imports":[{"href":"#missing-catalog","include-all":{}}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, oscal.StaticRepository{Documents: profiles})
	r := e.postJSON(t, "/v1/frameworks/import", map[string]any{"documentId": "missing-baseline"}, nil)
	wantStatus(t, r, 400, "INVALID_BODY")
	if r.body["message"] != "profile resolution requires an available imported catalog" {
		t.Fatalf("message = %s", r.raw)
	}
	frameworks, err := e.store.ListFrameworks(t.Context())
	if err != nil || len(frameworks) != 0 {
		t.Fatalf("unresolved profile created a framework: %+v, %v", frameworks, err)
	}
}

func TestOscalUploadTooLargeMethodsQueriesAndAuth(t *testing.T) {
	e := newEnv(t, nil)
	wantStatus(t, uploadOscal(t, e, make([]byte, (10<<20)+1)), 413, "PAYLOAD_TOO_LARGE")
	wantStatus(t, e.get(t, "/v1/oscal/uploads?x=1"), 400, "INVALID_QUERY")
	m := e.do(t, http.MethodPatch, "/v1/oscal/uploads/bad", strings.NewReader(`{}`), nil)
	wantStatus(t, m, 405, "METHOD_NOT_ALLOWED")
	if m.header.Get("Allow") != "DELETE, GET, HEAD" {
		t.Fatalf("Allow = %q", m.header.Get("Allow"))
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/oscal/uploads", nil)
	rec := newRecorder(e, req)
	if rec.Code != 401 {
		t.Fatalf("auth status = %d", rec.Code)
	}
}

func TestOscalSourcesIndependentAndRedacted(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(minimalOscal("catalog", "33333333-3333-3333-3333-333333333333"))
	}))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "secret upstream failure", 500) }))
	defer bad.Close()
	repo := oscal.NewHTTPRepository(good.URL+"/catalog?token=secret", bad.URL+"/broken?credential=hidden")
	e := newEnv(t, repo)
	r := e.get(t, "/v1/oscal/sources")
	wantStatus(t, r, 200, "")
	rows := items(t, r)
	if len(rows) != 2 {
		t.Fatalf("rows=%s", r.raw)
	}
	if strings.Contains(rows[0]["url"].(string), "?") || rows[0]["status"] != "available" {
		t.Fatalf("healthy=%v", rows[0])
	}
	if rows[1]["status"] != "unavailable" || rows[1]["errorCode"] != "OSCAL_SOURCE_UNAVAILABLE" || len(rows[1]["documents"].([]any)) != 0 {
		t.Fatalf("broken=%v", rows[1])
	}
	if strings.Contains(string(r.raw), "secret upstream failure") || strings.Contains(string(r.raw), "token=secret") {
		t.Fatalf("leak=%s", r.raw)
	}
	wantStatus(t, e.get(t, "/v1/oscal/sources?x=1"), 400, "INVALID_QUERY")
}
