package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func keyFor(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func exerciseStore(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	content := []byte("hello compliance " + time.Now().UTC().Format(time.RFC3339Nano))
	key := keyFor(content)

	ok, err := s.Exists(ctx, key)
	if err != nil || ok {
		t.Fatalf("Exists before put = %v, %v; want false, nil", ok, err)
	}
	if _, err := s.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get before put err = %v; want ErrNotFound", err)
	}

	if err := s.Put(ctx, key, bytes.NewReader(content), int64(len(content)), "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	ok, err = s.Exists(ctx, key)
	if err != nil || !ok {
		t.Fatalf("Exists after put = %v, %v; want true, nil", ok, err)
	}

	// Dedupe: second Put with different bytes under the same key is a no-op.
	if err := s.Put(ctx, key, strings.NewReader("DIFFERENT"), 9, "text/plain"); err != nil {
		t.Fatalf("Put duplicate: %v", err)
	}
	rc, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("Get content = %q; want %q", got, content)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if ok, err := s.Exists(ctx, key); err != nil || ok {
		t.Fatalf("Exists after delete = %v, %v; want false, nil", ok, err)
	}
	// Cleanup is best-effort, so deleting an absent content-addressed key is safe.
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("Delete absent: %v", err)
	}
}

func TestMemory(t *testing.T) {
	m := NewMemory()
	exerciseStore(t, m)
	if m.Len() != 0 {
		t.Fatalf("Len = %d; want 0 after delete", m.Len())
	}
	content := []byte("typed")
	key := keyFor(content)
	if err := m.Put(context.Background(), key, bytes.NewReader(content), int64(len(content)), "application/pdf"); err != nil {
		t.Fatal(err)
	}
	if ct := m.ContentType(key); ct != "application/pdf" {
		t.Fatalf("ContentType = %q; want application/pdf", ct)
	}
}

func TestS3(t *testing.T) {
	endpoint := os.Getenv("COMPLIANCE_BLOB_ENDPOINT")
	if endpoint == "" {
		t.Skip("COMPLIANCE_BLOB_ENDPOINT not set; skipping S3 integration test")
	}
	bucket := os.Getenv("COMPLIANCE_BLOB_BUCKET")
	if bucket == "" {
		bucket = "compliance-evidence-test"
	}
	region := os.Getenv("COMPLIANCE_BLOB_REGION")
	if region == "" {
		region = "us-east-1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := OpenS3(ctx, endpoint, os.Getenv("COMPLIANCE_BLOB_ACCESS_KEY"), os.Getenv("COMPLIANCE_BLOB_SECRET_KEY"), bucket, region, os.Getenv("COMPLIANCE_BLOB_USE_SSL") == "true")
	if err != nil {
		t.Fatalf("OpenS3: %v", err)
	}
	// Opening twice must be idempotent (bucket already exists).
	if _, err := OpenS3(ctx, endpoint, os.Getenv("COMPLIANCE_BLOB_ACCESS_KEY"), os.Getenv("COMPLIANCE_BLOB_SECRET_KEY"), bucket, region, os.Getenv("COMPLIANCE_BLOB_USE_SSL") == "true"); err != nil {
		t.Fatalf("OpenS3 second time: %v", err)
	}
	exerciseStore(t, s)
}
