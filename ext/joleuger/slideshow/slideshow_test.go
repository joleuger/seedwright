package slideshow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"seedwright/internal/app"
	"seedwright/internal/config"
	"seedwright/internal/data"
	"seedwright/internal/data/model"
	"seedwright/internal/storage"
)

func TestInheritedFilterQuery(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"page=3&per_page=24", ""},
		{"favorites=true", "favorites=true"},
		// Encode() sorts keys; pagination params are dropped.
		{"page=2&per_page=all&sort=seed&order=asc&favorites=true", "favorites=true&order=asc&sort=seed"},
		{"origin=external&page=1", "origin=external"},
	}
	for _, tt := range tests {
		q, err := url.ParseQuery(tt.in)
		if err != nil {
			t.Fatalf("ParseQuery(%q): %v", tt.in, err)
		}
		if got := inheritedFilterQuery(q); got != tt.want {
			t.Errorf("inheritedFilterQuery(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// setupSlideshowTest builds an extension on a real in-memory DB with one
// project ("test-project"), mirroring the core handler test harness.
func setupSlideshowTest(t *testing.T) (*Extension, *http.ServeMux) {
	t.Helper()

	db, err := data.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	store := storage.NewMockStorage()
	repo := data.NewProjectRepository(db, store)
	if err := repo.CreateProject(context.Background(), model.NewProject("test-project")); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	mux := http.NewServeMux()
	e := New(mux, repo, "")
	e.RegisterRoutes(&app.App{}) // nil Authz — RequireAction allows everything
	return e, mux
}

func getSlideshowPage(t *testing.T, mux *http.ServeMux, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestHandlePage_Renders(t *testing.T) {
	_, mux := setupSlideshowTest(t)

	code, body := getSlideshowPage(t, mux, "/basic/test-project/slideshow")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !strings.Contains(body, "SLIDESHOW — test-project") {
		t.Error("page does not show the project title")
	}
	// The queue fetch targets the core elements API, capped at 500.
	// (html/template escapes / and & in JS strings, so match on the
	// unescaped fragments.)
	if !strings.Contains(body, "var MAX_SLIDES = 500;") {
		t.Error("page does not define the queue cap")
	}
	if !strings.Contains(body, "elements?per_page=") {
		t.Error("page does not fetch the core elements API")
	}
}

func TestHandlePage_InheritsFilters(t *testing.T) {
	_, mux := setupSlideshowTest(t)

	code, body := getSlideshowPage(t, mux,
		"/basic/test-project/slideshow?favorites=true&sort=seed&page=2&per_page=24")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	// Filters carried over, pagination dropped. html/template renders &
	// as \u0026 inside JS string literals (valid JS, same value).
	if !strings.Contains(body, `var INHERITED = 'favorites=true\u0026sort=seed';`) {
		t.Error("inherited query should carry the filters and drop pagination")
	}
}

func TestHandlePage_UnknownProject404(t *testing.T) {
	_, mux := setupSlideshowTest(t)

	code, _ := getSlideshowPage(t, mux, "/basic/no-such-project/slideshow")
	if code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for unknown project", code)
	}
}

func TestHandlePage_PathPrefixInPage(t *testing.T) {
	db, err := data.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := data.NewProjectRepository(db, storage.NewMockStorage())
	if err := repo.CreateProject(context.Background(), model.NewProject("p1")); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	e := New(mux, repo, "/sd")
	e.RegisterRoutes(&app.App{})

	code, body := getSlideshowPage(t, mux, "/basic/p1/slideshow")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	// html/template escapes / as \/ inside JS string literals.
	if !strings.Contains(body, `var PREFIX = '\/sd';`) {
		t.Error("page does not embed the path prefix")
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	c, err := LoadConfig(&config.Config{})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !c.Enabled {
		t.Error("Enabled = false, want default true when config is absent")
	}
}

func TestLoadConfig_DisabledInFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
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
extensions:
  joleuger/slideshow:
    enabled: false
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg2, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	c, err := LoadConfig(cfg2)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.Enabled {
		t.Error("Enabled = true, want false per config")
	}
}

func TestDescriptor_Basics(t *testing.T) {
	if Descriptor.Key() != "joleuger/slideshow" {
		t.Errorf("Key = %q, want joleuger/slideshow", Descriptor.Key())
	}
	if len(Descriptor.Dependencies()) != 0 {
		t.Errorf("Dependencies = %v, want none (leaf extension)", Descriptor.Dependencies())
	}
	if err := Descriptor.Migrate(context.Background(), nil); err != nil {
		t.Errorf("Migrate = %v, want nil (no schema)", err)
	}
	if err := Descriptor.Sync(context.Background(), &app.App{}); err != nil {
		t.Errorf("Sync = %v, want nil (no state)", err)
	}
}

func TestSlideShowTemplate_Parses(t *testing.T) {
	// ParseFS panics on a malformed template; reaching here is the check.
	if SlideShowTemplate() == nil {
		t.Fatal("SlideShowTemplate() = nil")
	}
}
