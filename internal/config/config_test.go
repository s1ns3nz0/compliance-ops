package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeTokensFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tokens.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadReadsNamedAPITokensFile(t *testing.T) {
	path := writeTokensFile(t, `{"primary":"`+strings.Repeat("a", 14)+`","secondary":"`+strings.Repeat("b", 14)+`"}`)
	cfg, err := loadMap(map[string]string{
		"COMPLIANCE_API_TOKENS_FILE":  path,
		"COMPLIANCE_OSCAL_SOURCE_URL": "https://example.com/catalog.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"primary":   strings.Repeat("a", 14),
		"secondary": strings.Repeat("b", 14),
	}
	if !reflect.DeepEqual(cfg.APITokens, want) {
		t.Fatal("APITokens labels or values were not loaded")
	}
}

func TestLoadAcceptsOneHundredCharacterAPITokenLabel(t *testing.T) {
	label := strings.Repeat("l", 100)
	path := writeTokensFile(t, `{"`+label+`":"`+strings.Repeat("v", 14)+`"}`)
	tokens, err := loadAPITokensFile(path)
	if err != nil {
		t.Fatalf("loadAPITokensFile rejected a 100-character label: %v", err)
	}
	if tokens[label] == "" {
		t.Fatal("100-character label was not loaded")
	}
}

func TestValidEncodedTokenEnforcesMinimumLength(t *testing.T) {
	if token := strings.Repeat("a", 14); !validEncodedToken(token) {
		t.Fatal("validEncodedToken rejected a token68-safe 14-character value")
	}
	if token := strings.Repeat("b", 11); validEncodedToken(token) {
		t.Fatal("validEncodedToken accepted an 11-character value")
	}
}

func validEnv(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"COMPLIANCE_API_TOKENS_FILE":  writeTokensFile(t, `{"test":"`+strings.Repeat("t", 14)+`"}`),
		"COMPLIANCE_OSCAL_SOURCE_URL": "https://example.com/catalog.json",
	}
}

func loadMap(values map[string]string) (Config, error) {
	return Load(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
}

func TestLoadAuditActorDefaultsAndTrims(t *testing.T) {
	values := validEnv(t)
	cfg, err := loadMap(values)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AuditActor != "operator" {
		t.Fatalf("AuditActor = %q, want operator", cfg.AuditActor)
	}

	values["COMPLIANCE_AUDIT_ACTOR"] = "  trusted-operator  "
	cfg, err = loadMap(values)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AuditActor != "trusted-operator" {
		t.Fatalf("AuditActor = %q, want trusted-operator", cfg.AuditActor)
	}
}

func TestLoadAuditActorRejectsInvalidLength(t *testing.T) {
	values := validEnv(t)
	values["COMPLIANCE_AUDIT_ACTOR"] = strings.Repeat("한", 200)
	if _, err := loadMap(values); err != nil {
		t.Fatalf("Load rejected 200-character audit actor: %v", err)
	}

	for _, actor := range []string{"   ", strings.Repeat("a", 201), strings.Repeat("한", 201)} {
		values := validEnv(t)
		values["COMPLIANCE_AUDIT_ACTOR"] = actor
		if _, err := loadMap(values); err == nil {
			t.Fatalf("Load accepted audit actor of trimmed length %d", len(strings.TrimSpace(actor)))
		}
	}
}

func TestLoadRejectsLegacyAPIToken(t *testing.T) {
	values := validEnv(t)
	legacy := strings.Repeat("z", 14)
	values["COMPLIANCE_API_TOKEN"] = legacy
	_, err := loadMap(values)
	if err == nil {
		t.Fatal("Load accepted legacy COMPLIANCE_API_TOKEN")
	}
	if strings.Contains(err.Error(), legacy) {
		t.Fatal("configuration error exposed a token value")
	}
}

func TestLoadAPITokensFilePermissionPolicy(t *testing.T) {
	validTokenJSON := `{"primary":"` + strings.Repeat("v", 14) + `"}`
	for _, tc := range []struct {
		name string
		mode os.FileMode
		want bool
	}{
		{name: "owner read only", mode: 0o400, want: true},
		{name: "owner read write", mode: 0o600, want: true},
		{name: "group read only", mode: 0o440, want: true},
		{name: "owner write and group read", mode: 0o640, want: true},
		{name: "group writable", mode: 0o460, want: false},
		{name: "group executable", mode: 0o450, want: false},
		{name: "other readable", mode: 0o444, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTokensFile(t, validTokenJSON)
			if err := os.Chmod(path, tc.mode); err != nil {
				t.Fatal(err)
			}
			_, err := loadAPITokensFile(path)
			if tc.want && err != nil {
				t.Fatalf("loadAPITokensFile rejected mode %04o: %v", tc.mode, err)
			}
			if !tc.want && err == nil {
				t.Fatalf("loadAPITokensFile accepted mode %04o", tc.mode)
			}
			if err != nil && strings.Contains(err.Error(), strings.Repeat("v", 14)) {
				t.Fatal("permission error exposed a token value")
			}
		})
	}
}

func TestLoadRejectsOversizedAPITokensFiles(t *testing.T) {
	const maxTokensFileBytes = 1 << 20
	for _, tc := range []struct {
		name  string
		write func(t *testing.T, path string)
	}{
		{
			name: "sparse",
			write: func(t *testing.T, path string) {
				t.Helper()
				if err := os.Truncate(path, maxTokensFileBytes+1); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "regular",
			write: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(strings.Repeat("x", maxTokensFileBytes+1)), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTokensFile(t, `{}`)
			tc.write(t, path)
			_, err := loadAPITokensFile(path)
			if err == nil || err.Error() != "COMPLIANCE_API_TOKENS_FILE must not exceed 1 MiB" {
				t.Fatalf("loadAPITokensFile error = %v, want secret-safe size error", err)
			}
		})
	}
}

func TestLoadRejectsMoreThanOneHundredAPITokens(t *testing.T) {
	tokens := make(map[string]string, 101)
	for i := 0; i < 100; i++ {
		tokens[fmt.Sprintf("label-%03d", i)] = fmt.Sprintf("token-value-%03d", i)
	}
	raw, err := json.Marshal(tokens)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loadAPITokensFile(writeTokensFile(t, string(raw))); err != nil {
		t.Fatalf("loadAPITokensFile rejected 100 token entries: %v", err)
	}
	tokens["label-100"] = "token-value-100"
	raw, err = json.Marshal(tokens)
	if err != nil {
		t.Fatal(err)
	}
	_, err = loadAPITokensFile(writeTokensFile(t, string(raw)))
	if err == nil || err.Error() != "COMPLIANCE_API_TOKENS_FILE must contain at most 100 tokens" {
		t.Fatalf("loadAPITokensFile error = %v, want secret-safe entry limit error", err)
	}
}

func TestLoadRejectsAPITokenValueLongerThan4096Bytes(t *testing.T) {
	maxLengthToken := strings.Repeat("m", 4096)
	if _, err := loadAPITokensFile(writeTokensFile(t, `{"primary":"`+maxLengthToken+`"}`)); err != nil {
		t.Fatalf("loadAPITokensFile rejected a 4096-byte token value: %v", err)
	}
	secret := strings.Repeat("s", 4097)
	path := writeTokensFile(t, `{"primary":"`+secret+`"}`)
	_, err := loadAPITokensFile(path)
	if err == nil || err.Error() != "COMPLIANCE_API_TOKENS_FILE token values must be token68-safe strings of 12..4096 characters" {
		t.Fatalf("loadAPITokensFile error = %v, want secret-safe token length error", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("configuration error exposed a token value")
	}
}

func TestLoadRejectsInvalidNamedAPITokenFiles(t *testing.T) {
	validToken := strings.Repeat("v", 14)
	tests := map[string]string{
		"empty set":          `{}`,
		"empty token":        `{"primary":""}`,
		"blank label":        `{"   ":"` + validToken + `"}`,
		"label too long":     `{"` + strings.Repeat("l", 101) + `":"` + validToken + `"}`,
		"short token":        `{"primary":"too-short"}`,
		"padding only":       `{"primary":"` + strings.Repeat("=", 14) + `"}`,
		"short with padding": `{"primary":"` + strings.Repeat("a", 11) + `="}`,
		"duplicate token":    `{"primary":"` + validToken + `","secondary":"` + validToken + `"}`,
		"duplicate label":    `{"primary":"` + validToken + `","primary":"` + strings.Repeat("w", 14) + `"}`,
		"trailing data":      `{"primary":"` + validToken + `"} true`,
		"non-string token":   `{"primary":42}`,
	}
	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			values := validEnv(t)
			values["COMPLIANCE_API_TOKENS_FILE"] = writeTokensFile(t, contents)
			if _, err := loadMap(values); err == nil {
				t.Fatal("Load accepted invalid token configuration")
			}
		})
	}

	t.Run("insecure permissions", func(t *testing.T) {
		values := validEnv(t)
		path := writeTokensFile(t, `{"primary":"`+validToken+`"}`)
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		values["COMPLIANCE_API_TOKENS_FILE"] = path
		if _, err := loadMap(values); err == nil {
			t.Fatal("Load accepted token file with unsupported permissions")
		}
	})
}
