package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestNewStorageBackend_S3(t *testing.T) {
	node := yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "type"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "s3"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "endpoint"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "http://localhost:9000"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "region"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "garage"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "bucket"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "test-bucket"},
		},
	}

	store, err := NewStorageBackend(node)
	if err != nil {
		t.Fatalf("NewStorageBackend(s3): %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil S3Client")
	}
}

func TestNewStorageBackend_File(t *testing.T) {
	dir := t.TempDir()
	node := yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "type"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "file"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "file_path"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: dir},
		},
	}

	store, err := NewStorageBackend(node)
	if err != nil {
		t.Fatalf("NewStorageBackend(file): %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil FileStorage")
	}
}

func TestNewStorageBackend_DefaultsToS3(t *testing.T) {
	node := yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "endpoint"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "http://localhost:9000"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "region"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "garage"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "bucket"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "test-bucket"},
		},
	}

	store, err := NewStorageBackend(node)
	if err != nil {
		t.Fatalf("NewStorageBackend(default): %v", err)
	}
	if _, ok := store.(*S3Client); !ok {
		t.Errorf("expected *S3Client for empty type, got %T", store)
	}
}

func TestNewStorageBackend_UnknownType(t *testing.T) {
	node := yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "type"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "redis"},
		},
	}

	_, err := NewStorageBackend(node)
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
	if !strings.Contains(err.Error(), "unknown storage backend type") {
		t.Errorf("error message should mention unknown type: %v", err)
	}
}

func TestNewStorageBackend_FileNoPath(t *testing.T) {
	node := yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "type"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "file"},
		},
	}

	_, err := NewStorageBackend(node)
	if err == nil {
		t.Fatal("expected error for file type without file_path")
	}
}

func TestNewStorageBackend_S3NoEndpoint(t *testing.T) {
	node := yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "type"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "s3"},
		},
	}

	_, err := NewStorageBackend(node)
	if err == nil {
		t.Fatal("expected error for s3 type without endpoint")
	}
}

func TestNewStorageBackend_FileOps(t *testing.T) {
	dir := t.TempDir()
	node := yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "type"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "file"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: "file_path"},
			{Kind: yaml.ScalarNode, Tag: "yaml.org,2002:str", Value: dir},
		},
	}

	store, err := NewStorageBackend(node)
	if err != nil {
		t.Fatalf("NewStorageBackend(file): %v", err)
	}

	ctx := t.Context()
	data := []byte("factory-created file storage")
	if err := store.PutObject(ctx, "projects/test/hello.txt", bytes.NewReader(data), int64(len(data)), "text/plain"); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	// Read back from disk to verify persistence.
	body, err := os.ReadFile(filepath.Join(dir, "projects", "test", "hello.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(body) != string(data) {
		t.Errorf("body = %q, want %q", body, data)
	}
}

