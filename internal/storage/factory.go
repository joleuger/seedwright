package storage

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// NewStorageBackend reads the "type" field from the raw YAML node
// (the storage section of config.yaml) and returns a configured
// StorageBackend. Supported types: "s3", "file", "memory".
//
// The node is expected to be the raw yaml.Node for the storage
// configuration — e.g. the value of the "storage" key in config.yaml.
func NewStorageBackend(node yaml.Node) (StorageBackend, error) {
	var raw struct {
		Type           string `yaml:"type"`
		Endpoint       string `yaml:"endpoint"`
		Region         string `yaml:"region"`
		Bucket         string `yaml:"bucket"`
		AccessKey      string `yaml:"access_key"`
		SecretKey      string `yaml:"secret_key"`
		ForcePathStyle bool   `yaml:"force_path_style"`
		FilePath       string `yaml:"file_path"`
		Capacity       string `yaml:"capacity"`
	}

	if err := node.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode storage config: %w", err)
	}

	backendType := raw.Type
	if backendType == "" {
		backendType = "s3" // default for backward compatibility
	}

	switch backendType {
	case "s3":
		if raw.Endpoint == "" {
			return nil, fmt.Errorf("storage.type=s3 requires endpoint")
		}
		if raw.Region == "" {
			return nil, fmt.Errorf("storage.type=s3 requires region")
		}
		if raw.Bucket == "" {
			return nil, fmt.Errorf("storage.type=s3 requires bucket")
		}
		return NewS3Storage(raw.Endpoint, raw.Region, raw.Bucket, raw.AccessKey, raw.SecretKey, raw.ForcePathStyle)

	case "file":
		if raw.FilePath == "" {
			return nil, fmt.Errorf("storage.type=file requires file_path")
		}
		return NewFileStorage(raw.FilePath)

	case "memory":
		capacity := int64(DefaultMemoryCapacity)
		if raw.Capacity != "" {
			c, err := ParseCapacity(raw.Capacity)
			if err != nil {
				return nil, fmt.Errorf("storage.capacity: %w", err)
			}
			capacity = c
		}
		return NewMemoryStorage(capacity)

	default:
		return nil, fmt.Errorf("unknown storage backend type: %q (supported: s3, file, memory)", backendType)
	}
}
