package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/s1ns3nz0/compliance-ops/internal/config"
)

func TestAPITokenValuesPassOnlyConfiguredValuesToHTTPAPI(t *testing.T) {
	got := apiTokenValues(map[string]string{"label-b": "value-b", "label-a": "value-a"})
	if len(got) != 2 || got[0] != "value-a" || got[1] != "value-b" {
		t.Fatalf("apiTokenValues = %v, want configured values in label order", got)
	}
}

func TestNewHTTPServerHardeningAndUploadTimeouts(t *testing.T) {
	srv := newHTTPServer(config.Config{Port: 3456}, http.NotFoundHandler())
	if srv.ReadTimeout != 60*time.Second || srv.WriteTimeout != 120*time.Second {
		t.Fatalf("timeouts = read %s write %s", srv.ReadTimeout, srv.WriteTimeout)
	}
	if srv.MaxHeaderBytes != 1<<20 {
		t.Fatalf("MaxHeaderBytes = %d, want %d", srv.MaxHeaderBytes, 1<<20)
	}
}
