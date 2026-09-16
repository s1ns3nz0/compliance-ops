package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/s1ns3nz0/compliance-ops/internal/oscal"
)

func TestMemoryOscalUploadLifecycle(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	raw := []byte(`{"catalog":{"uuid":"11111111-1111-1111-1111-111111111111","metadata":{"title":"Uploaded","version":"1.2"}}}`)
	docs, err := oscal.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	u, err := m.CreateOscalUpload(ctx, docs[0], raw, hex.EncodeToString(sum[:]), int64(len(raw)), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if u.Version != "1.2" || u.UploadedBy != "alice" || len(u.Document) != 0 {
		t.Fatalf("upload = %+v", u)
	}
	got, err := m.GetOscalUpload(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Document) != string(raw) {
		t.Fatalf("document = %s", got.Document)
	}
	if _, err := m.CreateOscalUpload(ctx, docs[0], raw, u.Sha256, int64(len(raw)), "bob"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate err = %v", err)
	}
	if _, err := m.ImportUploadedFramework(ctx, u.ID, oscal.Document{ID: "different", Type: "catalog", Title: "Different"}, "alice"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mismatched document err = %v", err)
	}
	if frameworks, _ := m.ListFrameworks(ctx); len(frameworks) != 0 {
		t.Fatalf("mismatched upload import created frameworks: %+v", frameworks)
	}
	_, err = m.ImportUploadedFramework(ctx, u.ID, docs[0], "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteOscalUpload(ctx, u.ID, "alice"); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete imported err = %v", err)
	}
}

func TestPostgresOscalUploadLifecycle(t *testing.T) {
	p, ctx := openTestPostgres(t)
	raw := []byte(`{"catalog":{"uuid":"aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb","metadata":{"title":"Postgres Upload","version":"2"},"controls":[{"id":"x-1","title":"Control"}]}}`)
	docs, err := oscal.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	u, err := p.CreateOscalUpload(ctx, docs[0], raw, hex.EncodeToString(sum[:]), int64(len(raw)), "alice")
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.GetOscalUpload(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	docs2, err := oscal.ParseJSON(got.Document)
	if err != nil || docs2[0].ID != docs[0].ID {
		t.Fatalf("roundtrip = %s, %v", got.Document, err)
	}
	if _, err := p.CreateOscalUpload(ctx, docs[0], raw, u.Sha256, int64(len(raw)), "bob"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate = %v", err)
	}
	_, err = p.ImportUploadedFramework(ctx, u.ID, docs[0], "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteOscalUpload(ctx, u.ID, "alice"); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete imported = %v", err)
	}
}

func TestImportUploadedFrameworkMissingUploadDoesNotCreateFramework(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store, ctx context.Context) {
		doc := oscal.Document{ID: "atomic-catalog", Type: "catalog", Title: "Atomic", Controls: []oscal.Control{{ID: "a-1", Title: "A"}}}
		if _, err := s.ImportUploadedFramework(ctx, "00000000-0000-0000-0000-000000000001", doc, "alice"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing upload err = %v", err)
		}
		frameworks, err := s.ListFrameworks(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(frameworks) != 0 {
			t.Fatalf("failed import created frameworks: %+v", frameworks)
		}
	})
}
