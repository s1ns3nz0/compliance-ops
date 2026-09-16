// Package httpapi exposes the Compliance Ops HTTP API: the legacy read-only
// OSCAL view plus framework, requirement, evidence, dashboard, and audit
// routes. Routing uses the standard library ServeMux (Go 1.22+ method and
// pattern syntax); no third-party router is used.
package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/s1ns3nz0/compliance-ops/internal/blob"
	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
	"github.com/s1ns3nz0/compliance-ops/internal/store"
)

// Deps wires the API to its collaborators.
type Deps struct {
	// Oscal is the read-only OSCAL document source (required).
	Oscal oscal.Repository
	// Store is the tracking persistence. May be nil: tracking routes then
	// answer 503 TRACKING_UNAVAILABLE.
	Store store.Store
	// Blobs holds evidence files. May be nil: evidence routes then answer 503.
	Blobs blob.Store
	// APITokens contains the bearer tokens accepted on every /v1 route.
	APITokens []string
	// AuditActor is the trusted server-configured identity used for audit rows.
	// Caller-supplied X-Actor headers are accepted for compatibility but ignored.
	AuditActor string
	// MaxUploadBytes bounds a multipart evidence upload (whole request body).
	MaxUploadBytes int64
	// UI serves everything that is neither /healthz nor /v1. May be nil (404).
	UI http.Handler
	// Now supplies the clock; defaults to time.Now in UTC.
	Now func() time.Time
}

const (
	defaultMaxUploadBytes = 50 << 20
	defaultAuditActor     = "operator"
	contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"
)

type auditActorContextKey struct{}

type api struct {
	deps   Deps
	logger *slog.Logger
}

// New builds the HTTP handler. Middleware order (outermost first): recover,
// logging, then routing; /v1 additionally passes through the bearer check.
func New(deps Deps) http.Handler {
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	if deps.MaxUploadBytes <= 0 {
		deps.MaxUploadBytes = defaultMaxUploadBytes
	}
	if deps.AuditActor == "" {
		deps.AuditActor = defaultAuditActor
	}
	a := &api{deps: deps, logger: slog.Default()}

	v1 := &router{mux: http.NewServeMux()}
	// Legacy read-only OSCAL view.
	v1.handle(http.MethodGet, "/v1/oscal/documents", a.listOscalDocuments)
	v1.handle(http.MethodGet, "/v1/oscal/sources", a.listOscalSources)
	v1.handle(http.MethodPost, "/v1/oscal/uploads", a.requireStore(a.createOscalUpload))
	v1.handle(http.MethodGet, "/v1/oscal/uploads", a.requireStore(a.listOscalUploads))
	v1.handle(http.MethodGet, "/v1/oscal/uploads/{id}", a.requireStore(a.getOscalUpload))
	v1.handle(http.MethodDelete, "/v1/oscal/uploads/{id}", a.requireStore(a.deleteOscalUpload))
	v1.handle(http.MethodGet, "/v1/oscal/documents/{documentId}", a.getOscalDocument)
	v1.handle(http.MethodGet, "/v1/oscal/documents/{documentId}/controls", a.listOscalControls)
	v1.handle(http.MethodGet, "/v1/oscal/documents/{documentId}/controls/{controlId}", a.getOscalControl)
	// Tracking.
	v1.handle(http.MethodPost, "/v1/frameworks/import", a.requireStore(a.importFrameworks))
	v1.handle(http.MethodGet, "/v1/frameworks", a.requireStore(a.listFrameworks))
	v1.handle(http.MethodGet, "/v1/frameworks/{id}", a.requireStore(a.getFramework))
	v1.handle(http.MethodPatch, "/v1/frameworks/{id}", a.requireStore(a.patchFramework))
	v1.handle(http.MethodGet, "/v1/scope-categories", a.requireStore(a.listScopeCategories))
	v1.handle(http.MethodPost, "/v1/scope-categories", a.requireStore(a.createScopeCategory))
	v1.handle(http.MethodPatch, "/v1/scope-categories/{id}", a.requireStore(a.patchScopeCategory))
	v1.handle(http.MethodDelete, "/v1/scope-categories/{id}", a.requireStore(a.deleteScopeCategory))
	v1.handle(http.MethodGet, "/v1/requirements", a.requireStore(a.listRequirements))
	v1.handle(http.MethodGet, "/v1/requirements/{id}", a.requireStore(a.getRequirement))
	v1.handle(http.MethodPatch, "/v1/requirements/{id}", a.requireStore(a.patchRequirement))
	v1.handle(http.MethodPatch, "/v1/requirements/{id}/parts/{partId}", a.requireStore(a.patchPart))
	v1.handle(http.MethodGet, "/v1/requirements/{id}/assessments", a.requireStore(a.listRequirementAssessments))
	v1.handle(http.MethodGet, "/v1/assessments", a.requireStore(a.listAssessments))
	v1.handle(http.MethodGet, "/v1/assessments/{uploadId}", a.requireStore(a.getAssessment))
	v1.handle(http.MethodPatch, "/v1/assessments/{uploadId}/framework", a.requireStore(a.patchAssessmentFramework))
	v1.handle(http.MethodPost, "/v1/assessments/{uploadId}/mappings", a.requireStore(a.createAssessmentMapping))
	v1.handle(http.MethodDelete, "/v1/assessments/{uploadId}/mappings/{itemKey}", a.requireStore(a.deleteAssessmentMapping))
	v1.handle(http.MethodPost, "/v1/evidence", a.requireTracking(a.createEvidence))
	v1.handle(http.MethodGet, "/v1/evidence", a.requireStore(a.listEvidence))
	v1.handle(http.MethodGet, "/v1/evidence/{id}", a.requireStore(a.getEvidence))
	v1.handle(http.MethodGet, "/v1/evidence/{id}/download", a.requireTracking(a.downloadEvidence))
	v1.handle(http.MethodGet, "/v1/dashboard", a.requireStore(a.getDashboard))
	v1.handle(http.MethodGet, "/v1/audit", a.requireStore(a.listAudit))
	v1.finish()

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	root.HandleFunc("/healthz", methodNotAllowed)
	root.Handle("/v1/", a.authenticate(v1.mux))
	if deps.UI != nil {
		root.Handle("/", deps.UI)
	} else {
		root.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, codeNotFound, "")
		})
	}
	return a.securityHeaders(a.recoverer(a.logging(root)))
}

// router registers method-qualified patterns and, once finished, explicit
// fallbacks for the remaining common methods on every pattern so unsupported
// methods get a JSON 405 instead of the ServeMux plain-text default.
// (Method-less fallbacks would conflict with sibling literal/wildcard
// patterns such as /frameworks/import vs /frameworks/{id}.)
type router struct {
	mux      *http.ServeMux
	patterns []string
	methods  map[string]map[string]bool
}

var fallbackMethods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

func (rt *router) handle(method, pattern string, h http.HandlerFunc) {
	rt.mux.HandleFunc(method+" "+pattern, h)
	if rt.methods == nil {
		rt.methods = map[string]map[string]bool{}
	}
	if rt.methods[pattern] == nil {
		rt.methods[pattern] = map[string]bool{}
		rt.patterns = append(rt.patterns, pattern)
	}
	rt.methods[pattern][method] = true
}

func (rt *router) finish() {
	for _, p := range rt.patterns {
		allow := allowHeader(rt.methods[p])
		for _, m := range fallbackMethods {
			if !rt.methods[p][m] {
				rt.mux.HandleFunc(m+" "+p, methodNotAllowedWith(allow))
			}
		}
	}
	rt.mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, codeNotFound, "")
	})
}

// allowHeader renders the registered methods of a pattern as an RFC 9110
// Allow header value (sorted, comma-separated; GET implies HEAD).
func allowHeader(methods map[string]bool) string {
	list := make([]string, 0, len(methods)+1)
	for m := range methods {
		list = append(list, m)
	}
	if methods[http.MethodGet] && !methods[http.MethodHead] {
		list = append(list, http.MethodHead)
	}
	sort.Strings(list)
	return strings.Join(list, ", ")
}

// methodNotAllowedWith answers 405 with the mandatory Allow header.
func methodNotAllowedWith(allow string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Allow", allow)
		writeError(w, http.StatusMethodNotAllowed, codeMethodNotAllowed, "")
	}
}

// methodNotAllowed is the 405 handler for /healthz (GET/HEAD only).
func methodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Allow", "GET, HEAD")
	writeError(w, http.StatusMethodNotAllowed, codeMethodNotAllowed, "")
}

// authenticate enforces a configured bearer token on /v1 and sets the response
// hardening headers for JSON routes.
func (a *api) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		if !a.tokenValid(r.Header.Get("Authorization")) {
			h.Set("WWW-Authenticate", `Bearer realm="compliance-ops"`)
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "")
			return
		}
		ctx := context.WithValue(r.Context(), auditActorContextKey{}, a.deps.AuditActor)
		trustedRequest := r.WithContext(ctx)
		next.ServeHTTP(w, trustedRequest)
		// Preserve the matched pattern on the request received by outer middleware.
		r.Pattern = trustedRequest.Pattern
	})
}

func (a *api) tokenValid(header string) bool {
	if len(a.deps.APITokens) == 0 {
		return false
	}
	const prefix = "bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	presented := strings.TrimSpace(header[len(prefix):])
	presentedHash := sha256.Sum256([]byte(presented))
	valid := 0
	for _, configured := range a.deps.APITokens {
		configuredHash := sha256.Sum256([]byte(configured))
		valid |= subtle.ConstantTimeCompare(presentedHash[:], configuredHash[:])
	}
	return valid == 1
}

// requireStore answers 503 when no tracking store is configured.
func (a *api) requireStore(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.deps.Store == nil {
			writeError(w, http.StatusServiceUnavailable, codeTrackingUnavailable, "tracking store is not configured")
			return
		}
		h(w, r)
	}
}

// requireTracking answers 503 when either the store or the blob store is missing.
func (a *api) requireTracking(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.deps.Store == nil || a.deps.Blobs == nil {
			writeError(w, http.StatusServiceUnavailable, codeTrackingUnavailable, "evidence storage is not configured")
			return
		}
		h(w, r)
	}
}

// actor returns the trusted server-configured audit identity. X-Actor remains
// accepted for backwards compatibility but has no authority and is ignored.
func actor(r *http.Request) string {
	if trusted, ok := r.Context().Value(auditActorContextKey{}).(string); ok && trusted != "" {
		return trusted
	}
	return defaultAuditActor
}

// securityHeaders applies browser hardening to every UI, API, health, error,
// and panic response before any inner handler can write a status or body.
func (a *api) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the status code for logging and the recoverer.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wrote {
		s.status = http.StatusOK
		s.wrote = true
	}
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// RouteLabel returns a safe label for logs/metrics: the matched route pattern
// when known, otherwise a placeholder. It never contains query strings or
// path values.
func RouteLabel(r *http.Request) string {
	if r.Pattern != "" {
		return r.Pattern
	}
	return "unmatched"
}

// logging records method, matched route pattern, status, and duration only.
func (a *api) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		a.logger.Info("http.request",
			"method", r.Method,
			"route", RouteLabel(r),
			"status", rec.status,
			"durationMs", time.Since(start).Milliseconds(),
		)
	})
}

// recoverer converts panics into a 500 INTERNAL response without leaking details.
func (a *api) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec, ok := w.(*statusRecorder)
		if !ok {
			rec = &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			w = rec
		}
		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler {
					panic(p)
				}
				a.logger.Error("http.panic", "route", RouteLabel(r), "panic", p)
				if !rec.wrote {
					writeError(rec, http.StatusInternalServerError, codeInternal, "")
				}
			}
		}()
		next.ServeHTTP(w, r)
	})
}
