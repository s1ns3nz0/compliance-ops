package httpapi_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

func TestRequirementPatchExpectedUpdatedAtAPI(t *testing.T) {
	e := newEnv(t, nil)
	_, reqs := importAll(t, e)
	path := "/v1/requirements/" + reqs[0].ID
	detail := e.get(t, path)
	wantStatus(t, detail, http.StatusOK, "")
	updatedAt := detail.body["updatedAt"].(string)

	exact := e.patchJSON(t, path, fmt.Sprintf(`{"notes":"exact","expectedUpdatedAt":%q}`, updatedAt), nil)
	wantStatus(t, exact, http.StatusOK, "")
	if exact.body["notes"] != "exact" {
		t.Fatalf("exact response = %s", exact.raw)
	}
	stale := e.patchJSON(t, path, fmt.Sprintf(`{"notes":"wrong","expectedUpdatedAt":%q}`, updatedAt), nil)
	wantStatus(t, stale, http.StatusConflict, "CONFLICT")
	if stale.body["message"] != "resource was modified" {
		t.Fatalf("conflict message = %v", stale.body)
	}
	wantStatus(t, e.patchJSON(t, path, `{"notes":"legacy"}`, nil), http.StatusOK, "")
	for _, body := range []string{
		`{"notes":"x","expectedUpdatedAt":null}`,
		`{"notes":"x","expectedUpdatedAt":"not-a-time"}`,
		`{"notes":"x","expectedUpdatedAt":42}`,
	} {
		wantStatus(t, e.patchJSON(t, path, body, nil), http.StatusBadRequest, "INVALID_BODY")
	}
}

func TestScopeCategoryExpectedUpdatedAtAPI(t *testing.T) {
	e := newEnv(t, nil)
	created := e.postJSON(t, "/v1/scope-categories", map[string]any{"name": "scope"}, nil)
	wantStatus(t, created, http.StatusCreated, "")
	path := "/v1/scope-categories/" + created.body["id"].(string)
	updatedAt := created.body["updatedAt"].(string)

	exact := e.patchJSON(t, path, fmt.Sprintf(`{"name":"renamed","expectedUpdatedAt":%q}`, updatedAt), nil)
	wantStatus(t, exact, http.StatusOK, "")
	stalePatch := e.patchJSON(t, path, fmt.Sprintf(`{"name":"wrong","expectedUpdatedAt":%q}`, updatedAt), nil)
	wantStatus(t, stalePatch, http.StatusConflict, "CONFLICT")
	if stalePatch.body["message"] != "resource was modified" {
		t.Fatalf("stale patch message = %v", stalePatch.body)
	}
	wantStatus(t, e.patchJSON(t, path, `{"name":"legacy"}`, nil), http.StatusOK, "")
	for _, body := range []string{
		`{"name":"x","expectedUpdatedAt":null}`,
		`{"name":"x","expectedUpdatedAt":"not-a-time"}`,
		`{"name":"x","expectedUpdatedAt":42}`,
	} {
		wantStatus(t, e.patchJSON(t, path, body, nil), http.StatusBadRequest, "INVALID_BODY")
	}

	current := e.get(t, "/v1/scope-categories?q=legacy")
	wantStatus(t, current, http.StatusOK, "")
	currentUpdatedAt := items(t, current)[0]["updatedAt"].(string)
	staleDelete := e.do(t, http.MethodDelete, path+"?expectedUpdatedAt="+url.QueryEscape(updatedAt), nil, nil)
	wantStatus(t, staleDelete, http.StatusConflict, "CONFLICT")
	if staleDelete.body["message"] != "resource was modified" {
		t.Fatalf("stale delete message = %v", staleDelete.body)
	}
	wantStatus(t, e.do(t, http.MethodDelete, path+"?expectedUpdatedAt=not-a-time", nil, nil), http.StatusBadRequest, "INVALID_QUERY")
	wantStatus(t, e.do(t, http.MethodDelete, path+"?expectedUpdatedAt="+url.QueryEscape(currentUpdatedAt)+"&expectedUpdatedAt="+url.QueryEscape(currentUpdatedAt), nil, nil), http.StatusBadRequest, "INVALID_QUERY")
	wantStatus(t, e.do(t, http.MethodDelete, path+"?unknown=1", nil, nil), http.StatusBadRequest, "INVALID_QUERY")
	wantStatus(t, e.do(t, http.MethodDelete, path+"?expectedUpdatedAt="+url.QueryEscape(currentUpdatedAt), nil, nil), http.StatusNoContent, "")

	wantStatus(t, e.postJSON(t, "/v1/scope-categories", map[string]any{"name": "not-a-patch", "expectedUpdatedAt": currentUpdatedAt}, nil), http.StatusBadRequest, "INVALID_BODY")
	legacyDelete := e.postJSON(t, "/v1/scope-categories", map[string]any{"name": "legacy-delete"}, nil)
	wantStatus(t, legacyDelete, http.StatusCreated, "")
	wantStatus(t, e.do(t, http.MethodDelete, "/v1/scope-categories/"+legacyDelete.body["id"].(string), nil, nil), http.StatusNoContent, "")
}

func TestPartPatchExpectedUpdatedAtAPI(t *testing.T) {
	e := newEnv(t, nil)
	_, reqs := importAll(t, e)
	path := "/v1/requirements/" + reqs[0].ID + "/parts/ac-1_smt"

	created := e.patchJSON(t, path, `{"owner":"alice","expectedUpdatedAt":null}`, nil)
	wantStatus(t, created, http.StatusOK, "")
	updatedAt := created.body["part"].(map[string]any)["updatedAt"].(string)

	staleCreate := e.patchJSON(t, path, `{"owner":"wrong","expectedUpdatedAt":null}`, nil)
	wantStatus(t, staleCreate, http.StatusConflict, "CONFLICT")
	if staleCreate.body["message"] != "resource was modified" {
		t.Fatalf("conflict message = %v", staleCreate.body)
	}

	exact := e.patchJSON(t, path, fmt.Sprintf(`{"owner":"bob","expectedUpdatedAt":%q}`, updatedAt), nil)
	wantStatus(t, exact, http.StatusOK, "")
	if exact.body["part"].(map[string]any)["owner"] != "bob" {
		t.Fatalf("exact response = %s", exact.raw)
	}

	stale := e.patchJSON(t, path, fmt.Sprintf(`{"owner":"wrong","expectedUpdatedAt":%q}`, updatedAt), nil)
	wantStatus(t, stale, http.StatusConflict, "CONFLICT")
	legacy := e.patchJSON(t, path, `{"owner":"legacy"}`, nil)
	wantStatus(t, legacy, http.StatusOK, "")

	for _, body := range []string{
		`{"owner":"x","expectedUpdatedAt":"not-a-time"}`,
		`{"owner":"x","expectedUpdatedAt":42}`,
		`{"owner":"x","expectedUpdatedAt":true}`,
	} {
		wantStatus(t, e.patchJSON(t, path, body, nil), http.StatusBadRequest, "INVALID_BODY")
	}
}
