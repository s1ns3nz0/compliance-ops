package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/s1ns3nz0/compliance-ops/internal/blob"
	"github.com/s1ns3nz0/compliance-ops/internal/httpapi"
	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
	"github.com/s1ns3nz0/compliance-ops/internal/store"
)

const (
	testToken = "test-token-0123456789abcdef"
	catalogID = "8e4e759b-bd43-4d64-91b5-c04d6b1c4a93"
)

func writeTestTokensFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tokens.json")
	contents := `{"test":"` + strings.Repeat("t", 14) + `"}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// catalogJSON mirrors the fixture ported from the legacy TypeScript test suite (removed).
const catalogJSON = `{
  "catalog": {
    "uuid": "8e4e759b-bd43-4d64-91b5-c04d6b1c4a93",
    "metadata": { "title": "Example OSCAL catalog", "last-modified": "2026-09-03T00:00:00Z" },
    "groups": [{ "id": "ac", "title": "Access control", "controls": [{ "id": "ac-1", "title": "Access Control Policy", "parts": [{ "name": "statement", "prose": "Define access control policy." }], "controls": [{ "id": "ac-1.1", "title": "Nested control" }] }] }]
  }
}`

func fixtureDocs(t *testing.T) []oscal.Document {
	t.Helper()
	docs, err := oscal.ParseJSON([]byte(catalogJSON))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return docs
}

// countingBlobs wraps the in-memory blob store and counts Put calls.
type countingBlobs struct {
	blob.Store
	puts atomic.Int32
}

func (c *countingBlobs) Put(ctx context.Context, key string, r io.Reader, size int64, ct string) error {
	c.puts.Add(1)
	return c.Store.Put(ctx, key, r, size, ct)
}

type env struct {
	h     http.Handler
	store *store.Memory
	blobs *countingBlobs
	now   time.Time
}

func newEnv(t *testing.T, repo oscal.Repository, opts ...func(*httpapi.Deps)) *env {
	t.Helper()
	if repo == nil {
		repo = oscal.StaticRepository{Documents: fixtureDocs(t)}
	}
	e := &env{store: store.NewMemory(), blobs: &countingBlobs{Store: blob.NewMemory()}, now: time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)}
	e.store.Now = func() time.Time { return e.now }
	deps := httpapi.Deps{Oscal: repo, Store: e.store, Blobs: e.blobs, APITokens: []string{testToken}, AuditActor: "test-operator", MaxUploadBytes: 1 << 20, Now: func() time.Time { return e.now }}
	for _, o := range opts {
		o(&deps)
	}
	e.h = httpapi.New(deps)
	return e
}

type resp struct {
	code   int
	header http.Header
	raw    []byte
	body   map[string]any
}

func (e *env) do(t *testing.T, method, target string, body io.Reader, hdr map[string]string) resp {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Authorization", "Bearer "+testToken)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	out := resp{code: rec.Code, header: rec.Header(), raw: rec.Body.Bytes()}
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(out.raw, &out.body)
	}
	return out
}

func (e *env) get(t *testing.T, target string) resp { return e.do(t, http.MethodGet, target, nil, nil) }

// newRecorder serves a raw request (no default auth header) and returns the recorder.
func newRecorder(e *env, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// panicRepo simulates a bug inside a handler dependency.
type panicRepo struct{}

func (panicRepo) List(context.Context) ([]oscal.Document, error) { panic("boom") }

func (e *env) postJSON(t *testing.T, target string, v any, hdr map[string]string) resp {
	t.Helper()
	b, _ := json.Marshal(v)
	if hdr == nil {
		hdr = map[string]string{}
	}
	hdr["Content-Type"] = "application/json"
	return e.do(t, http.MethodPost, target, bytes.NewReader(b), hdr)
}

func (e *env) patchJSON(t *testing.T, target string, raw string, hdr map[string]string) resp {
	t.Helper()
	if hdr == nil {
		hdr = map[string]string{}
	}
	hdr["Content-Type"] = "application/json"
	return e.do(t, http.MethodPatch, target, strings.NewReader(raw), hdr)
}

func wantStatus(t *testing.T, r resp, code int, errCode string) {
	t.Helper()
	if r.code != code {
		t.Fatalf("status = %d, want %d (body %s)", r.code, code, r.raw)
	}
	if errCode != "" && r.body["code"] != errCode {
		t.Fatalf("code = %v, want %s", r.body["code"], errCode)
	}
}

func items(t *testing.T, r resp) []map[string]any {
	t.Helper()
	list, ok := r.body["items"].([]any)
	if !ok {
		t.Fatalf("items missing in %s", r.raw)
	}
	out := make([]map[string]any, 0, len(list))
	for _, it := range list {
		out = append(out, it.(map[string]any))
	}
	return out
}

// importAll imports the fixture catalog and returns its requirements.
func importAll(t *testing.T, e *env) (store.Framework, []store.Requirement) {
	t.Helper()
	r := e.postJSON(t, "/v1/frameworks/import", map[string]any{}, nil)
	wantStatus(t, r, 200, "")
	fws, _ := e.store.ListFrameworks(context.Background())
	if len(fws) != 1 {
		t.Fatalf("frameworks = %d, want 1", len(fws))
	}
	reqs, _, _ := e.store.ListRequirements(context.Background(), store.RequirementFilter{FrameworkID: fws[0].ID, Limit: 200})
	return fws[0], reqs
}

type filePart struct {
	name, contentType string
	data              []byte
}

func multipartBody(t *testing.T, fields map[string][]string, file *filePart) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, vs := range fields {
		for _, v := range vs {
			if err := mw.WriteField(k, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	if file != nil {
		hdr := map[string][]string{
			"Content-Disposition": {`form-data; name="file"; filename="` + file.name + `"`},
			"Content-Type":        {file.contentType},
		}
		pw, err := mw.CreatePart(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pw.Write(file.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}
