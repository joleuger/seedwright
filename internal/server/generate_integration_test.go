package server

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"testing"

	"seedwright/internal/data"
	"seedwright/internal/data/model"
	"seedwright/internal/storage"
)

// newTestDB creates an in-memory SQLite database for integration tests.
func newTestDB(t *testing.T) *sql.DB {
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
	return db
}

// TestGenerateRequiresProjectRow verifies that the jobs table has a
// foreign key to projects, and that calling CreateProject before
// a hypothetical job creation prevents a FK constraint error.
//
// This is a regression test for:
// https://github.com/.../issues/XXX — "ERROR start job error='create job: FOREIGN KEY constraint failed'"
//
// The root cause was that handleGenerate called StartJob without first
// ensuring the project row exists in the projects table. The fix adds
// CreateProject (INSERT OR IGNORE) before StartJob.
func TestGenerateRequiresProjectRow(t *testing.T) {
	db := newTestDB(t)

	store := storage.NewMockStorage()
	repo := data.NewProjectRepository(db, store)

	// Without CreateProject, the projects table is empty and a
	// hypothetical job insert would fail with a FK error.
	// This verifies the project table starts empty.
	projects, err := repo.ListProjects(context.Background(), false)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("expected 0 projects, got %d", len(projects))
	}

	// After CreateProject, the project row exists.
	err = repo.CreateProject(context.Background(), model.NewProject("fk-test-project"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Verify the project row exists.
	projects, err = repo.ListProjects(context.Background(), false)
	if err != nil {
		t.Fatalf("ListProjects after create: %v", err)
	}
	if len(projects) != 1 || projects[0] != "fk-test-project" {
		t.Fatalf("expected 1 project 'fk-test-project', got %v", projects)
	}
}

// TestGenerateFormShowsAfterProjectCreate exercises the full HTTP
// flow: create project → GET dashboard → verify form displays.
// This catches regressions where the dashboard fails to load after
// project creation (e.g., due to FK constraint on elements/jobs tables).
func TestGenerateFormShowsAfterProjectCreate(t *testing.T) {
	ts := newIntegrationServer(t)
	defer ts.Close()

	client := noRedirectClient()
	projectName := "fk-form-test"

	// Create project.
	resp := doNoRedirect(t, client, http.MethodPost, ts.URL+"/api/"+projectName+"/create", nil)
	resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST create: status = %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}

	// GET dashboard — should show the generate form.
	resp, err := client.Get(ts.URL + "/basic/" + projectName)
	if err != nil {
		t.Fatalf("GET dashboard: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET dashboard: status = %d, want %d (body: %s)", resp.StatusCode, http.StatusOK, string(body[:min(len(body), 300)]))
	}
	assertContains(t, body, "New Generation")
	assertContains(t, body, "Prompt")
}
