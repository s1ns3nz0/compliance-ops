// Package config reads runtime configuration from the environment and a
// protected named-token file.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxAPITokensFileBytes = 1 << 20
	maxAPITokens          = 100
	maxAPITokenLength     = 4096
)

// Config is the process configuration. Non-secret values come from environment
// variables; API tokens come from COMPLIANCE_API_TOKENS_FILE.
type Config struct {
	// Port the HTTP server listens on. Default 3000.
	Port int
	// OscalSourceURLs are the operator-configured, credential-free HTTPS OSCAL
	// sources (COMPLIANCE_OSCAL_SOURCE_URL, comma- or whitespace-separated,
	// deduplicated, at least one). Documents from every source are combined.
	OscalSourceURLs []string
	// APITokens maps operator-defined labels to bearer tokens required for /v1.
	// Values must be token68-safe and at least 12 characters; 43 or more
	// randomly generated characters are recommended for production. The source
	// file must have mode 0400, 0600, 0440, or 0640.
	APITokens map[string]string
	// AuditActor is the trusted server-configured identity recorded for mutations.
	AuditActor string
	// DatabaseURL is the PostgreSQL connection string. Optional: without it the
	// service runs in OSCAL-read-only mode and returns 503 for tracking routes.
	DatabaseURL string
	// Object storage for evidence blobs (S3 compatible).
	BlobEndpoint  string
	BlobBucket    string
	BlobAccessKey string
	BlobSecretKey string
	BlobUseSSL    bool
	BlobRegion    string
	// MaxUploadBytes bounds evidence uploads. Default 50 MiB.
	MaxUploadBytes int64
}

// Load reads configuration from the given lookup function (typically os.LookupEnv).
func Load(lookup func(string) (string, bool)) (Config, error) {
	get := func(key, def string) string {
		if v, ok := lookup(key); ok && v != "" {
			return v
		}
		return def
	}
	cfg := Config{
		Port:           3000,
		MaxUploadBytes: 50 << 20,
		BlobBucket:     get("COMPLIANCE_BLOB_BUCKET", "compliance-evidence"),
		BlobRegion:     get("COMPLIANCE_BLOB_REGION", "us-east-1"),
		BlobEndpoint:   get("COMPLIANCE_BLOB_ENDPOINT", ""),
		BlobAccessKey:  get("COMPLIANCE_BLOB_ACCESS_KEY", ""),
		BlobSecretKey:  get("COMPLIANCE_BLOB_SECRET_KEY", ""),
		DatabaseURL:    get("DATABASE_URL", ""),
		AuditActor:     strings.TrimSpace(get("COMPLIANCE_AUDIT_ACTOR", "operator")),
	}
	if v := get("PORT", ""); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			return cfg, fmt.Errorf("PORT must be an integer in 1..65535")
		}
		cfg.Port = p
	}
	if v := get("COMPLIANCE_MAX_UPLOAD_BYTES", ""); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			return cfg, fmt.Errorf("COMPLIANCE_MAX_UPLOAD_BYTES must be a positive integer")
		}
		cfg.MaxUploadBytes = n
	}
	cfg.BlobUseSSL = get("COMPLIANCE_BLOB_USE_SSL", "false") == "true"

	src, _ := lookup("COMPLIANCE_OSCAL_SOURCE_URL")
	urls, err := ParseSourceURLs(src)
	if err != nil {
		return cfg, err
	}
	cfg.OscalSourceURLs = urls

	if _, configured := lookup("COMPLIANCE_API_TOKEN"); configured {
		return cfg, errors.New("COMPLIANCE_API_TOKEN is no longer supported; use COMPLIANCE_API_TOKENS_FILE")
	}
	tokensPath := get("COMPLIANCE_API_TOKENS_FILE", "")
	if tokensPath == "" {
		return cfg, errors.New("COMPLIANCE_API_TOKENS_FILE is required")
	}
	cfg.APITokens, err = loadAPITokensFile(tokensPath)
	if err != nil {
		return cfg, err
	}
	if n := utf8.RuneCountInString(cfg.AuditActor); n < 1 || n > 200 {
		return cfg, errors.New("COMPLIANCE_AUDIT_ACTOR must be 1..200 characters after trimming")
	}
	return cfg, nil
}

func loadAPITokensFile(path string) (map[string]string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("COMPLIANCE_API_TOKENS_FILE must be a readable regular file")
	}
	permissions := info.Mode().Perm()
	if permissions != 0o400 && permissions != 0o600 && permissions != 0o440 && permissions != 0o640 {
		return nil, errors.New("COMPLIANCE_API_TOKENS_FILE permissions must be 0400, 0600, 0440, or 0640")
	}
	if info.Size() > maxAPITokensFileBytes {
		return nil, errors.New("COMPLIANCE_API_TOKENS_FILE must not exceed 1 MiB")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("COMPLIANCE_API_TOKENS_FILE cannot be read")
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	opening, err := dec.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, errors.New("COMPLIANCE_API_TOKENS_FILE must contain one JSON object")
	}
	tokens := make(map[string]string)
	seenValues := make(map[string]struct{})
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			return nil, errors.New("COMPLIANCE_API_TOKENS_FILE contains invalid JSON")
		}
		label, ok := keyToken.(string)
		if !ok {
			return nil, errors.New("COMPLIANCE_API_TOKENS_FILE contains an invalid label")
		}
		if _, exists := tokens[label]; exists {
			return nil, errors.New("COMPLIANCE_API_TOKENS_FILE contains a duplicate label")
		}
		if len(tokens) >= maxAPITokens {
			return nil, errors.New("COMPLIANCE_API_TOKENS_FILE must contain at most 100 tokens")
		}
		if strings.TrimSpace(label) == "" || utf8.RuneCountInString(label) > 100 {
			return nil, errors.New("COMPLIANCE_API_TOKENS_FILE labels must be nonblank and at most 100 characters")
		}
		var token string
		if err := dec.Decode(&token); err != nil || !validEncodedToken(token) {
			return nil, errors.New("COMPLIANCE_API_TOKENS_FILE token values must be token68-safe strings of 12..4096 characters")
		}
		if _, exists := seenValues[token]; exists {
			return nil, errors.New("COMPLIANCE_API_TOKENS_FILE token values must be unique")
		}
		tokens[label] = token
		seenValues[token] = struct{}{}
	}
	closing, err := dec.Token()
	if err != nil || closing != json.Delim('}') {
		return nil, errors.New("COMPLIANCE_API_TOKENS_FILE contains invalid JSON")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("COMPLIANCE_API_TOKENS_FILE must not contain trailing data")
	}
	if len(tokens) == 0 {
		return nil, errors.New("COMPLIANCE_API_TOKENS_FILE must contain at least one token")
	}
	return tokens, nil
}

// validEncodedToken accepts token68-safe strings with 12..4096 total characters
// and at least 12 non-padding characters. Operators should use 43 or more
// randomly generated characters for production tokens.
func validEncodedToken(token string) bool {
	if len(token) < 12 || len(token) > maxAPITokenLength {
		return false
	}
	encodedLength := 0
	padding := false
	for _, r := range token {
		if r == '=' {
			padding = true
			continue
		}
		if padding || !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && !strings.ContainsRune("-._~+/", r) {
			return false
		}
		encodedLength++
	}
	return encodedLength >= 12
}

func isSourceSeparator(r rune) bool { return r == ',' || unicode.IsSpace(r) }

// ParseSourceURLs splits a comma- or whitespace-separated list of OSCAL source
// URLs, validates each as a credential-free HTTPS URL without fragment, drops
// duplicates and preserves order. It errors when the list is empty.
func ParseSourceURLs(raw string) ([]string, error) {
	fields := strings.FieldsFunc(raw, isSourceSeparator)
	if len(fields) == 0 {
		return nil, errors.New("COMPLIANCE_OSCAL_SOURCE_URL is required")
	}
	out := make([]string, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	for _, f := range fields {
		u, err := url.Parse(f)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return nil, errors.New("COMPLIANCE_OSCAL_SOURCE_URL must be a comma-separated list of credential-free HTTPS URLs")
		}
		s := u.String()
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out, nil
}

// FromEnv loads configuration from the process environment.
func FromEnv() (Config, error) { return Load(os.LookupEnv) }
