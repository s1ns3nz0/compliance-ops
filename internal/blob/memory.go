package blob

import (
	"bytes"
	"context"
	"io"
	"sync"
)

// Memory is an in-memory Store for tests.
type Memory struct {
	mu           sync.RWMutex
	data         map[string][]byte
	contentTypes map[string]string
}

// NewMemory returns an empty in-memory blob store.
func NewMemory() *Memory {
	return &Memory{data: map[string][]byte{}, contentTypes: map[string]string{}}
}

// Put implements Store. Existing keys are left untouched (content-addressed).
func (m *Memory) Put(_ context.Context, key string, r io.Reader, size int64, contentType string) error {
	m.mu.RLock()
	_, exists := m.data[key]
	m.mu.RUnlock()
	if exists {
		// Drain so callers relying on the reader being consumed behave the same as S3.
		_, _ = io.Copy(io.Discard, r)
		return nil
	}
	var buf bytes.Buffer
	if size > 0 {
		buf.Grow(int(size))
	}
	if _, err := io.Copy(&buf, r); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.data[key]; exists {
		return nil
	}
	m.data[key] = buf.Bytes()
	m.contentTypes[key] = contentType
	return nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.data[key]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// Exists implements Store.
func (m *Memory) Exists(_ context.Context, key string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.data[key]
	return ok, nil
}

// Delete implements Store. Missing keys are a no-op.
func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	delete(m.contentTypes, key)
	return nil
}

// ContentType returns the stored content type for key ("" when absent).
func (m *Memory) ContentType(key string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.contentTypes[key]
}

// Len returns the number of stored blobs.
func (m *Memory) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.data)
}
