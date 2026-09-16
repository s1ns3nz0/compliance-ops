package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/s1ns3nz0/compliance-ops/internal/store"
)

type scopeCategoryBody struct {
	Name *string `json:"name"`
}

type scopeCategoryPatchBody struct {
	Name              *string         `json:"name"`
	ExpectedUpdatedAt json.RawMessage `json:"expectedUpdatedAt"`
}

func parseScopeCategoryBody(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body scopeCategoryBody
	if err := decodeJSONBody(w, r, &body, maxJSONBody); err != nil || body.Name == nil {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "name is required")
		return "", false
	}
	name := strings.TrimSpace(*body.Name)
	if !store.ValidScopeCategoryName(name) {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "name must be 1..100 characters")
		return "", false
	}
	return name, true
}

func (a *api) listScopeCategories(w http.ResponseWriter, r *http.Request) {
	if !allowedQuery(r, "q", "limit") {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "unknown query parameter")
		return
	}
	limit, ok := intQuery(r, "limit", 100, 1, 200)
	if !ok {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "limit must be an integer in 1..200")
		return
	}
	items, err := a.deps.Store.ListScopeCategories(r.Context(), r.URL.Query().Get("q"), limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if items == nil {
		items = []store.ScopeCategory{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *api) createScopeCategory(w http.ResponseWriter, r *http.Request) {
	name, ok := parseScopeCategoryBody(w, r)
	if !ok {
		return
	}
	c, err := a.deps.Store.CreateScopeCategory(r.Context(), name, actor(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (a *api) patchScopeCategory(w http.ResponseWriter, r *http.Request) {
	var body scopeCategoryPatchBody
	if err := decodeJSONBody(w, r, &body, maxJSONBody); err != nil || body.Name == nil {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "name is required")
		return
	}
	name := strings.TrimSpace(*body.Name)
	if !store.ValidScopeCategoryName(name) {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "name must be 1..100 characters")
		return
	}
	expected, ok := parseUpdatedAtPrecondition(body.ExpectedUpdatedAt, false)
	if !ok {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "expectedUpdatedAt must be an RFC3339 string")
		return
	}
	c, err := a.deps.Store.UpdateScopeCategory(r.Context(), r.PathValue("id"), name, expected, actor(r))
	if err != nil {
		if errors.Is(err, store.ErrPreconditionFailed) {
			writeError(w, http.StatusConflict, codeConflict, "resource was modified")
			return
		}
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (a *api) deleteScopeCategory(w http.ResponseWriter, r *http.Request) {
	if !allowedQuery(r, "expectedUpdatedAt") {
		writeError(w, http.StatusBadRequest, codeInvalidQuery, "unknown query parameter")
		return
	}
	expected := store.UpdatedAtPrecondition{}
	if values, present := r.URL.Query()["expectedUpdatedAt"]; present {
		if len(values) != 1 {
			writeError(w, http.StatusBadRequest, codeInvalidQuery, "expectedUpdatedAt must appear once")
			return
		}
		parsed, err := time.Parse(time.RFC3339, values[0])
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidQuery, "expectedUpdatedAt must be an RFC3339 string")
			return
		}
		expected = store.UpdatedAtPrecondition{State: store.UpdatedAtValue, Value: parsed}
	}
	err := a.deps.Store.DeleteScopeCategory(r.Context(), r.PathValue("id"), expected, actor(r))
	if errors.Is(err, store.ErrPreconditionFailed) {
		writeError(w, http.StatusConflict, codeConflict, "resource was modified")
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, codeConflict, "scope category is in use")
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
