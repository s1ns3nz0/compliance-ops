// Command server runs the Compliance Ops HTTP API and embedded UI.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/s1ns3nz0/compliance-ops/internal/blob"
	"github.com/s1ns3nz0/compliance-ops/internal/config"
	"github.com/s1ns3nz0/compliance-ops/internal/httpapi"
	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
	"github.com/s1ns3nz0/compliance-ops/internal/store"
	"github.com/s1ns3nz0/compliance-ops/internal/webui"
)

const shutdownTimeout = 10 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	cfg, err := config.FromEnv()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(2)
	}
	if err := run(context.Background(), cfg, logger); err != nil {
		logger.Error("server exited", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	deps := httpapi.Deps{
		Oscal:          oscal.NewHTTPRepository(cfg.OscalSourceURLs...),
		APITokens:      apiTokenValues(cfg.APITokens),
		AuditActor:     cfg.AuditActor,
		MaxUploadBytes: cfg.MaxUploadBytes,
		UI:             webui.Handler(),
		Now:            func() time.Time { return time.Now().UTC() },
	}

	if cfg.DatabaseURL != "" {
		st, err := store.OpenPostgres(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("open postgres: %w", err)
		}
		defer closeQuietly(st)
		deps.Store = st
	} else {
		logger.Warn("DATABASE_URL not set: tracking routes return 503 TRACKING_UNAVAILABLE")
	}

	if cfg.BlobEndpoint != "" {
		bs, err := blob.OpenS3(ctx, cfg.BlobEndpoint, cfg.BlobAccessKey, cfg.BlobSecretKey, cfg.BlobBucket, cfg.BlobRegion, cfg.BlobUseSSL)
		if err != nil {
			return fmt.Errorf("open blob storage: %w", err)
		}
		defer closeQuietly(bs)
		deps.Blobs = bs
	} else {
		logger.Warn("COMPLIANCE_BLOB_ENDPOINT not set: evidence upload/download return 503 TRACKING_UNAVAILABLE")
	}

	srv := newHTTPServer(cfg, httpapi.New(deps))

	errCh := make(chan error, 1)
	go func() {
		logger.Info("server listening",
			"port", cfg.Port,
			"oscalSourceCount", len(cfg.OscalSourceURLs),
			"oscalSourceHosts", hostsOf(cfg.OscalSourceURLs),
			"trackingEnabled", deps.Store != nil,
			"blobEnabled", deps.Blobs != nil,
			"maxUploadBytes", cfg.MaxUploadBytes,
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	return nil
}

func apiTokenValues(named map[string]string) []string {
	labels := make([]string, 0, len(named))
	for label := range named {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	values := make([]string, 0, len(labels))
	for _, label := range labels {
		values = append(values, named[label])
	}
	return values
}

func newHTTPServer(cfg config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              net.JoinHostPort("", strconv.Itoa(cfg.Port)),
		Handler:           handler,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
		BaseContext:       func(net.Listener) context.Context { return context.Background() },
	}
}

// closeQuietly closes v when it exposes a Close method of either common shape.
func closeQuietly(v any) {
	switch c := v.(type) {
	case interface{ Close() error }:
		_ = c.Close()
	case interface{ Close() }:
		c.Close()
	}
}

// hostsOf returns the distinct hosts of the configured sources (never paths
// or query strings, so nothing operator-specific reaches the logs).
func hostsOf(raws []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range raws {
		u, err := url.Parse(raw)
		if err != nil || seen[u.Host] {
			continue
		}
		seen[u.Host] = true
		out = append(out, u.Host)
	}
	return out
}
