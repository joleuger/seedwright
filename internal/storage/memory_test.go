package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMemoryStorage_PutGetListDelete(t *testing.T) {
	store, err := NewMemoryStorage(1024)
	if err != nil {
		t.Fatalf("NewMemoryStorage: %v", err)
	}
	ctx := context.Background()

	if err := store.PutObject(ctx, "projects/a/elements/1.json", bytes.NewReader([]byte(`{"id":"1"}`)), 10, "application/json"); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if err := store.PutObject(ctx, "projects/a/other.txt", bytes.NewReader([]byte("hello")), 5, "text/plain"); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if err := store.PutObject(ctx, "unrelated.bin", bytes.NewReader([]byte("xyz")), 3, "application/octet-stream"); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	// GetObject round-trip.
	body, size, err := store.GetObject(ctx, "projects/a/other.txt")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	data, _ := io.ReadAll(body)
	body.Close()
	if size != 5 || string(data) != "hello" {
		t.Errorf("GetObject = (%q, %d), want (hello, 5)", data, size)
	}

	// Missing key errors.
	if _, _, err := store.GetObject(ctx, "nope"); err == nil {
		t.Error("GetObject(missing) should error")
	}

	// ListObjects filters by prefix and sorts by key.
	objs, err := store.ListObjects(ctx, "projects/")
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("ListObjects(projects/) = %d objects, want 2", len(objs))
	}
	if objs[0].Key != "projects/a/elements/1.json" || objs[1].Key != "projects/a/other.txt" {
		t.Errorf("unexpected order: %v", []string{objs[0].Key, objs[1].Key})
	}
	if objs[0].Size != 10 {
		t.Errorf("size = %d, want 10", objs[0].Size)
	}

	// DeleteObject frees capacity and removes the key.
	if err := store.DeleteObject(ctx, "projects/a/other.txt"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	if _, _, err := store.GetObject(ctx, "projects/a/other.txt"); err == nil {
		t.Error("deleted object should be gone")
	}
	// Deleting a missing key is a no-op.
	if err := store.DeleteObject(ctx, "projects/a/other.txt"); err != nil {
		t.Errorf("DeleteObject(missing) should be a no-op, got %v", err)
	}
}

func TestMemoryStorage_CapacityFull(t *testing.T) {
	store, _ := NewMemoryStorage(10)
	ctx := context.Background()

	if err := store.PutObject(ctx, "a", bytes.NewReader(bytes.Repeat([]byte("x"), 10)), 10, "text/plain"); err != nil {
		t.Fatalf("PutObject at capacity: %v", err)
	}
	used, cap := store.Used()
	if used != 10 || cap != 10 {
		t.Errorf("Used = (%d, %d), want (10, 10)", used, cap)
	}

	// Any further write fails with ErrStorageFull.
	err := store.PutObject(ctx, "b", bytes.NewReader([]byte("y")), 1, "text/plain")
	if err == nil {
		t.Fatal("PutObject beyond capacity should fail")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("storage full")) {
		t.Errorf("error should mention storage full: %v", err)
	}
	// The failed write did not corrupt accounting.
	used, _ = store.Used()
	if used != 10 {
		t.Errorf("used after failed put = %d, want 10", used)
	}
}

func TestMemoryStorage_OverwriteAccounting(t *testing.T) {
	store, _ := NewMemoryStorage(100)
	ctx := context.Background()

	if err := store.PutObject(ctx, "a", bytes.NewReader(bytes.Repeat([]byte("x"), 90)), 90, "text/plain"); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	// Overwriting with a smaller payload must free the old bytes:
	// 90 used, shrink to 10 → 10 used, still within 100.
	if err := store.PutObject(ctx, "a", bytes.NewReader([]byte("small")), 5, "text/plain"); err != nil {
		t.Fatalf("PutObject(overwrite): %v", err)
	}
	used, _ := store.Used()
	if used != 5 {
		t.Errorf("used after overwrite = %d, want 5", used)
	}
	// And a large write now succeeds because capacity was freed.
	if err := store.PutObject(ctx, "b", bytes.NewReader(bytes.Repeat([]byte("y"), 90)), 90, "text/plain"); err != nil {
		t.Fatalf("PutObject after freeing: %v", err)
	}
	used, _ = store.Used()
	if used != 95 {
		t.Errorf("used = %d, want 95", used)
	}
}

func TestMemoryStorage_LocalFile(t *testing.T) {
	store, _ := NewMemoryStorage(1024)
	ctx := context.Background()
	data := []byte("temp file payload")
	if err := store.PutObject(ctx, "img.png", bytes.NewReader(data), int64(len(data)), "image/png"); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	path, cleanup, err := store.LocalFile(ctx, "img.png")
	if err != nil {
		t.Fatalf("LocalFile: %v", err)
	}
	defer cleanup()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(body, data) {
		t.Errorf("LocalFile content = %q, want %q", body, data)
	}
	// Missing key errors before any temp file is promised.
	if _, _, err := store.LocalFile(ctx, "missing"); err == nil {
		t.Error("LocalFile(missing) should error")
	}
}

func TestMemoryStorage_PresignedUnsupported(t *testing.T) {
	store, _ := NewMemoryStorage(1024)
	if store.PresignedURLsSupported() {
		t.Error("memory storage must not support presigned URLs")
	}
	if _, err := store.PresignedGetObject(context.Background(), "k", 0); err != ErrPresignedNotSupported {
		t.Errorf("PresignedGetObject = %v, want ErrPresignedNotSupported", err)
	}
}

func TestParseCapacity(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"10MB", 10 * 1024 * 1024},
		{"10mb", 10 * 1024 * 1024},
		{"512KB", 512 * 1024},
		{"1GB", 1024 * 1024 * 1024},
		{"10485760", 10485760},
		{"2048B", 2048},
		{" 8mb ", 8 * 1024 * 1024},
	}
	for _, c := range cases {
		got, err := ParseCapacity(c.in)
		if err != nil {
			t.Errorf("ParseCapacity(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseCapacity(%q) = %d, want %d", c.in, got, c.want)
		}
	}

	for _, bad := range []string{"", "abc", "-5MB", "0MB", "1.5MB"} {
		if _, err := ParseCapacity(bad); err == nil {
			t.Errorf("ParseCapacity(%q) should error", bad)
		}
	}
}

func TestNewStorageBackend_Memory(t *testing.T) {
	node := yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "type"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "memory"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "capacity"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "1KB"},
		},
	}
	store, err := NewStorageBackend(node)
	if err != nil {
		t.Fatalf("NewStorageBackend(memory): %v", err)
	}
	mem, ok := store.(*MemoryStorage)
	if !ok {
		t.Fatalf("expected *MemoryStorage, got %T", store)
	}
	if _, cap := mem.Used(); cap != 1024 {
		t.Errorf("capacity = %d, want 1024", cap)
	}
}

func TestNewStorageBackend_MemoryDefaultCapacity(t *testing.T) {
	node := yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "type"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "memory"},
		},
	}
	store, err := NewStorageBackend(node)
	if err != nil {
		t.Fatalf("NewStorageBackend(memory): %v", err)
	}
	mem, ok := store.(*MemoryStorage)
	if !ok {
		t.Fatalf("expected *MemoryStorage, got %T", store)
	}
	if _, cap := mem.Used(); cap != DefaultMemoryCapacity {
		t.Errorf("capacity = %d, want default %d", cap, DefaultMemoryCapacity)
	}
}

func TestNewStorageBackend_MemoryBadCapacity(t *testing.T) {
	node := yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "type"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "memory"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "capacity"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "nope"},
		},
	}
	if _, err := NewStorageBackend(node); err == nil {
		t.Fatal("expected error for invalid capacity")
	}
}
