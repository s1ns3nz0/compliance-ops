package webui_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/s1ns3nz0/compliance-ops/internal/webui"
)

func serve(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestHandler_ServesIndexAndSPAFallbackForExtensionlessRoutes(t *testing.T) {
	h := webui.Handler()
	root := serve(t, h, http.MethodGet, "/")
	if root.Code != 200 || !strings.HasPrefix(root.Header().Get("Content-Type"), "text/html") || !strings.Contains(root.Body.String(), "Compliance Ops") {
		t.Fatalf("GET / = %d %q %q", root.Code, root.Header().Get("Content-Type"), root.Body.String())
	}
	if cc := root.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("index Cache-Control = %q", cc)
	}
	for _, p := range []string{"/requirements/abc", "/frameworks/1c1e9a2e-0f3d-4d6c-9f1a-2b3c4d5e6f70", "/evidence", "/index.html"} {
		r := serve(t, h, http.MethodGet, p)
		if r.Code != 200 || r.Body.String() != root.Body.String() {
			t.Fatalf("GET %s = %d, want SPA index (same body)", p, r.Code)
		}
	}
}

func TestHandler_MissingAssetsAndFilesAre404NotSPAFallback(t *testing.T) {
	h := webui.Handler()
	for _, p := range []string{
		"/assets/does-not-exist.js",
		"/assets/nope",
		"/missing.js", "/missing.css", "/missing.map", "/favicon-missing.ico",
		"/img/missing.png", "/missing.svg", "/manifest-missing.json", "/robots-missing.txt",
	} {
		r := serve(t, h, http.MethodGet, p)
		if r.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404 (body %q)", p, r.Code, r.Body.String())
		}
		if strings.Contains(r.Body.String(), "<html") {
			t.Fatalf("GET %s served index.html instead of 404", p)
		}
		if r.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("GET %s missing nosniff", p)
		}
	}
}

func TestHandler_RejectsNonGetMethods(t *testing.T) {
	h := webui.Handler()
	r := serve(t, h, http.MethodPost, "/")
	if r.Code != http.StatusMethodNotAllowed || r.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST / = %d Allow=%q", r.Code, r.Header().Get("Allow"))
	}
}
