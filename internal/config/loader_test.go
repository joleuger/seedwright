package config

import (
	"os"
	"testing"
)

func TestLoad_validConfig(t *testing.T) {
	yaml := `
server:
  listen: ":9090"
sdcpp:
  backends:
    - name: "default"
      base_url: "http://localhost:3000"
storage:
  endpoint: "https://s3.example.com"
  region: "us-east-1"
  bucket: "images"
  access_key: "AKIA"
  secret_key: "secret"
database:
  sqlite_path: "test.db"
application:
  title: "Test"
  default_project: "dev"
`
	path := writeTempYAML(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Server.Listen != ":9090" {
		t.Errorf("listen = %q, want %q", cfg.Server.Listen, ":9090")
	}
	if len(cfg.SDCPP.Backends) != 1 {
		t.Fatalf("backends = %d, want 1", len(cfg.SDCPP.Backends))
	}
	if cfg.SDCPP.Backends[0].BaseURL != "http://localhost:3000" {
		t.Errorf("base_url = %q, want %q", cfg.SDCPP.Backends[0].BaseURL, "http://localhost:3000")
	}
	if cfg.SDCPP.Backends[0].Name != "default" {
		t.Errorf("backend name = %q, want %q", cfg.SDCPP.Backends[0].Name, "default")
	}
	if cfg.Storage.Bucket != "images" {
		t.Errorf("bucket = %q, want %q", cfg.Storage.Bucket, "images")
	}
	if cfg.Database.SQLiteDatabase != "test.db" {
		t.Errorf("sqlite_path = %q, want %q", cfg.Database.SQLiteDatabase, "test.db")
	}
	if cfg.Application.Title != "Test" {
		t.Errorf("title = %q, want %q", cfg.Application.Title, "Test")
	}
}

func TestLoad_legacyBaseURL(t *testing.T) {
	yaml := `
server:
  listen: ":8080"
sdcpp:
  base_url: "http://legacy:1234"
storage:
  endpoint: "https://s3.example.com"
  region: "us-east-1"
  bucket: "images"
  access_key: "AKIA"
  secret_key: "secret"
database:
  sqlite_path: "test.db"
application:
  title: "Test"
  default_project: "dev"
`
	path := writeTempYAML(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.SDCPP.Backends) != 1 {
		t.Fatalf("backends = %d, want 1", len(cfg.SDCPP.Backends))
	}
	if cfg.SDCPP.Backends[0].BaseURL != "http://legacy:1234" {
		t.Errorf("base_url = %q, want %q", cfg.SDCPP.Backends[0].BaseURL, "http://legacy:1234")
	}
	if cfg.SDCPP.Backends[0].Name != "default" {
		t.Errorf("backend name = %q, want %q", cfg.SDCPP.Backends[0].Name, "default")
	}
}

func TestLoad_missingFile(t *testing.T) {
	// A missing config file is not an error: first-run defaults
	// (memory storage + conventional sdcpp URL) keep the app bootable.
	cfg, err := Load("/nonexistent/path.yaml")
	if err != nil {
		t.Fatalf("Load(missing file) should return first-run defaults, got error: %v", err)
	}
	if cfg.Storage.Type != "memory" {
		t.Errorf("storage.type = %q, want memory", cfg.Storage.Type)
	}
}

func TestLoad_invalidYAML(t *testing.T) {
	path := writeTempYAML(t, "{{{ not valid yaml }}}")
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected error for invalid YAML")
	}
}

func TestLoad_missingRequiredFields(t *testing.T) {
	yaml := `
server:
  listen: ":8080"
`
	path := writeTempYAML(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected error for missing required fields")
	}
}

func TestLoad_defaults(t *testing.T) {
	yaml := `
server:
  listen: ":8080"
sdcpp:
  backends:
    - name: "default"
      base_url: "http://localhost:3000"
storage:
  endpoint: "https://s3.example.com"
  region: "us-east-1"
  bucket: "images"
  access_key: "AKIA"
  secret_key: "secret"
database:
  sqlite_path: "cache.db"
`
	path := writeTempYAML(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Application.Title != "seedwright" {
		t.Errorf("title = %q, want %q", cfg.Application.Title, "seedwright")
	}
	if cfg.Application.DefaultProject != "default" {
		t.Errorf("default_project = %q, want %q", cfg.Application.DefaultProject, "default")
	}
}

func TestLoad_multipleBackends(t *testing.T) {
	yaml := `
server:
  listen: ":8080"
sdcpp:
  backends:
    - name: "local"
      base_url: "http://localhost:1235"
    - name: "production"
      base_url: "http://prod:1235"
storage:
  endpoint: "https://s3.example.com"
  region: "us-east-1"
  bucket: "images"
  access_key: "AKIA"
  secret_key: "secret"
database:
  sqlite_path: "cache.db"
`
	path := writeTempYAML(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.SDCPP.Backends) != 2 {
		t.Fatalf("backends = %d, want 2", len(cfg.SDCPP.Backends))
	}
	if cfg.BackendNames()[0] != "local" {
		t.Errorf("first backend = %q, want %q", cfg.BackendNames()[0], "local")
	}
	if cfg.BackendNames()[1] != "production" {
		t.Errorf("second backend = %q, want %q", cfg.BackendNames()[1], "production")
	}
	url, err := cfg.BackendURL("production")
	if err != nil {
		t.Fatalf("BackendURL(production): %v", err)
	}
	if url != "http://prod:1235" {
		t.Errorf("BackendURL(production) = %q, want %q", url, "http://prod:1235")
	}
	if cfg.DefaultBackend() != "local" {
		t.Errorf("DefaultBackend() = %q, want %q", cfg.DefaultBackend(), "local")
	}
}

func writeTempYAML(t *testing.T, content string) string {
	f, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

func TestLoad_fileStorageType(t *testing.T) {
	yaml := `
server:
  listen: ":8080"
sdcpp:
  backends:
    - name: "default"
      base_url: "http://localhost:3000"
storage:
  type: "file"
  file_path: "/tmp/storage"
database:
  sqlite_path: "test.db"
`
	path := writeTempYAML(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Storage.Type != "file" {
		t.Errorf("storage type = %q, want %q", cfg.Storage.Type, "file")
	}
	if cfg.Storage.FilePath != "/tmp/storage" {
		t.Errorf("file_path = %q, want %q", cfg.Storage.FilePath, "/tmp/storage")
	}
	if cfg.StorageNode.Kind == 0 {
		t.Fatal("StorageNode should be set")
	}
}

func TestLoad_s3StorageType(t *testing.T) {
	yaml := `
server:
  listen: ":8080"
sdcpp:
  backends:
    - name: "default"
      base_url: "http://localhost:3000"
storage:
  type: "s3"
  endpoint: "https://s3.example.com"
  region: "garage"
  bucket: "images"
database:
  sqlite_path: "test.db"
`
	path := writeTempYAML(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Storage.Type != "s3" {
		t.Errorf("storage type = %q, want %q", cfg.Storage.Type, "s3")
	}
	if cfg.StorageNode.Kind == 0 {
		t.Fatal("StorageNode should be set")
	}
}

func TestExtensionConfig_noExtensionsSection(t *testing.T) {
	yaml := `
server:
  listen: ":8080"
sdcpp:
  backends:
    - name: "default"
      base_url: "http://localhost:3000"
storage:
  endpoint: "https://s3.example.com"
  region: "us-east-1"
  bucket: "images"
  access_key: "AKIA"
  secret_key: "secret"
database:
  sqlite_path: "cache.db"
`
	path := writeTempYAML(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	type extCfg struct {
		Enabled bool `yaml:"enabled"`
	}
	var c extCfg
	if err := cfg.ExtensionConfig("joleuger/batch", &c); err != nil {
		t.Fatalf("ExtensionConfig(): %v", err)
	}
	if c.Enabled {
		t.Errorf("Enabled = %v, want false (no config provided)", c.Enabled)
	}
}

func TestExtensionConfig_noConfigForExtension(t *testing.T) {
	yaml := `
server:
  listen: ":8080"
sdcpp:
  backends:
    - name: "default"
      base_url: "http://localhost:3000"
storage:
  endpoint: "https://s3.example.com"
  region: "us-east-1"
  bucket: "images"
  access_key: "AKIA"
  secret_key: "secret"
database:
  sqlite_path: "cache.db"
extensions:
  other/thing:
    foo: bar
`
	path := writeTempYAML(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	type extCfg struct {
		Enabled bool `yaml:"enabled"`
	}
	var c extCfg
	if err := cfg.ExtensionConfig("joleuger/batch", &c); err != nil {
		t.Fatalf("ExtensionConfig(): %v", err)
	}
	if c.Enabled {
		t.Errorf("Enabled = %v, want false (no config for this extension)", c.Enabled)
	}
}

func TestExtensionConfig_providedConfig(t *testing.T) {
	yaml := `
server:
  listen: ":8080"
sdcpp:
  backends:
    - name: "default"
      base_url: "http://localhost:3000"
storage:
  endpoint: "https://s3.example.com"
  region: "us-east-1"
  bucket: "images"
  access_key: "AKIA"
  secret_key: "secret"
database:
  sqlite_path: "cache.db"
extensions:
  joleuger/batch:
    max_workers: 4
    continue_on_failure: true
`
	path := writeTempYAML(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	type extCfg struct {
		MaxWorkers        int  `yaml:"max_workers"`
		ContinueOnFailure bool `yaml:"continue_on_failure"`
	}
	var c extCfg
	if err := cfg.ExtensionConfig("joleuger/batch", &c); err != nil {
		t.Fatalf("ExtensionConfig(): %v", err)
	}
	if c.MaxWorkers != 4 {
		t.Errorf("max_workers = %d, want 4", c.MaxWorkers)
	}
	if !c.ContinueOnFailure {
		t.Error("continue_on_failure = false, want true")
	}
}

func TestExtensionConfig_invalidYamlKey(t *testing.T) {
	yaml := `
server:
  listen: ":8080"
sdcpp:
  backends:
    - name: "default"
      base_url: "http://localhost:3000"
storage:
  endpoint: "https://s3.example.com"
  region: "us-east-1"
  bucket: "images"
  access_key: "AKIA"
  secret_key: "secret"
database:
  sqlite_path: "cache.db"
extensions:
  invalid key with spaces:
    foo: bar
`
	path := writeTempYAML(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected error for invalid extension key")
	}
}
