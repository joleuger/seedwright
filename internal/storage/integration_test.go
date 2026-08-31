//go:build integration

package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v5"

	"seedwright/internal/data/model"
)

// mustReadFirstLine reads the first line of the file and returns it
// after stripping the given prefix (or the entire line if prefix is
// empty). It fatals the test if the file cannot be read or is empty.
func mustReadFirstLine(filePath, prefix string) string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		panic(fmt.Sprintf("read %s: %v", filePath, err))
	}
	line := strings.TrimSpace(strings.Split(string(data), "\n")[0])
	if prefix != "" {
		if !strings.HasPrefix(line, prefix) {
			panic(fmt.Sprintf("%s: unexpected line: %q", filePath, line))
		}
		line = strings.TrimPrefix(line, prefix)
	}
	return strings.TrimSpace(line)
}

// skipIfNoIntegration skips the test when SDCPP_INTEGRATION is not "1".
func skipIfNoIntegration(t *testing.T) {
	if os.Getenv("SDCPP_INTEGRATION") != "1" {
		t.Skip("set SDCPP_INTEGRATION=1 to run integration tests")
	}
}

// skipIfNoS3 skips when SDCPP_INTEGRATION=1 but no S3 credentials are configured.
// At least one of env vars or test credential files must provide endpoint + bucket.
func skipIfNoS3(t *testing.T) {
	skipIfNoIntegration(t)

	// Fast path: all four env vars set.
	if os.Getenv("SDCPP_S3_ENDPOINT") != "" && os.Getenv("SDCPP_S3_BUCKET") != "" {
		return
	}

	// Slow path: check test credential files.
	testDir := "test"
	if v := os.Getenv("TEST_DIR"); v != "" {
		testDir = v
	}
	_, err := os.Stat(filepath.Join(testDir, "garage-access"))
	if os.IsNotExist(err) {
		t.Skip("SDCPP_INTEGRATION=1 but no S3 credentials configured")
	}
}

// newS3Client returns a new S3Client for integration tests, skipping if no
// S3 credentials are available.
func newS3Client(t *testing.T) *S3Client {
	t.Helper()
	skipIfNoS3(t)

	endpoint := os.Getenv("SDCPP_S3_ENDPOINT")
	region := os.Getenv("SDCPP_S3_REGION")
	bucket := os.Getenv("SDCPP_S3_BUCKET")
	accessKey := os.Getenv("SDCPP_S3_ACCESS_KEY")
	secretKey := os.Getenv("SDCPP_S3_SECRET_KEY")

	if endpoint == "" {
		endpoint = mustReadFirstLine(filepath.Join("test", "garage-access"), "garage url:")
	}
	if bucket == "" {
		bucket = mustReadFirstLine(filepath.Join("test", "garage-access"), "bucket name:")
	}
	if region == "" {
		region = mustReadFirstLine(filepath.Join("test", "garage-access"), "region:")
		if region == "" {
			region = "garage"
		}
	}
	if accessKey == "" {
		accessKey = mustReadFirstLine(filepath.Join("test", "garage-access"), "key id:")
	}
	if secretKey == "" {
		secretKey = mustReadFirstLine(filepath.Join("test", "garage-access-key"), "")
		if secretKey == "" {
			secretKey = "see file garage-access-key"
		}
	}

	client, err := NewS3Storage(endpoint, region, bucket, accessKey, secretKey, true)
	if err != nil {
		t.Fatalf("NewS3Storage: %v", err)
	}
	return client
}

// newFileClient returns a new FileStorage backed by a temp directory.
func newFileClient(t *testing.T) *FileStorage {
	t.Helper()
	skipIfNoIntegration(t)
	return &FileStorage{basePath: t.TempDir()}
}

// runStorageTests runs a common set of StorageBackend operations against the
// given client. This lets both S3 and file backends share the same test logic.
func runStorageTests(t *testing.T, store StorageBackend) {
	ctx := context.Background()

	// PutObject + GetObject.
	key := "test/integration-" + time.Now().Format("20060102-150405") + ".txt"
	data := []byte("integration test payload")
	if err := store.PutObject(ctx, key, bytes.NewReader(data), int64(len(data)), "text/plain"); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	reader, size, err := store.GetObject(ctx, key)
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

	// ListObjects.
	prefix := "test/integration-list-" + time.Now().Format("20060102-150405") + "/"
	keys := []string{
		prefix + "meta.json",
		prefix + "elements/abc.json",
		prefix + "images/abc.png",
	}
	for _, k := range keys {
		if err := store.PutObject(ctx, k, bytes.NewReader([]byte("data")), 4, "application/json"); err != nil {
			t.Fatalf("PutObject (list): %v", err)
		}
	}

	items, err := store.ListObjects(ctx, prefix)
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(items) != len(keys) {
		t.Errorf("got %d items, want %d", len(items), len(keys))
	}

	// DeleteObject.
	if err := store.DeleteObject(ctx, key); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}

	_, _, err = store.GetObject(ctx, key)
	if err == nil {
		t.Fatal("GetObject after delete: expected error")
	}

	// Cleanup list objects.
	for _, k := range keys {
		store.DeleteObject(ctx, k)
	}
}

// --- S3-specific tests ---

func TestS3Client_PutAndGet(t *testing.T) {
	runStorageTests(t, newS3Client(t))
}

func TestS3Client_ListObjects(t *testing.T) {
	store := newS3Client(t)
	ctx := context.Background()

	prefix := "integration-test/list-" + time.Now().Format("20060102-150405") + "/"

	keys := []string{
		prefix + "meta.json",
		prefix + "elements/abc.json",
		prefix + "images/abc.png",
	}
	for _, k := range keys {
		if err := store.PutObject(ctx, k, bytes.NewReader([]byte("data")), 4, "application/json"); err != nil {
			t.Fatalf("PutObject: %v", err)
		}
	}

	items, err := store.ListObjects(ctx, prefix)
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(items) != len(keys) {
		t.Errorf("got %d items, want %d", len(items), len(keys))
	}
	for _, item := range items {
		if item.Key == "" {
			t.Error("empty key in items")
		}
	}

	for _, k := range keys {
		store.DeleteObject(ctx, k)
	}
}

func TestS3Client_LocalFile(t *testing.T) {
	store := newS3Client(t)
	ctx := context.Background()

	key := "integration-test/localfile-" + time.Now().Format("20060102-150405") + "/obj.png"
	data := []byte("s3 local file bytes")
	if err := store.PutObject(ctx, key, bytes.NewReader(data), int64(len(data)), "image/png"); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	defer store.DeleteObject(ctx, key)

	path, cleanup, err := store.LocalFile(ctx, key)
	if err != nil {
		t.Fatalf("LocalFile: %v", err)
	}
	if cleanup == nil {
		t.Fatal("cleanup = nil, want non-nil for S3")
	}
	defer cleanup()

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

func TestS3Client_PresignedGetObject(t *testing.T) {
	store := newS3Client(t)
	ctx := context.Background()

	key := "integration-test/presigned-" + time.Now().Format("20060102-150405") + ".txt"
	if err := store.PutObject(ctx, key, bytes.NewReader([]byte("data")), 4, "text/plain"); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	if !store.PresignedURLsSupported() {
		t.Fatal("expected S3 backend to support presigned URLs")
	}

	url, err := store.PresignedGetObject(ctx, key, 1*time.Hour)
	if err != nil {
		t.Fatalf("PresignedGetObject: %v", err)
	}
	if url == "" {
		t.Error("presigned URL is empty")
	}

	store.DeleteObject(ctx, key)
}

func TestS3Client_Delete(t *testing.T) {
	store := newS3Client(t)
	ctx := context.Background()

	key := "integration-test/delete-" + time.Now().Format("20060102-150405") + ".txt"
	if err := store.PutObject(ctx, key, bytes.NewReader([]byte("data")), 4, "text/plain"); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	if err := store.DeleteObject(ctx, key); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}

	_, _, err := store.GetObject(ctx, key)
	if err == nil {
		t.Fatal("GetObject after delete: expected error")
	}
}

func TestS3Client_PutObject_NonSeekableReader(t *testing.T) {
	store := newS3Client(t)
	ctx := context.Background()

	key := "integration-test/noseeker-" + time.Now().Format("20060102-150405") + ".txt"
	// io.NopCloser creates a non-seekable reader — this is what the
	// actual code path does when handleJobSuccess passes an image reader.
	data := []byte("non-seekable test payload")
	reader := io.NopCloser(bytes.NewReader(data))

	if err := store.PutObject(ctx, key, reader, int64(len(data)), "text/plain"); err != nil {
		t.Fatalf("PutObject with non-seekable reader: %v", err)
	}

	getReader, size, err := store.GetObject(ctx, key)
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer getReader.Close()

	if size != int64(len(data)) {
		t.Errorf("size = %d, want %d", size, len(data))
	}

	body, err := io.ReadAll(getReader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(body, data) {
		t.Errorf("body = %q, want %q", body, data)
	}

	store.DeleteObject(ctx, key)
}

// --- File backend tests ---

func TestFileStorage_Integration(t *testing.T) {
	runStorageTests(t, newFileClient(t))
}

// Note: TestFileStorage_PresignedNotSupported and TestFileStorage_PathTraversal
// are defined in file_test.go (unit tests) and are intentionally not duplicated
// here to avoid redeclaration conflicts when both files are compiled together.

// --- Element schema validation tests ---

// schemaFileURL returns a file:// URL pointing at the Element JSON schema.
// Integration tests run from the repo root, so the schema lives at
// ../../schema/Element.schema from the test package.
func schemaFileURL() string {
	abs, err := filepath.Abs(filepath.Join("..", "..", "schema", "Element.schema"))
	if err != nil {
		return "" // should not happen
	}
	return "file://" + abs
}

// mustLoadSchema loads and compiles the Element JSON Schema, failing
// the test if loading fails.
func mustLoadSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	comp := jsonschema.NewCompiler()
	sch, err := comp.Compile(schemaFileURL())
	if err != nil {
		t.Fatalf("compile Element schema: %v", err)
	}
	return sch
}

// buildGeneratedElement creates a fully-populated "generated" origin
// Element matching the schema_version-1 shape written to S3 after a
// successful job completion (ImageInfo present, duration set on the element).
func buildGeneratedElement() model.Element {
	elem := model.NewImageElement("integration-test", "a beautiful sunset", 512, 512, 20, 7.0, 42, "v1-5", "", "", "", "v1-5.ckpt")
	elem.Image = &model.ImageInfo{
		S3Key:     "projects/integration-test/images/" + elem.ID + ".png",
		Format:    "png",
		Width:     512,
		Height:    512,
		SizeBytes: 1024000,
	}
	// Duration is set by the job service after job completion.
	// It lives on Generation.Duration and is persisted to S3.
	elem.Generation.Duration = 12.34
	return elem
}

// buildUploadedElement creates an "uploaded" origin Element with no
// Generation object (manually uploaded image with no sdcpp job).
func buildUploadedElement() model.Element {
	now := time.Now().UTC()
	elem := model.Element{
		ID:            model.NewImageElement("", "", 0, 0, 0, 0, 0, "", "").ID,
		Project:       "integration-test",
		Kind:          "image",
		Origin:        "uploaded",
		SchemaVersion: 1,
		Version:       1,
		CreatedAt:     now,
		Image: &model.ImageInfo{
			S3Key:     "projects/integration-test/images/" + model.NewImageElement("", "", 0, 0, 0, 0, 0, "", "").ID + ".png",
			Format:    "png",
			Width:     640,
			Height:    480,
			SizeBytes: 204800,
		},
		Prompt:  "manually uploaded image",
		Width:   640,
		Height:  480,
		Seed:    0,
		Model:   model.ElementModel{Architecture: "manual", Name: "manual.png"},
	}
	elem.Generation = nil
	return elem
}

// runElementSchemaTests runs Element JSON schema validation tests against
// the given StorageBackend. The element JSON is written via PutObject,
// read back via GetObject, parsed, and validated against the schema.
func runElementSchemaTests(t *testing.T, store StorageBackend) {
	sch := mustLoadSchema(t)
	ctx := context.Background()

	t.Run("generated_origin_validates", func(t *testing.T) {
		elem := buildGeneratedElement()
		data, err := elem.ToJSON()
		if err != nil {
			t.Fatalf("marshal element: %v", err)
		}

		key := "integration-test/elements/" + elem.ID + ".json"
		if err := store.PutObject(ctx, key, bytes.NewReader(data), int64(len(data)), "application/json"); err != nil {
			t.Fatalf("PutObject: %v", err)
		}
		defer store.DeleteObject(ctx, key)

		// Read back from storage.
		reader, _, err := store.GetObject(ctx, key)
		if err != nil {
			t.Fatalf("GetObject: %v", err)
		}
		defer reader.Close()

		storageData, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}

		// Parse as generic JSON object for schema validation.
		var raw map[string]any
		if err := json.Unmarshal(storageData, &raw); err != nil {
			t.Fatalf("unmarshal storage data: %v", err)
		}

		// Validate against schema.
		if err := sch.Validate(raw); err != nil {
			t.Errorf("generated-origin element does not validate against schema: %v", err)
		}
	})

	t.Run("uploaded_origin_validates", func(t *testing.T) {
		elem := buildUploadedElement()
		data, err := elem.ToJSON()
		if err != nil {
			t.Fatalf("marshal element: %v", err)
		}

		key := "integration-test/elements/" + elem.ID + ".json"
		if err := store.PutObject(ctx, key, bytes.NewReader(data), int64(len(data)), "application/json"); err != nil {
			t.Fatalf("PutObject: %v", err)
		}
		defer store.DeleteObject(ctx, key)

		// Read back from storage.
		reader, _, err := store.GetObject(ctx, key)
		if err != nil {
			t.Fatalf("GetObject: %v", err)
		}
		defer reader.Close()

		storageData, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}

		var raw map[string]any
		if err := json.Unmarshal(storageData, &raw); err != nil {
			t.Fatalf("unmarshal storage data: %v", err)
		}

		if err := sch.Validate(raw); err != nil {
			t.Errorf("uploaded-origin element does not validate against schema: %v", err)
		}
	})
}

func TestS3Client_ElementSchema_Generated(t *testing.T) {
	runElementSchemaTests(t, newS3Client(t))
}

func TestS3Client_ElementSchema_Uploaded(t *testing.T) {
	runElementSchemaTests(t, newS3Client(t))
}

func TestFileStorage_ElementSchema_Generated(t *testing.T) {
	runElementSchemaTests(t, newFileClient(t))
}

func TestFileStorage_ElementSchema_Uploaded(t *testing.T) {
	runElementSchemaTests(t, newFileClient(t))
}
