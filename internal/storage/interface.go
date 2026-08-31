package storage

import (
	"context"
	"fmt"
	"io"
	"time"
)

// StorageBackend is the interface for object storage operations.
// Implemented by S3, file, and mock backends.
type StorageBackend interface {
	// ListObjects returns all objects under the given prefix (folder-like path).
	ListObjects(ctx context.Context, prefix string) ([]ObjectInfo, error)

	// GetObject retrieves an object by key. Returns a reader, content size, and error.
	// The caller must close the ReadCloser.
	GetObject(ctx context.Context, key string) (io.ReadCloser, int64, error)

	// PutObject stores data at the given key with the specified content type.
	PutObject(ctx context.Context, key string, data io.Reader, size int64, contentType string) error

	// LocalFile returns a local filesystem path for the object, plus a
	// cleanup function. File-based backends return the object's real path
	// with a nil cleanup (zero copy; the source file is never modified).
	// Remote backends (S3) download the object into a temp file and return
	// a cleanup that removes it. The path is valid only until cleanup is
	// called; the caller MUST call cleanup when it is non-nil.
	LocalFile(ctx context.Context, key string) (path string, cleanup func(), err error)

	// DeleteObject removes an object by key.
	DeleteObject(ctx context.Context, key string) error

	// PresignedURLsSupported reports whether this backend can generate
	// temporary download URLs via PresignedGetObject. File-based backends
	// typically return false — clients should use the server's serve
	// endpoints instead.
	PresignedURLsSupported() bool

	// PresignedGetObject returns a temporary URL to download the object.
	// Returns ErrPresignedNotSupported if this backend does not support
	// presigned URLs (check PresignedURLsSupported first).
	PresignedGetObject(ctx context.Context, key string, expiry time.Duration) (string, error)
}

// ErrPresignedNotSupported is returned when PresignedGetObject is called on a
// backend that does not support presigned URLs (e.g. file storage).
var ErrPresignedNotSupported = fmt.Errorf("presigned URLs are not supported by this backend")

// ObjectInfo holds metadata for a single object returned by ListObjects.
type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
	ETag         string
}
