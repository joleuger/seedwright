package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"time"
)

// --- MockStorage tests ---

func TestMockStorage_PutAndGet(t *testing.T) {
	m := NewMockStorage()
	data := []byte("hello world")
	ctx := context.Background()

	if err := m.PutObject(ctx, "test/file.txt", bytes.NewReader(data), int64(len(data)), "text/plain"); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	reader, size, err := m.GetObject(ctx, "test/file.txt")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer reader.Close()

	if size != int64(len(data)) {
		t.Errorf("size = %d, want %d", size, len(data))
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(body, data) {
		t.Errorf("body = %q, want %q", body, data)
	}
}

func TestMockStorage_ListObjects(t *testing.T) {
	m := NewMockStorage()
	ctx := context.Background()

	m.PutObject(ctx, "projects/default/meta.json", bytes.NewReader([]byte(`{}`)), 2, "application/json")
	m.PutObject(ctx, "projects/default/elements/abc.json", bytes.NewReader([]byte(`{"id":"abc"}`)), 11, "application/json")
	m.PutObject(ctx, "projects/default/images/abc.png", bytes.NewReader([]byte("PNG")), 3, "image/png")
	m.PutObject(ctx, "projects/other/meta.json", bytes.NewReader([]byte(`{}`)), 2, "application/json")

	// List all under projects/default/
	items, err := m.ListObjects(ctx, "projects/default/")
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(items) != 3 {
		t.Errorf("got %d items, want 3", len(items))
	}
	for _, item := range items {
		if item.Key == "" {
			t.Error("empty key in items")
		}
		if item.Size <= 0 {
			t.Errorf("key %s: size %d <= 0", item.Key, item.Size)
		}
	}

	// List only elements
	elemItems, err := m.ListObjects(ctx, "projects/default/elements/")
	if err != nil {
		t.Fatalf("ListObjects elements: %v", err)
	}
	if len(elemItems) != 1 {
		t.Errorf("got %d element items, want 1", len(elemItems))
	}

	// List prefix with no results
	empty, err := m.ListObjects(ctx, "projects/nonexistent/")
	if err != nil {
		t.Fatalf("ListObjects nonexistent: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("got %d items for nonexistent prefix, want 0", len(empty))
	}
}

func TestMockStorage_Delete(t *testing.T) {
	m := NewMockStorage()
	ctx := context.Background()

	m.PutObject(ctx, "file.txt", bytes.NewReader([]byte("data")), 4, "text/plain")
	if err := m.DeleteObject(ctx, "file.txt"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}

	_, _, err := m.GetObject(ctx, "file.txt")
	if err == nil {
		t.Fatal("GetObject after delete: expected error")
	}
}

func TestMockStorage_PresignedURL(t *testing.T) {
	m := NewMockStorage()

	if !m.PresignedURLsSupported() {
		t.Error("mock storage should report presigned URLs as supported")
	}

	url, err := m.PresignedGetObject(context.Background(), "images/test.png", 1*time.Hour)
	if err != nil {
		t.Fatalf("PresignedGetObject: %v", err)
	}
	if url != "http://mock/presigned/images/test.png" {
		t.Errorf("url = %q, want presigned URL", url)
	}
}

func TestMockStorage_GetMissing(t *testing.T) {
	m := NewMockStorage()
	ctx := context.Background()

	_, _, err := m.GetObject(ctx, "nonexistent.txt")
	if err == nil {
		t.Fatal("expected error for missing object")
	}
}

func TestMockStorage_LocalFile(t *testing.T) {
	m := NewMockStorage()
	ctx := context.Background()
	data := []byte("in-memory object bytes")

	if err := m.PutObject(ctx, "projects/test/images/elem-1.png", bytes.NewReader(data), int64(len(data)), "image/png"); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	path, cleanup, err := m.LocalFile(ctx, "projects/test/images/elem-1.png")
	if err != nil {
		t.Fatalf("LocalFile: %v", err)
	}
	if cleanup == nil {
		t.Fatal("cleanup = nil, want non-nil for mock storage")
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(body, data) {
		t.Errorf("body = %q, want %q", body, data)
	}

	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("path still exists after cleanup: %v", err)
	}
}

func TestMockStorage_LocalFile_Missing(t *testing.T) {
	m := NewMockStorage()
	ctx := context.Background()

	if _, _, err := m.LocalFile(ctx, "no/such/key.png"); err == nil {
		t.Fatal("LocalFile on missing key: expected error, got nil")
	}
}

// --- Seeker interface test (verifies S3Client buffering path) ---

func TestSeekerInterface(t *testing.T) {
	// bytes.NewReader implements io.Seeker — this is the fast path.
	var reader io.Reader = bytes.NewReader([]byte("data"))
	if _, ok := reader.(io.Seeker); !ok {
		t.Error("bytes.NewReader should implement io.Seeker")
	}

	// io.NopCloser does NOT implement io.Seeker — this triggers buffering in S3Client.PutObject.
	nopCloser := io.NopCloser(bytes.NewReader([]byte("data")))
	if _, ok := nopCloser.(io.Seeker); ok {
		t.Error("io.NopCloser should NOT implement io.Seeker (triggers buffering)")
	}
}

// --- S3Client interface satisfaction ---

var _ StorageBackend = (*MockStorage)(nil)
