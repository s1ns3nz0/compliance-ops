package httpapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/s1ns3nz0/compliance-ops/internal/blob"
	"github.com/s1ns3nz0/compliance-ops/internal/httpapi"
	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
	"github.com/s1ns3nz0/compliance-ops/internal/store"
)

func TestAuth_RejectsMissingAndWrongToken(t *testing.T) {
	e := newEnv(t, nil)
	for _, tc := range []struct {
		name  string
		auth  string
		wantC int
	}{
		{"missing", "", 401},
		{"wrong", "Bearer nope", 401},
		{"prefix-of-token", "Bearer " + testToken[:10], 401},
		{"basic", "Basic abc", 401},
		{"valid", "Bearer " + testToken, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, "/v1/oscal/documents", nil)
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			rec := newRecorder(e, req)
			if rec.Code != tc.wantC {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantC, rec.Body.String())
			}
			if tc.wantC == 401 && !strings.Contains(rec.Body.String(), `"UNAUTHORIZED"`) {
				t.Fatalf("body = %s", rec.Body.String())
			}
			if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("hardening headers missing: %v", rec.Header())
			}
		})
	}
	// Unknown /v1 route with valid token → 404 JSON; without → 401.
	wantStatus(t, e.get(t, "/v1/whatever"), 404, "NOT_FOUND")
	// Health is public and JSON.
	req, _ := http.NewRequest(http.MethodGet, "/healthz", nil)
	rec := newRecorder(e, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Fatalf("healthz = %d %s", rec.Code, rec.Body.String())
	}
}

func TestTracking_UnavailableWithoutStore(t *testing.T) {
	e := newEnv(t, nil, func(d *httpapi.Deps) { d.Store = nil; d.Blobs = nil })
	wantStatus(t, e.get(t, "/v1/frameworks"), 503, "TRACKING_UNAVAILABLE")
	wantStatus(t, e.get(t, "/v1/dashboard"), 503, "TRACKING_UNAVAILABLE")
	wantStatus(t, e.do(t, http.MethodPost, "/v1/evidence", nil, nil), 503, "TRACKING_UNAVAILABLE")
	wantStatus(t, e.get(t, "/v1/oscal/documents"), 200, "")
}

func TestFrameworks_ImportIsIdempotent(t *testing.T) {
	e := newEnv(t, nil)
	first := e.postJSON(t, "/v1/frameworks/import", map[string]any{}, nil)
	wantStatus(t, first, 200, "")
	got := items(t, first)
	if len(got) != 1 || got[0]["created"] != float64(2) || got[0]["updated"] != float64(0) {
		t.Fatalf("first import = %s", first.raw)
	}
	fw := got[0]["framework"].(map[string]any)
	if fw["oscalDocumentId"] != catalogID || fw["requirementCount"] != float64(2) {
		t.Fatalf("framework = %v", fw)
	}

	second := e.postJSON(t, "/v1/frameworks/import", map[string]any{"documentId": catalogID}, nil)
	wantStatus(t, second, 200, "")
	got = items(t, second)
	if len(got) != 1 || got[0]["created"] != float64(0) || got[0]["updated"] != float64(2) {
		t.Fatalf("second import = %s", second.raw)
	}
	if got[0]["framework"].(map[string]any)["id"] != fw["id"] {
		t.Fatalf("framework id changed across imports")
	}

	wantStatus(t, e.postJSON(t, "/v1/frameworks/import", map[string]any{"documentId": "missing"}, nil), 404, "NOT_FOUND")
	wantStatus(t, e.postJSON(t, "/v1/frameworks/import", map[string]any{"bogus": 1}, nil), 400, "INVALID_BODY")
	// Empty body imports everything.
	wantStatus(t, e.do(t, http.MethodPost, "/v1/frameworks/import", nil, nil), 200, "")

	list := e.get(t, "/v1/frameworks")
	wantStatus(t, list, 200, "")
	if len(items(t, list)) != 1 {
		t.Fatalf("frameworks = %s", list.raw)
	}
	one := e.get(t, "/v1/frameworks/"+fw["id"].(string))
	wantStatus(t, one, 200, "")
	if one.body["title"] != "Example OSCAL catalog" {
		t.Fatalf("framework = %s", one.raw)
	}
	wantStatus(t, e.get(t, "/v1/frameworks/nope"), 404, "NOT_FOUND")
}

func TestRequirements_ListFilterAndValidation(t *testing.T) {
	e := newEnv(t, nil)
	fw, _ := importAll(t, e)
	r := e.get(t, "/v1/requirements?frameworkId="+fw.ID+"&limit=1&offset=0")
	wantStatus(t, r, 200, "")
	if len(items(t, r)) != 1 || r.body["total"] != float64(2) {
		t.Fatalf("list = %s", r.raw)
	}
	r = e.get(t, "/v1/requirements?q=nested")
	if len(items(t, r)) != 1 || items(t, r)[0]["controlId"] != "ac-1.1" {
		t.Fatalf("search = %s", r.raw)
	}
	r = e.get(t, "/v1/requirements?status=planned")
	if r.body["total"] != float64(2) {
		t.Fatalf("status filter = %s", r.raw)
	}
	// List rows omit the parts tree but always carry related.
	if _, has := items(t, r)[0]["parts"]; has {
		t.Fatalf("list must omit parts: %s", r.raw)
	}
	if rel, ok := items(t, r)[0]["related"].([]any); !ok || rel == nil {
		t.Fatalf("list related must be an array: %s", r.raw)
	}
	wantStatus(t, e.get(t, "/v1/requirements?status=not_started"), 400, "INVALID_QUERY")
	wantStatus(t, e.get(t, "/v1/requirements?status=done"), 400, "INVALID_QUERY")
	wantStatus(t, e.get(t, "/v1/requirements?limit=-1"), 400, "INVALID_QUERY")
	wantStatus(t, e.get(t, "/v1/requirements?offset=x"), 400, "INVALID_QUERY")
	wantStatus(t, e.get(t, "/v1/requirements?foo=1"), 400, "INVALID_QUERY")
	wantStatus(t, e.get(t, "/v1/requirements/missing"), 404, "NOT_FOUND")
}

func TestRequirements_PatchUsesTrustedAuditActor(t *testing.T) {
	e := newEnv(t, nil)
	_, reqs := importAll(t, e)
	id := reqs[0].ID

	r := e.patchJSON(t, "/v1/requirements/"+id, `{"statusOverride":"partial","notes":"working"}`, map[string]string{"X-Actor": "  alice@example.com "})
	wantStatus(t, r, 200, "")
	if r.body["status"] != "partial" || r.body["derivedStatus"] != "planned" || r.body["statusOverride"] != "partial" || r.body["notes"] != "working" || r.body["owner"] != "" {
		t.Fatalf("patched = %s", r.raw)
	}
	if _, has := r.body["dueDate"]; has {
		t.Fatalf("dueDate must be absent: %s", r.raw)
	}

	audit, _ := e.store.ListAudit(context.Background(), 200)
	var updates []store.AuditEntry
	for _, a := range audit {
		if a.Action == "requirement.update" {
			updates = append(updates, a)
		}
	}
	if len(updates) != 1 {
		t.Fatalf("requirement.update audit rows = %d, want 1", len(updates))
	}
	if updates[0].Actor != "test-operator" || updates[0].EntityID != id {
		t.Fatalf("audit row = %+v", updates[0])
	}
	change, _ := updates[0].Detail["statusOverride"].(map[string]any)
	if change["from"] != nil || change["to"] != "partial" || updates[0].Detail["notes"] != true {
		t.Fatalf("audit detail = %+v", updates[0].Detail)
	}

	// Absent leaves the override untouched; null clears it.
	r = e.patchJSON(t, "/v1/requirements/"+id, `{"notes":"more"}`, nil)
	wantStatus(t, r, 200, "")
	if r.body["statusOverride"] != "partial" || r.body["status"] != "partial" {
		t.Fatalf("override should be untouched: %s", r.raw)
	}
	r = e.patchJSON(t, "/v1/requirements/"+id, `{"statusOverride":null}`, nil)
	wantStatus(t, r, 200, "")
	if _, has := r.body["statusOverride"]; has || r.body["status"] != "planned" {
		t.Fatalf("override should be cleared: %s", r.raw)
	}

	// Derived fields are rejected with a specific message.
	for _, body := range []string{`{"status":"implemented"}`, `{"owner":"alice"}`, `{"dueDate":"2026-10-01"}`, `{"dueDate":null}`, `{"status":null,"notes":"x"}`} {
		bad := e.patchJSON(t, "/v1/requirements/"+id, body, nil)
		wantStatus(t, bad, 400, "INVALID_BODY")
		if bad.body["message"] != "status, owner and dueDate are derived from parts" {
			t.Fatalf("%s → message %v", body, bad.body["message"])
		}
	}
	wantStatus(t, e.patchJSON(t, "/v1/requirements/"+id, `{"statusOverride":"done"}`, nil), 400, "INVALID_BODY")
	wantStatus(t, e.patchJSON(t, "/v1/requirements/"+id, `{"statusOverride":"in_progress"}`, nil), 400, "INVALID_BODY")
	wantStatus(t, e.patchJSON(t, "/v1/requirements/"+id, `{"statusOverride":1}`, nil), 400, "INVALID_BODY")
	wantStatus(t, e.patchJSON(t, "/v1/requirements/"+id, `{"notes":"`+strings.Repeat("n", 10001)+`"}`, nil), 400, "INVALID_BODY")
	for _, s := range []string{"implemented", "partial", "planned", "alternative", "not_applicable"} {
		wantStatus(t, e.patchJSON(t, "/v1/requirements/"+id, `{"statusOverride":"`+s+`"}`, nil), 200, "")
	}
	wantStatus(t, e.patchJSON(t, "/v1/requirements/"+id, `{"unknown":1}`, nil), 400, "INVALID_BODY")
	wantStatus(t, e.patchJSON(t, "/v1/requirements/"+id, `not json`, nil), 400, "INVALID_BODY")
	wantStatus(t, e.patchJSON(t, "/v1/requirements/missing", `{"notes":"x"}`, nil), 404, "NOT_FOUND")

	detail := e.get(t, "/v1/requirements/"+id)
	wantStatus(t, detail, 200, "")
	if ev, ok := detail.body["evidence"].([]any); !ok || len(ev) != 0 {
		t.Fatalf("evidence field = %v", detail.body["evidence"])
	}
	if _, has := detail.body["activities"]; has {
		t.Fatalf("activities must be gone: %s", detail.raw)
	}
	// Audit listing route.
	al := e.get(t, "/v1/audit?limit=5")
	wantStatus(t, al, 200, "")
	if len(items(t, al)) == 0 {
		t.Fatalf("audit empty")
	}
	wantStatus(t, e.get(t, "/v1/audit?limit=0"), 400, "INVALID_QUERY")
}

// setPart patches one trackable part and returns the response.
func setPart(t *testing.T, e *env, reqID, partID, body string) resp {
	t.Helper()
	r := e.patchJSON(t, "/v1/requirements/"+reqID+"/parts/"+partID, body, nil)
	wantStatus(t, r, 200, "")
	return r
}

func upload(t *testing.T, e *env, fields map[string][]string, file *filePart, hdr map[string]string) resp {
	t.Helper()
	body, ct := multipartBody(t, fields, file)
	if hdr == nil {
		hdr = map[string]string{}
	}
	hdr["Content-Type"] = ct
	return e.do(t, http.MethodPost, "/v1/evidence", body, hdr)
}

type createEvidenceFailStore struct{ store.Store }

func (createEvidenceFailStore) CreateEvidence(context.Context, store.Evidence, string) (store.Evidence, error) {
	return store.Evidence{}, errors.New("injected evidence failure")
}

type oneFailureStore struct {
	store.Store
	firstEntered chan struct{}
	releaseFirst chan struct{}
	calls        atomic.Int32
}

func (s *oneFailureStore) CreateEvidence(ctx context.Context, e store.Evidence, actor string) (store.Evidence, error) {
	if s.calls.Add(1) == 1 {
		close(s.firstEntered)
		select {
		case <-s.releaseFirst:
			return store.Evidence{}, errors.New("injected first evidence failure")
		case <-ctx.Done():
			return store.Evidence{}, ctx.Err()
		}
	}
	return s.Store.CreateEvidence(ctx, e, actor)
}

type existingSignalBlobs struct {
	blob.Store
	existing chan struct{}
	once     sync.Once
}

func (b *existingSignalBlobs) Exists(ctx context.Context, key string) (bool, error) {
	exists, err := b.Store.Exists(ctx, key)
	if err == nil && exists {
		b.once.Do(func() { close(b.existing) })
	}
	return exists, err
}

func TestEvidence_ConcurrentSameDigestFailureDoesNotDeleteSuccessfulBlob(t *testing.T) {
	e := newEnv(t, nil)
	_, reqs := importAll(t, e)
	failing := &oneFailureStore{Store: e.store, firstEntered: make(chan struct{}), releaseFirst: make(chan struct{})}
	existing := &existingSignalBlobs{Store: e.blobs.Store, existing: make(chan struct{})}
	e.blobs.Store = existing
	e.h = httpapi.New(httpapi.Deps{Oscal: oscal.StaticRepository{Documents: fixtureDocs(t)}, Store: failing, Blobs: e.blobs, APITokens: []string{testToken}, AuditActor: "test-operator", MaxUploadBytes: 1 << 20, Now: func() time.Time { return e.now }})

	data := []byte("same digest concurrent evidence")
	fields := map[string][]string{"title": {"Concurrent"}, "requirementIds": {reqs[0].ID}}
	results := make(chan resp, 2)
	go func() { results <- upload(t, e, fields, &filePart{name: "first.txt", data: data}, nil) }()
	<-failing.firstEntered
	go func() { results <- upload(t, e, fields, &filePart{name: "second.txt", data: data}, nil) }()

	// Without digest serialization, the second request observes the first
	// request's blob before the failing request compensates it.
	select {
	case <-existing.existing:
	case <-time.After(100 * time.Millisecond):
	}
	close(failing.releaseFirst)

	r1, r2 := <-results, <-results
	var succeeded resp
	if r1.code == http.StatusCreated && r2.code == http.StatusInternalServerError {
		succeeded = r1
	} else if r2.code == http.StatusCreated && r1.code == http.StatusInternalServerError {
		succeeded = r2
	} else {
		t.Fatalf("statuses = %d and %d, want 201 and 500", r1.code, r2.code)
	}
	download := e.get(t, "/v1/evidence/"+succeeded.body["id"].(string)+"/download")
	if download.code != http.StatusOK || !bytes.Equal(download.raw, data) {
		t.Fatalf("successful evidence download = %d %q", download.code, download.raw)
	}
}

func TestEvidence_CreateFailureCompensatesOnlyNewBlob(t *testing.T) {
	newFailingEnv := func(t *testing.T) *env {
		return newEnv(t, nil, func(d *httpapi.Deps) { d.Store = createEvidenceFailStore{Store: d.Store} })
	}
	t.Run("new blob is deleted", func(t *testing.T) {
		e := newFailingEnv(t)
		_, reqs := importAll(t, e)
		data := []byte("new evidence")
		r := upload(t, e, map[string][]string{"title": {"New"}, "requirementIds": {reqs[0].ID}}, &filePart{name: "new.txt", data: data}, nil)
		wantStatus(t, r, 500, "INTERNAL")
		if got := e.blobs.Store.(*blob.Memory).Len(); got != 0 {
			t.Fatalf("new blob retained after metadata failure: %d", got)
		}
	})
	t.Run("preexisting blob is retained", func(t *testing.T) {
		e := newFailingEnv(t)
		_, reqs := importAll(t, e)
		data := []byte("shared evidence")
		sum := sha256.Sum256(data)
		key := hex.EncodeToString(sum[:])
		memory := e.blobs.Store.(*blob.Memory)
		if err := memory.Put(t.Context(), key, bytes.NewReader(data), int64(len(data)), "text/plain"); err != nil {
			t.Fatal(err)
		}
		r := upload(t, e, map[string][]string{"title": {"Shared"}, "requirementIds": {reqs[0].ID}}, &filePart{name: "shared.txt", data: data}, nil)
		wantStatus(t, r, 500, "INTERNAL")
		if exists, err := memory.Exists(t.Context(), key); err != nil || !exists || memory.Len() != 1 {
			t.Fatalf("preexisting blob was removed: exists=%v len=%d err=%v", exists, memory.Len(), err)
		}
	})
}

func TestEvidence_UploadDownloadRoundTrip(t *testing.T) {
	e := newEnv(t, nil)
	_, reqs := importAll(t, e)
	data := bytes.Repeat([]byte("evidence bytes\n"), 1000)
	sum := sha256.Sum256(data)
	wantSha := hex.EncodeToString(sum[:])

	r := upload(t, e, map[string][]string{
		"title":          {"SOC2 report"},
		"description":    {"Annual report"},
		"validFrom":      {"2026-01-01"},
		"validUntil":     {"2026-12-31"},
		"requirementIds": {reqs[0].ID + "," + reqs[1].ID},
	}, &filePart{name: `../we;ird/report.pdf`, contentType: "application/pdf", data: data}, map[string]string{"X-Actor": "uploader"})
	wantStatus(t, r, 201, "")
	if r.body["sha256"] != wantSha || r.body["sizeBytes"] != float64(len(data)) || r.body["contentType"] != "application/pdf" || r.body["uploadedBy"] != "test-operator" {
		t.Fatalf("evidence = %s", r.raw)
	}
	for _, gone := range []string{"reviewState", "reviewer", "reviewComment", "reviewedAt"} {
		if _, has := r.body[gone]; has {
			t.Fatalf("review field %q must be gone: %s", gone, r.raw)
		}
	}
	if _, has := r.body["activityIds"]; has {
		t.Fatalf("activityIds must be gone: %s", r.raw)
	}
	if r.body["kind"] != "file" {
		t.Fatalf("kind = %v, want file", r.body["kind"])
	}
	if _, has := r.body["url"]; has {
		t.Fatalf("file evidence must omit url: %s", r.raw)
	}
	if ids := r.body["requirementIds"].([]any); len(ids) != 2 {
		t.Fatalf("requirementIds = %v", ids)
	}
	if strings.Contains(r.body["fileName"].(string), "/") || strings.Contains(r.body["fileName"].(string), `"`) {
		t.Fatalf("fileName not sanitized: %v", r.body["fileName"])
	}
	id := r.body["id"].(string)

	dl := e.get(t, "/v1/evidence/"+id+"/download")
	if dl.code != 200 {
		t.Fatalf("download status = %d: %s", dl.code, dl.raw)
	}
	if !bytes.Equal(dl.raw, data) {
		t.Fatalf("download bytes differ (%d vs %d)", len(dl.raw), len(data))
	}
	got := sha256.Sum256(dl.raw)
	if hex.EncodeToString(got[:]) != wantSha || dl.header.Get("X-Content-Sha256") != wantSha {
		t.Fatalf("sha mismatch: %s", dl.header.Get("X-Content-Sha256"))
	}
	if dl.header.Get("Content-Type") != "application/pdf" || dl.header.Get("Content-Length") != "15000" || dl.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers = %v", dl.header)
	}
	if cd := dl.header.Get("Content-Disposition"); cd != `attachment; filename="report.pdf"` {
		t.Fatalf("content-disposition = %q, want quoted sanitized base name", cd)
	}

	// Listing and detail.
	list := e.get(t, "/v1/evidence?requirementId="+reqs[0].ID)
	wantStatus(t, list, 200, "")
	if list.body["total"] != float64(1) {
		t.Fatalf("list = %s", list.raw)
	}
	// reviewState is no longer a known parameter (strict query policy).
	wantStatus(t, e.get(t, "/v1/evidence?reviewState=approved"), 400, "INVALID_QUERY")
	wantStatus(t, e.get(t, "/v1/evidence?activityId=nope"), 400, "INVALID_QUERY")
	wantStatus(t, e.get(t, "/v1/evidence/"+id), 200, "")
	wantStatus(t, e.get(t, "/v1/evidence/missing"), 404, "NOT_FOUND")
	detail := e.get(t, "/v1/requirements/"+reqs[0].ID)
	if ev := detail.body["evidence"].([]any); len(ev) != 1 {
		t.Fatalf("requirement evidence = %v", ev)
	}
}

func TestEvidence_SameFileTwiceStoresBlobOnce(t *testing.T) {
	e := newEnv(t, nil)
	_, reqs := importAll(t, e)
	data := []byte("same content")
	fields := map[string][]string{"title": {"A"}, "requirementIds": {reqs[0].ID}}
	wantStatus(t, upload(t, e, fields, &filePart{name: "a.txt", contentType: "text/plain", data: data}, nil), 201, "")
	fields2 := map[string][]string{"title": {"B"}, "requirementIds": {reqs[0].ID, reqs[1].ID}}
	wantStatus(t, upload(t, e, fields2, &filePart{name: "b.txt", contentType: "text/plain", data: data}, nil), 201, "")
	if n := e.blobs.puts.Load(); n != 1 {
		t.Fatalf("blob Put calls = %d, want 1", n)
	}
	list := e.get(t, "/v1/evidence")
	if list.body["total"] != float64(2) {
		t.Fatalf("evidence total = %v", list.body["total"])
	}
}

func TestEvidence_UploadValidation(t *testing.T) {
	e := newEnv(t, nil, func(d *httpapi.Deps) { d.MaxUploadBytes = 4096 })
	_, reqs := importAll(t, e)
	ok := map[string][]string{"title": {"T"}, "requirementIds": {reqs[0].ID}}

	big := bytes.Repeat([]byte("x"), 8192)
	wantStatus(t, upload(t, e, ok, &filePart{name: "big.bin", contentType: "application/octet-stream", data: big}, nil), 413, "PAYLOAD_TOO_LARGE")
	if e.blobs.puts.Load() != 0 {
		t.Fatalf("oversize upload reached blob store")
	}

	small := &filePart{name: "s.txt", contentType: "text/plain", data: []byte("ok")}
	wantStatus(t, upload(t, e, map[string][]string{"title": {"T"}}, small, nil), 400, "INVALID_BODY")
	wantStatus(t, upload(t, e, map[string][]string{"requirementIds": {reqs[0].ID}}, small, nil), 400, "INVALID_BODY")
	wantStatus(t, upload(t, e, map[string][]string{"title": {strings.Repeat("t", 201)}, "requirementIds": {reqs[0].ID}}, small, nil), 400, "INVALID_BODY")
	wantStatus(t, upload(t, e, ok, nil, nil), 400, "INVALID_BODY")
	wantStatus(t, upload(t, e, map[string][]string{"title": {"T"}, "requirementIds": {"missing"}}, small, nil), 404, "NOT_FOUND")
	wantStatus(t, upload(t, e, map[string][]string{"title": {"T"}, "requirementIds": {reqs[0].ID}, "validUntil": {"soon"}}, small, nil), 400, "INVALID_BODY")
	wantStatus(t, e.do(t, http.MethodPost, "/v1/evidence", strings.NewReader("{}"), map[string]string{"Content-Type": "application/json"}), 400, "INVALID_BODY")
	wantStatus(t, e.do(t, http.MethodPost, "/v1/evidence", strings.NewReader("{}"), map[string]string{"Content-Type": "text/plain"}), 400, "INVALID_BODY")
	// Multipart with both a file and a url is rejected.
	wantStatus(t, upload(t, e, map[string][]string{"title": {"T"}, "requirementIds": {reqs[0].ID}, "url": {"https://example.com/a"}}, small, nil), 400, "INVALID_BODY")
	if e.blobs.puts.Load() != 0 {
		t.Fatalf("invalid uploads reached blob store")
	}
}

func TestDashboard_Route(t *testing.T) {
	e := newEnv(t, nil)
	_, reqs := importAll(t, e)
	scope, err := e.store.CreateScopeCategory(context.Background(), "production service", "tester")
	if err != nil {
		t.Fatal(err)
	}
	setPart(t, e, reqs[0].ID, "ac-1_smt", `{"status":"implemented","scopeCategoryIds":["`+scope.ID+`"]}`)
	setPart(t, e, reqs[1].ID, "ac-1.1_smt", `{"dueDate":"2026-09-01"}`)
	r := e.get(t, "/v1/dashboard")
	wantStatus(t, r, 200, "")
	totals := r.body["totals"].(map[string]any)
	if totals["total"] != float64(2) || totals["implemented"] != float64(1) || totals["coveragePercent"] != float64(50) || totals["overdue"] != float64(1) {
		t.Fatalf("totals = %v", totals)
	}
	if totals["dueSoon"] != float64(0) {
		t.Fatalf("dueSoon = %v", totals["dueSoon"])
	}
	up, ok := r.body["upcomingRequirements"].([]any)
	if !ok || len(up) != 1 || len(r.body["frameworks"].([]any)) != 1 {
		t.Fatalf("dashboard = %s", r.raw)
	}
	row := up[0].(map[string]any)
	if row["id"] != reqs[1].ID || row["overdue"] != true || row["daysUntilDue"] != float64(-14) || row["frameworkShortName"] != "Example OSCAL catalog" {
		t.Fatalf("upcoming row = %v", row)
	}
	if _, has := row["parts"]; has {
		t.Fatalf("upcoming row must omit parts: %v", row)
	}
	for _, gone := range []string{"overdueRequirements", "expiringEvidence"} {
		if _, has := r.body[gone]; has {
			t.Fatalf("%s must be gone: %s", gone, r.raw)
		}
	}
	// A due date within 30 days counts as dueSoon and is listed after the overdue row.
	setPart(t, e, reqs[1].ID, "ac-1.1_smt", `{"dueDate":"2026-10-01"}`)
	r = e.get(t, "/v1/dashboard")
	totals = r.body["totals"].(map[string]any)
	if totals["overdue"] != float64(0) || totals["dueSoon"] != float64(1) {
		t.Fatalf("totals after reschedule = %v", totals)
	}
	if row := r.body["upcomingRequirements"].([]any)[0].(map[string]any); row["daysUntilDue"] != float64(16) || row["overdue"] != false {
		t.Fatalf("rescheduled row = %v", row)
	}
	setPart(t, e, reqs[1].ID, "ac-1.1_smt", `{"dueDate":"2026-09-01"}`)
	r = e.get(t, "/v1/dashboard")
	totals = r.body["totals"].(map[string]any)
	if len(r.body["recentActivity"].([]any)) != 6 {
		t.Fatalf("recentActivity = %v", r.body["recentActivity"])
	}
	by := totals["byStatus"].(map[string]any)
	for _, k := range []string{"implemented", "partial", "planned", "alternative", "not_applicable"} {
		if _, ok := by[k]; !ok {
			t.Fatalf("byStatus missing %q: %v", k, by)
		}
	}
	if _, old := by["not_started"]; old || by["planned"] != float64(1) || by["implemented"] != float64(1) {
		t.Fatalf("byStatus = %v", by)
	}
	if fw := r.body["frameworks"].([]any)[0].(map[string]any); fw["shortName"] != "Example OSCAL catalog" {
		t.Fatalf("framework shortName = %v", fw["shortName"])
	}
	// A status override counts as the effective status; the derived due date is kept.
	wantStatus(t, e.patchJSON(t, "/v1/requirements/"+reqs[1].ID, `{"statusOverride":"not_applicable"}`, nil), 200, "")
	r = e.get(t, "/v1/dashboard")
	totals = r.body["totals"].(map[string]any)
	if totals["applicable"] != float64(1) || totals["coveragePercent"] != float64(100) || totals["overdue"] != float64(0) || len(r.body["upcomingRequirements"].([]any)) != 0 {
		t.Fatalf("totals with override = %v", totals)
	}
	wantStatus(t, e.patchJSON(t, "/v1/requirements/"+reqs[1].ID, `{"statusOverride":null}`, nil), 200, "")
	r = e.get(t, "/v1/dashboard")
	if r.body["totals"].(map[string]any)["overdue"] != float64(1) {
		t.Fatalf("totals after clearing override = %v", r.body["totals"])
	}
	wantStatus(t, e.get(t, "/v1/dashboard?x=1"), 400, "INVALID_QUERY")
}

func TestInternal_PanicBecomes500(t *testing.T) {
	e := newEnv(t, panicRepo{})
	r := e.get(t, "/v1/oscal/documents")
	wantStatus(t, r, 500, "INTERNAL")
	if strings.Contains(string(r.raw), "boom") {
		t.Fatalf("panic text leaked: %s", r.raw)
	}
}
