package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestScopeCategoryAPIAndPartAssignment(t *testing.T) {
	e := newEnv(t, nil)
	_, reqs := importAll(t, e)

	unauth := httptest.NewRequest(http.MethodGet, "/v1/scope-categories", nil)
	if rec := newRecorder(e, unauth); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", rec.Code)
	}
	created := e.postJSON(t, "/v1/scope-categories", map[string]any{"name": " Production 2 "}, map[string]string{"X-Actor": "alice"})
	wantStatus(t, created, http.StatusCreated, "")
	id := created.body["id"].(string)
	company := e.postJSON(t, "/v1/scope-categories", map[string]any{"name": "Entire company"}, nil)
	wantStatus(t, company, http.StatusCreated, "")
	companyID := company.body["id"].(string)
	wantStatus(t, e.postJSON(t, "/v1/scope-categories", map[string]any{"name": "production 2"}, nil), http.StatusConflict, "CONFLICT")

	list := e.get(t, "/v1/scope-categories?q=COMP&limit=100")
	wantStatus(t, list, http.StatusOK, "")
	if got := items(t, list); len(got) != 1 || got[0]["id"] != companyID {
		t.Fatalf("search=%v", got)
	}
	wantStatus(t, e.get(t, "/v1/scope-categories?wat=1"), http.StatusBadRequest, "INVALID_QUERY")

	partURL := "/v1/requirements/" + reqs[0].ID + "/parts/ac-1_smt"
	patched := e.patchJSON(t, partURL, `{"status":"implemented","scopeCategoryIds":["`+id+`","`+companyID+`","`+id+`"]}`, nil)
	wantStatus(t, patched, http.StatusOK, "")
	part := patched.body["part"].(map[string]any)
	if scopes := part["scopes"].([]any); len(scopes) != 2 || scopes[0].(map[string]any)["name"] != "Entire company" {
		t.Fatalf("scopes=%v", scopes)
	}
	wantStatus(t, e.patchJSON(t, partURL, `{"scopeCategoryIds":[]}`, nil), http.StatusBadRequest, "INVALID_BODY")
	wantStatus(t, e.patchJSON(t, partURL, `{"scope":"legacy"}`, nil), http.StatusBadRequest, "INVALID_BODY")
	wantStatus(t, e.patchJSON(t, partURL, `{"scopeCategoryIds":["00000000-0000-0000-0000-000000000000"]}`, nil), http.StatusNotFound, "NOT_FOUND")

	filtered := e.get(t, "/v1/requirements?scopeCategoryId="+id+","+companyID)
	wantStatus(t, filtered, http.StatusOK, "")
	if filtered.body["total"] != float64(1) {
		t.Fatalf("filtered=%s", filtered.raw)
	}
	wantStatus(t, e.get(t, "/v1/requirements?scope=legacy"), http.StatusBadRequest, "INVALID_QUERY")

	inUse := e.do(t, http.MethodDelete, "/v1/scope-categories/"+id, nil, nil)
	wantStatus(t, inUse, http.StatusConflict, "CONFLICT")
	if inUse.body["message"] != "scope category is in use" {
		t.Fatalf("message=%v", inUse.body)
	}
	wantStatus(t, e.patchJSON(t, partURL, `{"status":"planned","scopeCategoryIds":[]}`, nil), http.StatusOK, "")
	wantStatus(t, e.do(t, http.MethodDelete, "/v1/scope-categories/"+id, nil, nil), http.StatusNoContent, "")

	renamed := e.patchJSON(t, "/v1/scope-categories/"+companyID, `{"name":"Production 1"}`, nil)
	wantStatus(t, renamed, http.StatusOK, "")
	if renamed.body["name"] != "Production 1" {
		t.Fatalf("rename=%s", renamed.raw)
	}
	badMethod := e.do(t, http.MethodPut, "/v1/scope-categories/"+companyID, nil, nil)
	wantStatus(t, badMethod, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
	if badMethod.header.Get("Allow") != "DELETE, PATCH" {
		t.Fatalf("Allow=%q", badMethod.header.Get("Allow"))
	}
}
