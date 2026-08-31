package config

import (
	"path/filepath"
	"testing"
)

func TestLoad_MissingFile_ReturnsFirstRunDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.yaml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(missing file) should not error, got: %v", err)
	}

	// Ephemeral memory storage is the first-run default.
	if cfg.Storage.Type != "memory" {
		t.Errorf("storage.type = %q, want memory", cfg.Storage.Type)
	}
	// The storage factory must receive a usable node.
	if cfg.StorageNode.Kind == 0 {
		t.Error("StorageNode is a zero node — storage factory would fail")
	}

	// The conventional sdcpp location.
	if len(cfg.SDCPP.Backends) != 1 {
		t.Fatalf("backends = %d, want 1", len(cfg.SDCPP.Backends))
	}
	if cfg.SDCPP.Backends[0].Name != "default" {
		t.Errorf("backend name = %q, want default", cfg.SDCPP.Backends[0].Name)
	}
	if cfg.SDCPP.Backends[0].BaseURL != "http://127.0.0.1:1234" {
		t.Errorf("base_url = %q, want http://127.0.0.1:1234", cfg.SDCPP.Backends[0].BaseURL)
	}

	// Everything else falls back to the usual defaults.
	if cfg.Server.Listen != ":8080" {
		t.Errorf("listen = %q, want :8080", cfg.Server.Listen)
	}
	if cfg.Application.Title != "seedwright" {
		t.Errorf("title = %q, want seedwright", cfg.Application.Title)
	}
	if cfg.Application.DefaultProject != "default" {
		t.Errorf("default_project = %q, want default", cfg.Application.DefaultProject)
	}
	if cfg.Auth != nil {
		t.Error("auth should be nil for first-run defaults")
	}
}

func TestApplyDefaults_BackendDefaultIsConventionalURL(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	if len(cfg.SDCPP.Backends) != 1 || cfg.SDCPP.Backends[0].BaseURL != "http://127.0.0.1:1234" {
		t.Errorf("default backend = %+v, want http://127.0.0.1:1234", cfg.SDCPP.Backends)
	}
}

func TestValidate_MemoryStorageRequiresNothing(t *testing.T) {
	cfg := &Config{
		Storage: StorageConfig{Type: "memory"},
		Database: DatabaseConfig{
			SQLiteDatabase: "cache.db",
		},
		SDCPP: SDCPPConfig{
			Backends: []SDCPPBackend{{Name: "default", BaseURL: "http://127.0.0.1:1234"}},
		},
		Server: ServerConfig{Listen: ":8080"},
	}
	if err := Validate(cfg); err != nil {
		t.Errorf("Validate(memory) should pass, got: %v", err)
	}
}

func TestLoad_PresentButInvalidStillFails(t *testing.T) {
	path := writeTempYAML(t, "storage:\n  type: memory\n")
	// No database / server / backends → invalid.
	if _, err := Load(path); err == nil {
		t.Error("Load(present but invalid) should fail")
	}
}
