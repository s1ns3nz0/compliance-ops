// Package blob defines the content-addressed object storage contract for
// evidence files. Keys are lowercase hex SHA-256 digests of the content.
package blob

import (
	"context"
	"errors"
	"io"
)

// ErrNotFound is returned by Get when no blob exists under the key.
var ErrNotFound = errors.New("blob not found")

// Store persists immutable blobs keyed by content digest.
type Store interface {
	// Put stores the blob under key. Storing an existing key is a no-op success.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	// Get opens the blob for reading. Returns ErrNotFound when absent.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Exists reports whether key is stored.
	Exists(ctx context.Context, key string) (bool, error)
	// Delete removes key. Missing keys are a no-op. It is used for best-effort
	// compensation when newly stored content-addressed bytes cannot be linked.
	Delete(ctx context.Context, key string) error
}
