package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"seedwright/internal/authz"
	"seedwright/internal/data"
	"seedwright/internal/data/querybuilder"
	"seedwright/internal/storage"
)

func TestRootNoConflictWithSpecificPaths(t *testing.T) {
	// Create a minimal mux with the same patterns used in New()
	// but without a full handler tree.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("root"))
	})
	mux.HandleFunc("GET /api/{project}/settings", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("settings"))
	})
	mux.HandleFunc("POST /api/{project}/generate", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("generate"))
	})
	mux.HandleFunc("POST /api/{project}/ext/joleuger/batch/preview", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("batch-preview"))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Test 1: GET / → root
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "root" {
		t.Errorf("GET / = %d %q, want 200 'root'", resp.StatusCode, string(body))
	}
	t.Logf("  OK: GET / → 200 'root'")

	// Test 2: GET /api/test/settings → settings
	resp, err = http.Get(ts.URL + "/api/test/settings")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "settings" {
		t.Errorf("GET /api/test/settings = %d %q, want 200 'settings'", resp.StatusCode, string(body))
	}
	t.Logf("  OK: GET /api/test/settings → 200 'settings'")

	// Test 3: POST /api/test/ext/joleuger/batch/preview → batch-preview
	req, _ := http.NewRequest("POST", ts.URL+"/api/test/ext/joleuger/batch/preview", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "batch-preview" {
		t.Errorf("POST /api/test/ext/joleuger/batch/preview = %d %q, want 200 'batch-preview'", resp.StatusCode, string(body))
	}
	t.Logf("  OK: POST /api/test/ext/joleuger/batch/preview → 200 'batch-preview'")

	// Test 4: POST /api/test/generate → generate
	req, _ = http.NewRequest("POST", ts.URL+"/api/test/generate", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "generate" {
		t.Errorf("POST /api/test/generate = %d %q, want 200 'generate'", resp.StatusCode, string(body))
	}
	t.Logf("  OK: POST /api/test/generate → 200 'generate'")
}

func TestRootPathOnly(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("root"))
	})

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "root" {
		t.Errorf("GET / = %d %q, want 200 'root'", rec.Code, rec.Body.String())
	}
	t.Logf("  OK: GET / → 200 'root'")
}

func TestRootDoesNotCatchOtherPaths(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("root"))
	})

	// GET /api/test → should NOT match GET /{$}, so 404
	req := httptest.NewRequest("GET", "/api/test", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/test = %d, want 404 (root should NOT match other paths)", rec.Code)
	}
	t.Logf("  OK: GET /api/test → 404")
}

func TestRootDoesNotCatchPOST(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("root"))
	})
	mux.HandleFunc("POST /api/test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("api"))
	})

	// POST /api/test → should match POST /api/test, not GET /{$}
	req := httptest.NewRequest("POST", "/api/test", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "api" {
		t.Errorf("POST /api/test = %d %q, want 200 'api'", rec.Code, rec.Body.String())
	}
	t.Logf("  OK: POST /api/test → 200 'api'")
}

func TestNewServerWithExtensionRoute(t *testing.T) {
	// Create a server using the actual New() function, then add an extension route
	// on the same mux. This tests the real routing behavior with the full handler tree.
	db, err := data.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}

	store := storage.NewMockStorage()
	repo := data.NewProjectRepository(db, store)
	var nilQB *querybuilder.Builder
	elemRepo := data.NewElementRepository(db, store, nilQB)

	srv := New(&Config{
		Title:       "seedwright",
		Storage:     store,
		ProjectRepo: repo,
		ElementRepo: elemRepo,
		Authz:       &authz.StaticEnforcer{Principal: "user:root"},
	})

	// Type-assert the ServeMux for extension route registration.
	mux := srv.(*http.ServeMux)

	// Register an extension-like route on the same mux.
	mux.HandleFunc("POST /api/{project}/ext/joleuger/batch/preview", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("batch-preview-ok"))
	})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	// POST to extension route — should NOT be intercepted by GET /{$}
	req, _ := http.NewRequest("POST", ts.URL+"/api/test/ext/joleuger/batch/preview", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "batch-preview-ok" {
		t.Errorf("POST /api/test/ext/joleuger/batch/preview = %d %q, want 200 'batch-preview-ok'", resp.StatusCode, string(body))
	}
	t.Logf("  OK: POST /api/test/ext/joleuger/batch/preview → 200 'batch-preview-ok'")
}
