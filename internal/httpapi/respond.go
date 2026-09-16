package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/s1ns3nz0/compliance-ops/internal/store"
)

// Error codes returned in the JSON error body.
const (
	codeInvalidQuery           = "INVALID_QUERY"
	codeInvalidBody            = "INVALID_BODY"
	codeUnauthorized           = "UNAUTHORIZED"
	codeNotFound               = "NOT_FOUND"
	codeMethodNotAllowed       = "METHOD_NOT_ALLOWED"
	codePayloadTooLarge        = "PAYLOAD_TOO_LARGE"
	codeOscalSourceUnavailable = "OSCAL_SOURCE_UNAVAILABLE"
	codeTrackingUnavailable    = "TRACKING_UNAVAILABLE"
	codeInternal               = "INTERNAL"
	codeConflict               = "CONFLICT"
)

// ErrorBody is the JSON error envelope.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorBody{Code: code, Message: message})
}

// writeStoreError maps store errors to HTTP responses. Unknown errors are
// logged and answered as 500 INTERNAL without the error text.
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "")
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusBadRequest, codeInvalidBody, "invalid value")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, codeConflict, "conflict")
	default:
		slog.Default().Error("store.error", "error", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "")
	}
}

// decodeJSONBody strictly decodes a single JSON object into dst.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("body must contain a single JSON object")
	}
	return nil
}

// allowedQuery reports whether the request query uses only the given keys.
func allowedQuery(r *http.Request, keys ...string) bool {
	for k := range r.URL.Query() {
		ok := false
		for _, allowed := range keys {
			if k == allowed {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// intQuery parses an integer query parameter with bounds; absent → def.
func intQuery(r *http.Request, key string, def, min, max int) (int, bool) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		if !r.URL.Query().Has(key) {
			return def, true
		}
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		return 0, false
	}
	return n, true
}

const dateLayout = "2006-01-02"

// parseDate parses YYYY-MM-DD as a UTC midnight instant.
func parseDate(s string) (time.Time, error) {
	return time.ParseInLocation(dateLayout, s, time.UTC)
}
