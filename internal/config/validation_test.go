package config

import "testing"

func TestValidate_emptyConfig(t *testing.T) {
	cfg := &Config{}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("Validate() expected error for empty config")
	}
}

func TestValidate_validConfig(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{Listen: ":8080"},
		SDCPP: SDCPPConfig{
			Backends: []SDCPPBackend{{Name: "default", BaseURL: "http://localhost:3000"}},
		},
		Storage: StorageConfig{
			Endpoint: "https://s3.example.com",
			Region:   "garage",
			Bucket:   "images",
		},
		Database: DatabaseConfig{SQLiteDatabase: "cache.db"},
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
}

func TestValidate_invalidSDCPPURL(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{Listen: ":8080"},
		SDCPP: SDCPPConfig{
			Backends: []SDCPPBackend{{Name: "default", BaseURL: "no-scheme-here"}},
		},
		Storage: StorageConfig{
			Endpoint: "https://s3.example.com",
			Region:   "garage",
			Bucket:   "images",
		},
		Database: DatabaseConfig{SQLiteDatabase: "cache.db"},
	}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("Validate() expected error for invalid sdcpp URL")
	}
}

func TestValidate_invalidStorageEndpoint(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{Listen: ":8080"},
		SDCPP: SDCPPConfig{
			Backends: []SDCPPBackend{{Name: "default", BaseURL: "http://localhost:3000"}},
		},
		Storage: StorageConfig{
			Endpoint: ":::",
			Region:   "garage",
			Bucket:   "images",
		},
		Database: DatabaseConfig{SQLiteDatabase: "cache.db"},
	}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("Validate() expected error for invalid storage endpoint")
	}
}

func TestValidate_noBackends(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{Listen: ":8080"},
		SDCPP:  SDCPPConfig{},
		Storage: StorageConfig{
			Endpoint: "https://s3.example.com",
			Region:   "garage",
			Bucket:   "images",
		},
		Database: DatabaseConfig{SQLiteDatabase: "cache.db"},
	}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("Validate() expected error for no backends")
	}
}

func TestValidate_backendWithoutName(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{Listen: ":8080"},
		SDCPP: SDCPPConfig{
			Backends: []SDCPPBackend{{BaseURL: "http://localhost:3000"}},
		},
		Storage: StorageConfig{
			Endpoint: "https://s3.example.com",
			Region:   "garage",
			Bucket:   "images",
		},
		Database: DatabaseConfig{SQLiteDatabase: "cache.db"},
	}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("Validate() expected error for backend without name")
	}
}
