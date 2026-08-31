package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultMemoryCapacity is the default size of the memory storage
// backend: 10 MB — enough to try everything, not enough to forget it's
// temporary.
const DefaultMemoryCapacity = 10 * 1024 * 1024

// ErrStorageFull is returned by the memory backend when a write would
// exceed its capacity. The device is considered full (like a real disk);
// there is no eviction or round-robin (see ideas/storage-improvements.md).
var ErrStorageFull = fmt.Errorf("storage full")

// MemoryStorage is an in-process, ephemeral StorageBackend. Everything
// lives in RAM and is lost on restart. It exists so the app can boot
// with no configuration at all (first run, containers without volumes,
// routers like OpenWrt) — zero external services, zero filesystem
// writes, zero credentials.
//
// The backend has a hard total-byte capacity: a PutObject that would
// exceed it fails with ErrStorageFull.
type MemoryStorage struct {
	mu        sync.RWMutex
	objects   map[string]memoryObject
	usedBytes int64
	capacity  int64
}

type memoryObject struct {
	data         []byte
	contentType  string
	lastModified time.Time
}

// NewMemoryStorage creates a memory backend with the given total-byte
// capacity.
func NewMemoryStorage(capacity int64) (*MemoryStorage, error) {
	if capacity <= 0 {
		return nil, fmt.Errorf("memory storage capacity must be positive, got %d", capacity)
	}
	return &MemoryStorage{
		objects:  map[string]memoryObject{},
		capacity: capacity,
	}, nil
}

// ParseCapacity parses a human-readable capacity such as "10MB",
// "512KB", "1GB", or a bare byte count ("10485760"). Suffixes are
// binary: 1 KB = 1024 bytes. Case-insensitive.
func ParseCapacity(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty capacity")
	}
	for _, unit := range []struct {
		suffix string
		mult   int64
	}{{"KB", 1024}, {"MB", 1024 * 1024}, {"GB", 1024 * 1024 * 1024}} {
		if strings.HasSuffix(strings.ToUpper(s), unit.suffix) {
			num := strings.TrimSpace(strings.TrimSuffix(strings.ToUpper(s), unit.suffix))
			n, err := strconv.ParseInt(num, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid capacity %q: %w", s, err)
			}
			if n <= 0 {
				return 0, fmt.Errorf("capacity must be positive, got %s", s)
			}
			return n * unit.mult, nil
		}
	}
	if strings.HasSuffix(strings.ToUpper(s), "B") {
		s = strings.TrimSuffix(strings.ToUpper(s), "B")
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid capacity %q (use a byte count or a KB/MB/GB suffix)", s)
	}
	if n <= 0 {
		return 0, fmt.Errorf("capacity must be positive, got %s", s)
	}
	return n, nil
}

// Used returns (usedBytes, capacityBytes).
func (m *MemoryStorage) Used() (int64, int64) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.usedBytes, m.capacity
}

// ListObjects returns all objects under the given prefix, sorted by key.
func (m *MemoryStorage) ListObjects(_ context.Context, prefix string) ([]ObjectInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ObjectInfo, 0, len(m.objects))
	for key, obj := range m.objects {
		if strings.HasPrefix(key, prefix) {
			out = append(out, ObjectInfo{
				Key:          key,
				Size:         int64(len(obj.data)),
				LastModified: obj.lastModified,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// GetObject retrieves an object by key.
func (m *MemoryStorage) GetObject(_ context.Context, key string) (io.ReadCloser, int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	obj, ok := m.objects[key]
	if !ok {
		return nil, 0, fmt.Errorf("object not found: %s", key)
	}
	return io.NopCloser(bytes.NewReader(obj.data)), int64(len(obj.data)), nil
}

// PutObject stores data at key. Fails with ErrStorageFull when the
// write would exceed the capacity (after accounting for an overwrite of
// the existing key, if any).
func (m *MemoryStorage) PutObject(_ context.Context, key string, data io.Reader, size int64, contentType string) error {
	buf, err := io.ReadAll(data)
	if err != nil {
		return fmt.Errorf("read upload: %w", err)
	}
	_ = size // authoritative size is the actual payload length
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.objects[key]; ok {
		m.usedBytes -= int64(len(old.data))
	}
	if m.usedBytes+int64(len(buf)) > m.capacity {
		return fmt.Errorf("%w: %d bytes used of %d, writing %d bytes would exceed capacity",
			ErrStorageFull, m.usedBytes, m.capacity, len(buf))
	}
	m.objects[key] = memoryObject{
		data:         buf,
		contentType:  contentType,
		lastModified: time.Now(),
	}
	m.usedBytes += int64(len(buf))
	return nil
}

// LocalFile materializes the object into a temp file (the memory
// backend has no real path), mirroring the S3 backend's behavior.
// The caller MUST call cleanup when it is non-nil.
func (m *MemoryStorage) LocalFile(ctx context.Context, key string) (string, func(), error) {
	body, _, err := m.GetObject(ctx, key)
	if err != nil {
		return "", nil, err
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		return "", nil, err
	}
	tmp, err := os.CreateTemp("", "seedwright-mem-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp file: %w", err)
	}
	path := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(path)
		return "", nil, fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(path)
		return "", nil, fmt.Errorf("close temp file: %w", err)
	}
	return path, func() { os.Remove(path) }, nil
}

// DeleteObject removes an object by key. Deleting a missing key is a no-op.
func (m *MemoryStorage) DeleteObject(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if obj, ok := m.objects[key]; ok {
		m.usedBytes -= int64(len(obj.data))
		delete(m.objects, key)
	}
	return nil
}

// PresignedURLsSupported reports false — clients use the server's serve
// endpoints (same as file storage).
func (m *MemoryStorage) PresignedURLsSupported() bool { return false }

// PresignedGetObject is not supported by the memory backend.
func (m *MemoryStorage) PresignedGetObject(_ context.Context, _ string, _ time.Duration) (string, error) {
	return "", ErrPresignedNotSupported
}
