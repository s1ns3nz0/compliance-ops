package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 405 responses must carry Allow (RFC 9110 §15.5.6) listing the methods
// registered for the matched pattern.
func TestRouter_MethodNotAllowedCarriesAllowHeader(t *testing.T) {
	e := newEnv(t, nil)
	cases := []struct {
		method, path, allow string
	}{
		{http.MethodPost, "/v1/oscal/documents", "GET, HEAD"},
		{http.MethodDelete, "/v1/oscal/documents/" + catalogID, "GET, HEAD"},
		{http.MethodGet, "/v1/frameworks/import", "POST"},
		{http.MethodDelete, "/v1/requirements/abc", "GET, HEAD, PATCH"},
		{http.MethodPut, "/v1/evidence", "GET, HEAD, POST"},
	}
	for _, c := range cases {
		r := e.do(t, c.method, c.path, nil, nil)
		wantStatus(t, r, 405, "METHOD_NOT_ALLOWED")
		if got := r.header.Get("Allow"); got != c.allow {
			t.Fatalf("%s %s: Allow = %q, want %q", c.method, c.path, got, c.allow)
		}
	}
	rec := newRecorder(e, httptest.NewRequest(http.MethodPost, "/healthz", nil))
	if rec.Code != 405 || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST /healthz = %d Allow=%q", rec.Code, rec.Header().Get("Allow"))
	}
}
