package httpapi_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/s1ns3nz0/compliance-ops/internal/config"
	"github.com/s1ns3nz0/compliance-ops/internal/httpapi"
	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
	"github.com/s1ns3nz0/compliance-ops/internal/webui"
)

// Ported from the legacy TypeScript test suite (removed).

func TestOscal_ListsDocumentsAndSafeSummaryMetadata(t *testing.T) {
	e := newEnv(t, nil)
	r := e.get(t, "/v1/oscal/documents")
	wantStatus(t, r, 200, "")
	want := []any{map[string]any{"id": catalogID, "type": "catalog", "title": "Example OSCAL catalog", "lastModified": "2026-09-03T00:00:00Z", "controlCount": float64(2)}}
	if !reflect.DeepEqual(r.body["items"], want) {
		t.Fatalf("items = %v, want %v", r.body["items"], want)
	}
}

func TestOscal_ReturnsCanonicalDocumentAndSearchableControls(t *testing.T) {
	e := newEnv(t, nil)
	doc := e.get(t, "/v1/oscal/documents/"+catalogID)
	wantStatus(t, doc, 200, "")
	title := doc.body["document"].(map[string]any)["catalog"].(map[string]any)["metadata"].(map[string]any)["title"]
	if title != "Example OSCAL catalog" {
		t.Fatalf("document.catalog.metadata.title = %v", title)
	}
	if doc.body["controlCount"] != float64(2) || doc.body["id"] != catalogID {
		t.Fatalf("summary fields missing: %s", doc.raw)
	}

	search := e.get(t, "/v1/oscal/documents/"+catalogID+"/controls?q=nested")
	wantStatus(t, search, 200, "")
	got := items(t, search)
	if len(got) != 1 || got[0]["id"] != "ac-1.1" || search.body["total"] != float64(1) || search.body["documentId"] != catalogID {
		t.Fatalf("search = %s", search.raw)
	}

	ctl := e.get(t, "/v1/oscal/documents/"+catalogID+"/controls/ac-1")
	wantStatus(t, ctl, 200, "")
	if ctl.body["title"] != "Access Control Policy" || ctl.body["documentId"] != catalogID || ctl.body["raw"] == nil {
		t.Fatalf("control = %s", ctl.raw)
	}
	if ctl.body["text"] != "ac-1 Access Control Policy Define access control policy. statement" {
		t.Fatalf("control text = %v", ctl.body["text"])
	}
}

func TestOscal_ReadOnlyRejectsInvalidPathsAndQueries(t *testing.T) {
	e := newEnv(t, nil)
	wantStatus(t, e.do(t, http.MethodPost, "/v1/oscal/documents", nil, nil), 405, "METHOD_NOT_ALLOWED")
	wantStatus(t, e.do(t, http.MethodDelete, "/v1/oscal/documents/"+catalogID, nil, nil), 405, "METHOD_NOT_ALLOWED")
	wantStatus(t, e.get(t, "/v1/oscal/documents?q=ac"), 400, "INVALID_QUERY")
	wantStatus(t, e.get(t, "/v1/oscal/documents/"+catalogID+"/controls?limit=101"), 400, "INVALID_QUERY")
	wantStatus(t, e.get(t, "/v1/oscal/documents/"+catalogID+"/controls?limit=0"), 400, "INVALID_QUERY")
	wantStatus(t, e.get(t, "/v1/oscal/documents/"+catalogID+"/controls?limit=1.5"), 400, "INVALID_QUERY")
	wantStatus(t, e.get(t, "/v1/oscal/documents/"+catalogID+"/controls?page=2"), 400, "INVALID_QUERY")
	wantStatus(t, e.get(t, "/v1/oscal/documents/"+catalogID+"/controls/missing"), 404, "NOT_FOUND")
	wantStatus(t, e.get(t, "/v1/oscal/documents/missing"), 404, "NOT_FOUND")
	wantStatus(t, e.get(t, "/v1/oscal/nope"), 404, "NOT_FOUND")
	wantStatus(t, e.get(t, "/v1/oscal/documents/"+catalogID+"/controls?limit=1"), 200, "")
	wantStatus(t, e.get(t, "/v1/oscal/documents/"+catalogID+"/controls?limit=100"), 200, "")
}

func TestOscal_FailsClosedWhenSourceUnavailable(t *testing.T) {
	e := newEnv(t, oscal.FailingRepository{Err: errors.New("unavailable")})
	r := e.get(t, "/v1/oscal/documents")
	wantStatus(t, r, 503, "OSCAL_SOURCE_UNAVAILABLE")
	if !reflect.DeepEqual(r.body, map[string]any{"code": "OSCAL_SOURCE_UNAVAILABLE"}) {
		t.Fatalf("body = %s", r.raw)
	}
	wantStatus(t, e.get(t, "/v1/oscal/documents/"+catalogID), 503, "OSCAL_SOURCE_UNAVAILABLE")
	if strings.Contains(string(r.raw), "unavailable\"") {
		t.Fatalf("error text leaked: %s", r.raw)
	}
}

func TestOscal_ValidatesShapeAndRequiresCredentialFreeHTTPSSource(t *testing.T) {
	if _, err := oscal.ParseJSON([]byte(`{"catalog":{"uuid":"x","metadata":{}}}`)); err == nil || !strings.Contains(err.Error(), "metadata.title") {
		t.Fatalf("parse error = %v, want metadata.title", err)
	}
	tokensPath := writeTestTokensFile(t)
	env := func(source string) map[string]string {
		return map[string]string{
			"COMPLIANCE_API_TOKENS_FILE":  tokensPath,
			"COMPLIANCE_OSCAL_SOURCE_URL": source,
		}
	}
	load := func(m map[string]string) (config.Config, error) {
		return config.Load(func(k string) (string, bool) { v, ok := m[k]; return v, ok })
	}
	cfg, err := load(env("https://oscal.example/documents.json"))
	if err != nil || !reflect.DeepEqual(cfg.OscalSourceURLs, []string{"https://oscal.example/documents.json"}) {
		t.Fatalf("cfg source URLs = %v, err = %v", cfg.OscalSourceURLs, err)
	}
	// Several sources: comma and/or whitespace separated, deduplicated, ordered.
	cfg, err = load(env(" https://a.example/one.json, https://b.example/two.json\nhttps://a.example/one.json ,"))
	if err != nil || !reflect.DeepEqual(cfg.OscalSourceURLs, []string{"https://a.example/one.json", "https://b.example/two.json"}) {
		t.Fatalf("multi cfg = %+v, err = %v", cfg.OscalSourceURLs, err)
	}
	// One bad entry fails the whole list; an empty/separator-only list is "required".
	if _, err := load(env("https://a.example/one.json,http://b.example/two.json")); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("mixed list error = %v", err)
	}
	for _, raw := range []string{"", " , ", ",,"} {
		if _, err := load(env(raw)); err == nil || !strings.Contains(err.Error(), "required") {
			t.Fatalf("empty list %q error = %v", raw, err)
		}
	}
	if _, err := load(map[string]string{"COMPLIANCE_API_TOKENS_FILE": tokensPath}); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("missing error = %v", err)
	}
	if _, err := load(env("http://oscal.example/documents.json")); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("http url error = %v", err)
	}
	if _, err := load(env("https://user:pass@oscal.example/documents.json")); err == nil || !strings.Contains(err.Error(), "credential-free") {
		t.Fatalf("credential url error = %v", err)
	}
}

func TestOscal_UsesOnlyConfiguredSourceAndDoesNotWrite(t *testing.T) {
	var calls []*http.Request
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Clone(r.Context()))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(catalogJSON))
	}))
	defer src.Close()
	repo := oscal.NewHTTPRepository(src.URL + "/documents.json")
	docs, err := repo.List(t.Context())
	if err != nil || len(docs) != 1 || docs[0].ID != catalogID {
		t.Fatalf("docs = %v, err = %v", docs, err)
	}
	if len(calls) != 1 || calls[0].Method != http.MethodGet || calls[0].URL.Path != "/documents.json" || calls[0].Header.Get("Accept") != "application/json" {
		t.Fatalf("unexpected source calls: %+v", calls)
	}
}

func TestOscal_RouteLabelOmitsIdentifiersAndQueries(t *testing.T) {
	e := newEnv(t, nil)
	var seen string
	req := httptest.NewRequest(http.MethodGet, "/v1/oscal/documents/x/controls/ac-1?q=secret", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	seen = httpapi.RouteLabel(req)
	if seen != "GET /v1/oscal/documents/{documentId}/controls/{controlId}" {
		t.Fatalf("route label = %q", seen)
	}
	if strings.Contains(seen, "secret") || strings.Contains(seen, "ac-1") {
		t.Fatalf("route label leaks identifiers: %q", seen)
	}
}

func TestOscal_ServesUIFromAPIOrigin(t *testing.T) {
	e := newEnv(t, nil, func(d *httpapi.Deps) { d.UI = webui.Handler() })
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body := new(strings.Builder)
	_, _ = io.Copy(body, res.Body)
	if res.StatusCode != 200 || !strings.Contains(body.String(), "Compliance Ops") {
		t.Fatalf("status = %d body = %q", res.StatusCode, body.String())
	}
	// SPA fallback for a client-side route, and no auth required.
	res2, err := http.Get(srv.URL + "/requirements/abc")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != 200 || !strings.HasPrefix(res2.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("spa fallback status = %d ct = %q", res2.StatusCode, res2.Header.Get("Content-Type"))
	}
	// Health is public.
	res3, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res3.Body.Close()
	if res3.StatusCode != 200 {
		t.Fatalf("healthz = %d", res3.StatusCode)
	}
}
