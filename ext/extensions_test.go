package ext

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"seedwright/internal/app"
)

// writeTestConfig creates a minimal valid config file in dir, with an
// optional extra block appended (e.g. an extensions: override).
func writeTestConfig(t *testing.T, dir, extra string) string {
	t.Helper()
	cfg := `
server:
  listen: "127.0.0.1:0"
sdcpp:
  backends:
    - name: "default"
      base_url: "http://localhost:1235"
storage:
  type: "file"
  file_path: ` + dir + `/storage
database:
  sqlite_path: ` + dir + `/cache.db
application:
  title: "test"
` + extra
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// bootstrapTestApp runs the real app.Bootstrap against a temp config so
// that RegisterAll gets a fully wired App (DB, mux, hooks, storage).
func bootstrapTestApp(t *testing.T, extra string) *app.App {
	t.Helper()
	dir := t.TempDir()
	a, err := app.Bootstrap(writeTestConfig(t, dir, extra), false)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	t.Cleanup(func() { a.DB.Close() })
	return a
}

func TestRegisterAll_AllEnabled(t *testing.T) {
	a := bootstrapTestApp(t, "")
	if err := RegisterAll(context.Background(), a, a.Config); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}

	if len(a.EnabledExtensions) != len(Bundled) {
		t.Errorf("enabled extensions = %v, want all %d bundled", a.EnabledExtensions, len(Bundled))
	}
	if !strings.Contains(strings.Join(a.EnabledExtensions, ","), "joleuger/imageproc") {
		t.Error("imageproc not in the enabled list")
	}
	if a.ExtDeps == nil {
		t.Fatal("ExtDeps is nil after RegisterAll")
	}
	for _, k := range a.EnabledExtensions {
		if !a.ExtDeps.IsInitialized(k) {
			t.Errorf("%s not marked initialized", k)
		}
	}

	// The photobooth registers its own settings saver — the core settings
	// endpoint dispatches section saves to the extension, which owns the
	// validation and persistence of its settings.
	if a.Hooks == nil || a.Hooks.SettingsSavers == nil {
		t.Fatal("Hooks.SettingsSavers is nil after RegisterAll")
	}
	if a.Hooks.SettingsSavers["joleuger/photobooth"] == nil {
		t.Error("photobooth settings saver not registered")
	}
}

func TestRegisterAll_PhotoboothEnabled_PrinterDisabled(t *testing.T) {
	// The photobooth declares an optional runtime dependency on the
	// printer. With the printer disabled, registration must still
	// succeed — the print feature degrades, startup does not.
	a := bootstrapTestApp(t, "extensions:\n  joleuger/printer:\n    enabled: false\n")
	if err := RegisterAll(context.Background(), a, a.Config); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}

	for _, k := range a.EnabledExtensions {
		if k == "joleuger/printer" {
			t.Error("printer is in the enabled list but disabled in config")
		}
	}
	if a.ExtDeps.IsEnabled("joleuger/photobooth") != true {
		t.Error("photobooth not marked enabled")
	}
	if a.ExtDeps.IsEnabled("joleuger/printer") {
		t.Error("printer marked enabled but disabled in config")
	}
	if !a.ExtDeps.IsInitialized("joleuger/photobooth") {
		t.Error("photobooth not marked initialized")
	}
}

func TestRegisterAll_PrinterRequiresImageProc(t *testing.T) {
	// The printer imports imageproc's Go package (the crop pipeline), so
	// a CompileRequired edge exists: disabling imageproc while leaving
	// the printer enabled must fail startup.
	a := bootstrapTestApp(t, "extensions:\n  joleuger/imageproc:\n    enabled: false\n")
	err := RegisterAll(context.Background(), a, a.Config)
	if err == nil {
		t.Fatal("RegisterAll succeeded with imageproc disabled, want a dependency error")
	}
	if !strings.Contains(err.Error(), "joleuger/imageproc") {
		t.Errorf("error = %q, want it to mention joleuger/imageproc", err)
	}
}

func TestResolveEnabled_DisabledExtensionsSkipped(t *testing.T) {
	a := bootstrapTestApp(t, "extensions:\n  joleuger/favorites:\n    enabled: false\n  joleuger/printer:\n    enabled: false\n")

	enabled, allKeys, err := resolveEnabled(a.Config)
	if err != nil {
		t.Fatalf("resolveEnabled: %v", err)
	}
	if len(allKeys) != len(Bundled) {
		t.Errorf("allKeys = %d, want %d", len(allKeys), len(Bundled))
	}
	if len(enabled) != len(Bundled)-2 {
		t.Errorf("enabled = %d, want %d", len(enabled), len(Bundled)-2)
	}
	for _, e := range enabled {
		if e.Key() == "joleuger/favorites" || e.Key() == "joleuger/printer" {
			t.Errorf("%s is enabled but disabled in config", e.Key())
		}
	}
	if !strings.Contains(strings.Join(allKeys, ","), "joleuger/favorites") {
		t.Error("allKeys missing favorites")
	}
}
