package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/s1ns3nz0/compliance-ops/internal/httpapi"
)

func TestNamedAPITokensAuthenticateV1WhileHealthRemainsPublic(t *testing.T) {
	secondaryToken := strings.Repeat("s", 14)
	e := newEnv(t, nil, func(deps *httpapi.Deps) {
		deps.APITokens = []string{testToken, secondaryToken}
	})

	for name, token := range map[string]string{
		"first configured token":  testToken,
		"second configured token": secondaryToken,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/oscal/documents", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			if rec := newRecorder(e, req); rec.Code != http.StatusOK {
				t.Fatalf("configured token status = %d, want 200", rec.Code)
			}
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/oscal/documents", nil)
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 14))
	if rec := newRecorder(e, req); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown token status = %d, want 401", rec.Code)
	}

	if rec := newRecorder(e, httptest.NewRequest(http.MethodGet, "/healthz", nil)); rec.Code != http.StatusOK {
		t.Fatalf("public health status = %d, want 200", rec.Code)
	}
}
