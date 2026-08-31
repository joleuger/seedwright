package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStorage_PutAndGet(t *testing.T) {
	dir := t.TempDir()
	fs := &FileStorage{basePath: dir}
	ctx := context.Background()

	data := []byte("hello file storage")
	err := fs.PutObject(ctx, "projects/test/elements/abc.json", bytes.NewReader(data), int64(len(data)), "application/json")
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	reader, size, err := fs.GetObject(ctx, "projects/test/elements/abc.json")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer reader.Close()

	if size != int64(len(data)) {
		t.Errorf("size = %d, want %d", size, len(data))
	}

	body, err := os.ReadFile(filepath.Join(dir, "projects", "test", "elements", "abc.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(body, data) {
		t.Errorf("body = %q, want %q", body, data)
	}
}

func TestFileStorage_ListObjects(t *testing.T) {
	dir := t.TempDir()
	fs := &FileStorage{basePath: dir}
	ctx := context.Background()

	fs.PutObject(ctx, "projects/default/elements/a.json", bytes.NewReader([]byte(`{}`)), 2, "application/json")
	fs.PutObject(ctx, "projects/default/elements/b.json", bytes.NewReader([]byte(`{}`)), 2, "application/json")
	fs.PutObject(ctx, "projects/default/images/a.png", bytes.NewReader([]byte("PNG")), 3, "image/png")
	fs.PutObject(ctx, "projects/other/elements/c.json", bytes.NewReader([]byte(`{}`)), 2, "application/json")

	items, err := fs.ListObjects(ctx, "projects/default/")
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(items) != 3 {
		t.Errorf("got %d items, want 3", len(items))
	}

	elemItems, err := fs.ListObjects(ctx, "projects/default/elements/")
	if err != nil {
		t.Fatalf("ListObjects elements: %v", err)
	}
	if len(elemItems) != 2 {
		t.Errorf("got %d element items, want 2", len(elemItems))
	}
}

func TestFileStorage_Delete(t *testing.T) {
	dir := t.TempDir()
	fs := &FileStorage{basePath: dir}
	ctx := context.Background()

	fs.PutObject(ctx, "projects/test/file.json", bytes.NewReader([]byte("data")), 4, "application/json")
	if err := fs.DeleteObject(ctx, "projects/test/file.json"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}

	_, _, err := fs.GetObject(ctx, "projects/test/file.json")
	if err == nil {
		t.Fatal("GetObject after delete: expected error")
	}
}

func TestFileStorage_PresignedNotSupported(t *testing.T) {
	dir := t.TempDir()
	fs := &FileStorage{basePath: dir}

	if fs.PresignedURLsSupported() {
		t.Error("file storage should not support presigned URLs")
	}

	_, err := fs.PresignedGetObject(context.Background(), "test.png", 1)
	if err == nil {
		t.Fatal("expected error from PresignedGetObject on file storage")
	}
	if !errors.Is(err, ErrPresignedNotSupported) {
		t.Fatalf("expected ErrPresignedNotSupported, got %v", err)
	}
}

func TestFileStorage_GetMissing(t *testing.T) {
	dir := t.TempDir()
	fs := &FileStorage{basePath: dir}
	ctx := context.Background()

	_, _, err := fs.GetObject(ctx, "nonexistent.json")
	if err == nil {
		t.Fatal("expected error for missing object")
	}
}

func TestFileStorage_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	fs := &FileStorage{basePath: dir}
	ctx := context.Background()

	// Attempt directory traversal.
	_, _, err := fs.GetObject(ctx, "../../../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestFileStorage_LocalFile(t *testing.T) {
	dir := t.TempDir()
	fs := &FileStorage{basePath: dir}
	ctx := context.Background()

	data := []byte("real on-disk object")
	err := fs.PutObject(ctx, "projects/test/images/elem-1.png", bytes.NewReader(data), int64(len(data)), "image/png")
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	path, cleanup, err := fs.LocalFile(ctx, "projects/test/images/elem-1.png")
	if err != nil {
		t.Fatalf("LocalFile: %v", err)
	}
	// File storage is zero-copy: the real path, nothing to clean up.
	if cleanup != nil {
		t.Error("cleanup = non-nil, want nil for file storage")
	}
	want := filepath.Join(dir, "projects", "test", "images", "elem-1.png")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(body, data) {
		t.Errorf("body = %q, want %q", body, data)
	}
}

func TestFileStorage_LocalFile_Missing(t *testing.T) {
	dir := t.TempDir()
	fs := &FileStorage{basePath: dir}

	if _, _, err := fs.LocalFile(context.Background(), "no/such/key.png"); err == nil {
		t.Fatal("LocalFile on missing key: expected error, got nil")
	}
}

func TestFileStorage_LocalFile_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	fs := &FileStorage{basePath: dir}

	if _, _, err := fs.LocalFile(context.Background(), "../outside.png"); err == nil {
		t.Fatal("LocalFile with traversal key: expected error, got nil")
	}
}

func TestFileStorage_AutoCreatesDir(t *testing.T) {
	base := filepath.Join(t.TempDir(), "nested", "deep")
	fs, err := NewFileStorage(base)
	if err != nil {
		t.Fatalf("NewFileStorage: %v", err)
	}

	ctx := context.Background()
	if err := fs.PutObject(ctx, "file.txt", bytes.NewReader([]byte("data")), 4, "text/plain"); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	if _, err := os.Stat(filepath.Join(base, "file.txt")); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}
