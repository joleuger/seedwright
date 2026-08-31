package server

import (
	"context"
	"database/sql"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"seedwright/internal/data"
	"seedwright/internal/data/model"
	"seedwright/internal/data/querybuilder"
	"seedwright/internal/storage"
)

// nilQB is safe: ListElements checks r.qb != nil before using it.
var nilQB *querybuilder.Builder

// setupHandler creates a handler + mux with in-memory SQLite + mock S3.
func setupHandler(t *testing.T) (*handler, *http.ServeMux) {
	t.Helper()
	h, mux, _ := setupHandlerWithQB(t, nilQB)
	return h, mux
}

// setupHandlerWithQB is setupHandler with a custom query builder and
// returns the DB for tests that need direct column access.
func setupHandlerWithQB(t *testing.T, qb *querybuilder.Builder) (*handler, *http.ServeMux, *sql.DB) {
	t.Helper()

	db, err := data.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	// Extension column + migration tracker — mimics what extensions
	// add via their Migrate() functions at startup.
	_, err = db.Exec(`ALTER TABLE elements ADD COLUMN ext_joleuger_favorites_is_favorite INTEGER DEFAULT 0`)
	if err != nil {
		t.Fatalf("ALTER TABLE: %v", err)
	}
	_, err = db.Exec(`INSERT OR IGNORE INTO extensions_metadata (extension_key, version) VALUES ('ext_joleuger_favorites', 1)`)
	if err != nil {
		t.Fatalf("extensions_metadata: %v", err)
	}

	store := storage.NewMockStorage()
	repo := data.NewProjectRepository(db, store)
	elemRepo := data.NewElementRepository(db, store, qb)

	ctx := context.Background()
	if err := repo.CreateProject(ctx, model.NewProject("test-project")); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	h := &handler{
		cfg: &Config{
			Title:       "seedwright",
			Storage:     store,
			ProjectRepo: repo,
			ElementRepo: elemRepo,
		},
		templates:  loadTemplates(),
		promptHelp: template.HTML(promptHelpHTML),
	}

	// Build a minimal mux with the same routes as the real app.
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.welcome)
	mux.HandleFunc("/{project}", h.projectPage)
	mux.HandleFunc("/{project}/generate", h.generate)
	mux.HandleFunc("/{project}/gallery", h.gallery)
	mux.HandleFunc("GET /{project}/element/{id}", h.elementPage)
	mux.HandleFunc("GET /{project}/element/{id}/image", h.serveImage)
	mux.HandleFunc("POST /{project}/jobs/cancel-all", h.cancelAllJobs)
	mux.HandleFunc("POST /{project}/element/{id}/generate-clone", h.generateClone)
	mux.HandleFunc("POST /{project}/element/{id}/regenerate-in-place", h.regenerateInPlace)
	mux.HandleFunc("/{project}/jobs/active", h.activeJobsJSON)
	mux.HandleFunc("/{project}/jobs/{id}", h.jobStatusJSON)
	mux.HandleFunc("/{project}/jobs/{id}/cancel", h.cancelJob)
	mux.HandleFunc("/{project}/settings", h.projectSettings)
	mux.HandleFunc("POST /{project}/settings", h.updateProjectSettings)
	mux.HandleFunc("POST /{project}/create", h.createProject)
	mux.HandleFunc("/create-project", h.createProjectFromWelcome)
	mux.HandleFunc("/switch-backend", h.switchBackend)
	mux.HandleFunc("POST /{project}/element/{id}/delete", h.deleteElement)
	mux.HandleFunc("POST /{project}/delete", h.deleteProject)
	mux.HandleFunc("/{project}/elements", h.elementsJSON)

	return h, mux, db
}

func TestCreateProject_NewProject(t *testing.T) {
	_, mux := setupHandler(t)

	// POST to a non-existent project should create it and redirect (303).
	req := httptest.NewRequest(http.MethodPost, "/new-project/create", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	t.Logf("POST status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	location := rec.Header().Get("Location")
	if location != "/basic/new-project" {
		t.Errorf("location = %q, want %q", location, "/basic/new-project")
	}

	// Follow the redirect — should land on the project dashboard (200),
	// not the create form.
	req = httptest.NewRequest(http.MethodGet, "/basic/new-project", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET redirect: status = %d, want %d", rec.Code, http.StatusOK)
	}

	body, _ := io.ReadAll(rec.Body)
	if strings.Contains(string(body), "Create this project to start generating?") {
		t.Error("redirected page still shows create button — project was not created")
	}
}

func TestCreateProject_Idempotent(t *testing.T) {
	_, mux := setupHandler(t)

	// Create a new project.
	req := httptest.NewRequest(http.MethodPost, "/another-project/create", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("first create: status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	// Create the same project again — should still redirect (idempotent via INSERT OR IGNORE).
	req = httptest.NewRequest(http.MethodPost, "/another-project/create", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("second create: status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
}

func TestCreateProject_MethodNotAllowed(t *testing.T) {
	_, mux := setupHandler(t)

	// GET /new-project/create — no exact GET match for this path.
	// It falls through to GET /{project} with project="new-project/create"
	// which validates and rejects the name (slash not allowed).
	req := httptest.NewRequest(http.MethodGet, "/new-project/create", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Should get a 200 error page (not a crash or unexpected redirect).
	if rec.Code != http.StatusOK {
		t.Errorf("GET /new-project/create: status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestCreateProject_InvalidName(t *testing.T) {
	_, mux := setupHandler(t)

	// Project name starting with '-' is invalid.
	req := httptest.NewRequest(http.MethodPost, "/-bad-name/create", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("POST /-bad-name/create: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCreateProjectFromWelcome_JSONParsing(t *testing.T) {
	_, mux := setupHandler(t)

	// Invalid JSON body should return 400.
	req := httptest.NewRequest(http.MethodPost, "/create-project", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	// Valid JSON should parse name and create project (303 redirect).
	body := strings.NewReader(`{"name": "json-project"}`)
	req = httptest.NewRequest(http.MethodPost, "/create-project", body)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("valid JSON: status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	location := rec.Header().Get("Location")
	if location != "/basic/json-project" {
		t.Errorf("location = %q, want %q", location, "/basic/json-project")
	}
}
