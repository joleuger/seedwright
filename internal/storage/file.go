package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"time"
)

// FileStorage is a local filesystem implementation of StorageBackend.
// S3 keys (forward-slash separated) map to subdirectories under a base path.
//
// Example mapping: "projects/default/elements/abc.json" →
//
//	{basePath}/projects/default/elements/abc.json
type FileStorage struct {
	basePath string
}

// NewFileStorage creates a FileStorage that writes to baseDir.
// The directory is created (or verified to exist) on construction.
func NewFileStorage(baseDir string) (*FileStorage, error) {
	info, err := os.Stat(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(baseDir, 0o755); err != nil {
				return nil, fmt.Errorf("create storage directory %s: %w", baseDir, err)
			}
		} else {
			return nil, fmt.Errorf("stat storage directory %s: %w", baseDir, err)
		}
	} else if !info.IsDir() {
		return nil, fmt.Errorf("storage path %s is not a directory", baseDir)
	}

	return &FileStorage{
		basePath: baseDir,
	}, nil
}

// sanitizeKey cleans an S3 key and ensures the resolved path stays
// under basePath (prevents directory traversal).
func (f *FileStorage) sanitizeKey(key string) (string, error) {
	cleaned := filepath.Join(f.basePath, key)
	absBase, err := filepath.Abs(f.basePath)
	if err != nil {
		return "", fmt.Errorf("resolve base path: %w", err)
	}
	absKey, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("resolve key path: %w", err)
	}
	// Ensure the key resolves inside basePath.
	if !hasPrefix(absKey, absBase) {
		return "", fmt.Errorf("key %q escapes storage directory", key)
	}
	return absKey, nil
}

// hasPrefix reports whether path starts with prefix (both absolute,
// normalized paths). We append a separator to avoid partial-prefix
// matches (e.g., "/data" matching "/datanew").
func hasPrefix(path, prefix string) bool {
	return path == prefix || len(path) > len(prefix) && path[len(prefix)] == os.PathSeparator && path[:len(prefix)] == prefix
}

func (f *FileStorage) ListObjects(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	target, err := f.sanitizeKey(prefix)
	if err != nil {
		return nil, err
	}

	var items []ObjectInfo
	err = filepath.Walk(target, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if info.IsDir() {
			return nil
		}
		// Convert back to S3-style key (relative, forward slashes).
		rel, err := filepath.Rel(f.basePath, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		items = append(items, ObjectInfo{
			Key:          rel,
			Size:         info.Size(),
			LastModified: info.ModTime(),
		})
		return nil
	})

	return items, err
}

func (f *FileStorage) GetObject(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	path, err := f.sanitizeKey(key)
	if err != nil {
		return nil, 0, err
	}

	finfo, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, errors.New("not found")
		}
		return nil, 0, fmt.Errorf("stat %s: %w", key, err)
	}

	fh, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("open %s: %w", key, err)
	}

	return fh, finfo.Size(), nil
}

func (f *FileStorage) PutObject(ctx context.Context, key string, data io.Reader, size int64, contentType string) error {
	path, err := f.sanitizeKey(key)
	if err != nil {
		return err
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", key, err)
	}

	out, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", key, err)
	}
	defer out.Close()

	_, err = io.Copy(out, data)
	if err != nil {
		return fmt.Errorf("write %s: %w", key, err)
	}

	return nil
}

func (f *FileStorage) DeleteObject(ctx context.Context, key string) error {
	path, err := f.sanitizeKey(key)
	if err != nil {
		return err
	}

	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return errors.New("not found")
		}
		return fmt.Errorf("delete %s: %w", key, err)
	}

	// Clean up empty parent directories (walk up to basePath).
	dir := filepath.Dir(path)
	baseAbs, _ := filepath.Abs(f.basePath)
	for dir != baseAbs && dir != "." {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			break // stop at first non-empty dir or basePath
		}
		if err := os.Remove(dir); err != nil {
			break
		}
		dir = filepath.Dir(dir)
	}

	return nil
}

// LocalFile returns the object's real filesystem path and a nil cleanup:
// the object already lives on local disk, so this is a zero-copy no-op.
// The path stays valid indefinitely (nothing to clean up).
func (f *FileStorage) LocalFile(ctx context.Context, key string) (string, func(), error) {
	path, err := f.sanitizeKey(key)
	if err != nil {
		return "", nil, err
	}

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", nil, errors.New("not found")
		}
		return "", nil, fmt.Errorf("stat %s: %w", key, err)
	}

	return path, nil, nil
}

func (f *FileStorage) PresignedURLsSupported() bool { return false }

func (f *FileStorage) PresignedGetObject(ctx context.Context, key string, expiry time.Duration) (string, error) {
	return "", ErrPresignedNotSupported
}

// detectContentType inspects the file extension and falls back to
// application/octet-stream. This is sufficient for the known object
// types this project stores: JSON and images.
func detectContentType(key string) string {
	ext := filepath.Ext(key)
	ct := mime.TypeByExtension(ext)
	if ct == "" {
		ct = "application/octet-stream"
	}
	return ct
}
