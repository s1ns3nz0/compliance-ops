package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/s1ns3nz0/compliance-ops/internal/httpapi"
)

func TestSecurityHeadersCoverUIAPIAndHealth(t *testing.T) {
	ui := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
	})
	e := newEnv(t, nil, func(deps *httpapi.Deps) { deps.UI = ui })

	for _, path := range []string{"/", "/v1", "/healthz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if path == "/v1" {
			req.Header.Set("Authorization", "Bearer "+testToken)
		}
		rec := newRecorder(e, req)
		for name, want := range map[string]string{
			"Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'",
			"Referrer-Policy":         "no-referrer",
			"Permissions-Policy":      "camera=(), geolocation=(), microphone=()",
			"X-Content-Type-Options":  "nosniff",
			"X-Frame-Options":         "DENY",
		} {
			if got := rec.Header().Get(name); got != want {
				t.Errorf("GET %s: %s = %q, want %q", path, name, got, want)
			}
		}
	}
}
