package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// MockStorage is an in-memory implementation of Storage for tests.
type MockStorage struct {
	mu    sync.Mutex
	objects map[string][]byte // key -> data
}

// NewMockStorage creates an empty in-memory storage.
func NewMockStorage() *MockStorage {
	return &MockStorage{objects: make(map[string][]byte)}
}

// Objects returns a snapshot of stored objects.
func (m *MockStorage) Objects() map[string][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make(map[string][]byte, len(m.objects))
	for k, v := range m.objects {
		cp[k] = v
	}
	return cp
}

func (m *MockStorage) ListObjects(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var items []ObjectInfo
	for key, data := range m.objects {
		if strings.HasPrefix(key, prefix) {
			items = append(items, ObjectInfo{
				Key:          key,
				Size:         int64(len(data)),
				LastModified: time.Now(),
				ETag:         `"` + stringHash(key) + `"`,
			})
		}
	}
	return items, nil
}

func (m *MockStorage) GetObject(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, ok := m.objects[key]
	if !ok {
		return nil, 0, errors.New("not found")
	}
	return io.NopCloser(strings.NewReader(string(data))), int64(len(data)), nil
}

func (m *MockStorage) PutObject(ctx context.Context, key string, data io.Reader, size int64, contentType string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	b, err := io.ReadAll(data)
	if err != nil {
		return err
	}
	m.objects[key] = b
	return nil
}

// LocalFile materializes the object's bytes into a temp file and returns
// its path plus a cleanup that removes it. The caller MUST call cleanup
// when it is non-nil; the path is valid only until then.
func (m *MockStorage) LocalFile(ctx context.Context, key string) (string, func(), error) {
	m.mu.Lock()
	data, ok := m.objects[key]
	m.mu.Unlock()
	if !ok {
		return "", nil, errors.New("not found")
	}

	f, err := os.CreateTemp("", "sdcpp-mock-")
	if err != nil {
		return "", nil, fmt.Errorf("create temp file for %s: %w", key, err)
	}
	name := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(name)
		return "", nil, fmt.Errorf("write temp file for %s: %w", key, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", nil, fmt.Errorf("close temp file for %s: %w", key, err)
	}

	cleanup := func() { os.Remove(name) }
	return name, cleanup, nil
}

func (m *MockStorage) DeleteObject(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

func (m *MockStorage) PresignedURLsSupported() bool { return true }

func (m *MockStorage) PresignedGetObject(ctx context.Context, key string, expiry time.Duration) (string, error) {
	return "http://mock/presigned/" + key, nil
}

// stringHash is a trivial hash for generating mock ETags.
func stringHash(s string) string {
	h := 0
	for i := 0; i < len(s); i++ {
		h = h*31 + int(s[i])
	}
	return string(rune(h))
}
