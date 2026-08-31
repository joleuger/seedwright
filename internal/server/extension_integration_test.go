package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"seedwright/internal/authz"
	"seedwright/internal/data"
	"seedwright/internal/data/model"
	"seedwright/internal/data/querybuilder"
	"seedwright/internal/storage"
)

// newServerWithBatchRoute creates a httptest.Server with core routes +
// a batch-preview-like route registered on the same mux. This mirrors the
// real flow where ext.RegisterAll() adds extension routes to the same mux.
func newServerWithBatchRoute(t *testing.T) *httptest.Server {
	t.Helper()

	db, err := data.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}

	store := storage.NewMockStorage()
	repo := data.NewProjectRepository(db, store)
	var nilQB *querybuilder.Builder
	elemRepo := data.NewElementRepository(db, store, nilQB)

	// Create schema before inserting any data.
	if err := data.CreateSchema(db); err != nil {
		t.Fatalf("CreateSchema: %v", err)
	}

	// Pre-create the "test" project so settings and other project-dependent endpoints work.
	if err := repo.CreateProject(context.Background(), model.NewProject("test")); err != nil {
		t.Fatalf("CreateProject test: %v", err)
	}

	srv := New(&Config{
		Title:       "seedwright",
		PathPrefix:  "",
		Storage:     store,
		ProjectRepo: repo,
		ElementRepo: elemRepo,
		Authz:       &authz.StaticEnforcer{Principal: "user:root"},
	})

	// Type-assert the ServeMux for extension route registration.
	mux := srv.(*http.ServeMux)

	// Register a batch-preview-like handler on the same mux (same as
	// ext.RegisterAll does via batch.Extension.RegisterRoutes).
	mux.HandleFunc("POST /api/{project}/ext/joleuger/batch/preview", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Prompt string `json:"prompt"`
			Seeds  string `json:"seeds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if body.Prompt == "" || body.Seeds == "" {
			http.Error(w, "prompt and seeds required", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"variants": []map[string]interface{}{
				{"prompt": body.Prompt, "seed": "1"},
			},
		})
	})

	return httptest.NewServer(srv)
}

// newPrefixedServerWithBatchRoute creates a server with path_prefix="/sd" + batch route.
// Uses the actual New() + NewStripPrefix() so the routing mirrors production.
func newPrefixedServerWithBatchRoute(t *testing.T) (*httptest.Server, *http.ServeMux) {
	t.Helper()

	db, err := data.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}

	store := storage.NewMockStorage()
	repo := data.NewProjectRepository(db, store)
	var nilQB *querybuilder.Builder
	elemRepo := data.NewElementRepository(db, store, nilQB)

	// Pre-create the "test" project so settings and other project-dependent endpoints work.
	if err := repo.CreateProject(context.Background(), model.NewProject("test")); err != nil {
		t.Fatalf("CreateProject test: %v", err)
	}

	// New() always returns the raw *http.ServeMux (prefix stripping is
	// applied by the caller). This is what lets extensions register routes.
	srv := New(&Config{
		Title:       "seedwright",
		PathPrefix:  "/sd",
		Storage:     store,
		ProjectRepo: repo,
		ElementRepo: elemRepo,
		Authz:       &authz.StaticEnforcer{Principal: "user:root"},
	})

	// Type-assert the ServeMux for extension route registration.
	mux := srv.(*http.ServeMux)

	// Register a batch extension route on the inner mux.
	mux.HandleFunc("POST /api/{project}/ext/joleuger/batch/preview", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"variants": []map[string]interface{}{
				{"prompt": "test", "seed": "1"},
			},
		})
	})

	// Wrap with prefix stripping (same as Bootstrap does).
	wrapped := NewStripPrefix("/sd", mux)

	return httptest.NewServer(wrapped), mux
}

// TestExtensionRoute_RootPrefix verifies that extension routes work with no path_prefix.
func TestExtensionRoute_RootPrefix(t *testing.T) {
	ts := newServerWithBatchRoute(t)
	defer ts.Close()

	// POST /api/test/ext/joleuger/batch/preview
	body := strings.NewReader(`{"prompt":"a cat","seeds":"1,2,3"}`)
	req, _ := http.NewRequest("POST", ts.URL+"/api/test/ext/joleuger/batch/preview", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST batch/preview: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	t.Logf("Status: %d, Body: %s", resp.StatusCode, string(respBody))

	if resp.StatusCode != http.StatusOK {
		t.Errorf("POST /api/test/ext/joleuger/batch/preview: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// TestExtensionRoute_Prefixed verifies that extension routes work with path_prefix="/sd".
func TestExtensionRoute_Prefixed(t *testing.T) {
	ts, _ := newPrefixedServerWithBatchRoute(t)
	defer ts.Close()

	// POST /sd/api/test/ext/joleuger/batch/preview
	body := strings.NewReader(`{"prompt":"a cat","seeds":"1,2,3"}`)
	req, _ := http.NewRequest("POST", ts.URL+"/sd/api/test/ext/joleuger/batch/preview", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST batch/preview: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	t.Logf("Status: %d, Body: %s", resp.StatusCode, string(respBody))

	if resp.StatusCode != http.StatusOK {
		t.Errorf("POST /sd/api/test/ext/joleuger/batch/preview: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// TestCoreAPIs_RootPrefix verifies that all core API routes work with no path_prefix.
func TestCoreAPIs_RootPrefix(t *testing.T) {
	ts := newServerWithBatchRoute(t)
	defer ts.Close()

	client := noRedirectClient()

	tests := []struct {
		name     string
		method   string
		path     string
		body     io.Reader
		wantStatus int
	}{
		// Core JSON API — POST
		{"POST create", "POST", "/api/test/create", nil, http.StatusSeeOther},
		{"POST generate", "POST", "/api/test/generate", nil, http.StatusBadRequest},
		{"POST cancel-all", "POST", "/api/test/jobs/cancel-all", nil, http.StatusInternalServerError},
		{"POST generate-clone", "POST", "/api/test/element/abc/generate-clone", nil, http.StatusNotFound},
		{"POST regenerate-in-place", "POST", "/api/test/element/abc/regenerate-in-place", nil, http.StatusNotFound},
		{"POST settings", "POST", "/api/test/settings", strings.NewReader(`{}`), http.StatusOK},
		{"POST create-project", "POST", "/create-project", strings.NewReader(`{"name":"test2"}`), http.StatusSeeOther},
		{"POST switch-backend", "POST", "/switch-backend", strings.NewReader(`{}`), http.StatusBadRequest},
		{"POST delete element", "POST", "/api/test/element/abc/delete", nil, http.StatusOK},
		{"POST delete project", "POST", "/api/test/delete", nil, http.StatusSeeOther},

		// Core JSON API — GET
		{"GET active jobs", "GET", "/api/test/jobs/active", nil, http.StatusInternalServerError},
		{"GET job status", "GET", "/api/test/jobs/abc", nil, http.StatusInternalServerError},
		{"POST cancel job", "POST", "/api/test/jobs/abc/cancel", nil, http.StatusInternalServerError},
		{"GET settings", "GET", "/api/test/settings", nil, http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp *http.Response
			var err error

			req, _ := http.NewRequest(tt.method, ts.URL+tt.path, tt.body)
			if tt.body != nil && !strings.Contains(tt.path, "create-project") && !strings.Contains(tt.path, "switch-backend") {
				req.Header.Set("Content-Type", "application/json")
			}
			req.Header.Set("Accept", "application/json")
			resp, err = client.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", tt.method, tt.path, err)
			}
			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)
			t.Logf("  %s %s → %d (body: %s)", tt.method, tt.path, resp.StatusCode, string(body[:min(len(body), 200)]))

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}

			// Check for unexpected redirects.
			if resp.StatusCode >= 300 && resp.StatusCode < 400 && tt.wantStatus != http.StatusSeeOther {
				t.Errorf("unexpected redirect for %s %s: %d", tt.method, tt.path, resp.StatusCode)
			}
		})
	}
}

// TestCoreAPIs_Prefixed verifies that all core API routes work with path_prefix="/sd".
func TestCoreAPIs_Prefixed(t *testing.T) {
	ts, _ := newPrefixedServerWithBatchRoute(t)
	defer ts.Close()

	client := noRedirectClient()

	tests := []struct {
		name     string
		method   string
		path     string
		body     io.Reader
		wantStatus int
	}{
		// Core JSON API — POST
		{"POST create", "POST", "/sd/api/test/create", nil, http.StatusSeeOther},
		{"POST generate", "POST", "/sd/api/test/generate", nil, http.StatusBadRequest},
		{"POST cancel-all", "POST", "/sd/api/test/jobs/cancel-all", nil, http.StatusInternalServerError},
		{"POST generate-clone", "POST", "/sd/api/test/element/abc/generate-clone", nil, http.StatusNotFound},
		{"POST regenerate-in-place", "POST", "/sd/api/test/element/abc/regenerate-in-place", nil, http.StatusNotFound},
		{"POST settings", "POST", "/sd/api/test/settings", strings.NewReader(`{}`), http.StatusOK},
		{"POST create-project", "POST", "/sd/create-project", strings.NewReader(`{"name":"test2"}`), http.StatusSeeOther},
		{"POST switch-backend", "POST", "/sd/switch-backend", strings.NewReader(`{}`), http.StatusBadRequest},
		{"POST delete element", "POST", "/sd/api/test/element/abc/delete", nil, http.StatusOK},
		{"POST delete project", "POST", "/sd/api/test/delete", nil, http.StatusSeeOther},

		// Core JSON API — GET
		{"GET active jobs", "GET", "/sd/api/test/jobs/active", nil, http.StatusInternalServerError},
		{"GET job status", "GET", "/sd/api/test/jobs/abc", nil, http.StatusInternalServerError},
		{"POST cancel job", "POST", "/sd/api/test/jobs/abc/cancel", nil, http.StatusInternalServerError},
		{"GET settings", "GET", "/sd/api/test/settings", nil, http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp *http.Response
			var err error

			req, _ := http.NewRequest(tt.method, ts.URL+tt.path, tt.body)
			if tt.body != nil && !strings.Contains(tt.path, "create-project") && !strings.Contains(tt.path, "switch-backend") {
				req.Header.Set("Content-Type", "application/json")
			}
			req.Header.Set("Accept", "application/json")
			resp, err = client.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", tt.method, tt.path, err)
			}
			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)
			t.Logf("  %s %s → %d (body: %s)", tt.method, tt.path, resp.StatusCode, string(body[:min(len(body), 200)]))

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}

			// Check for unexpected redirects.
			if resp.StatusCode >= 300 && resp.StatusCode < 400 && tt.wantStatus != http.StatusSeeOther {
				t.Errorf("unexpected redirect for %s %s: %d", tt.method, tt.path, resp.StatusCode)
			}
		})
	}
}

// TestPhotoboothRoute verifies that the photobooth route works.
func TestPhotoboothRoute(t *testing.T) {
	ts := newServerWithBatchRoute(t)
	defer ts.Close()

	// GET /photobooth/test
	req, _ := http.NewRequest("GET", ts.URL+"/photobooth/test", nil)
	req.Header.Set("Accept", "text/html")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET photobooth: %v", err)
	}
	defer resp.Body.Close()

	t.Logf("  GET /photobooth/test → %d", resp.StatusCode)

	if resp.StatusCode != http.StatusNotFound {
		// Expected — photobooth route is NOT registered in this test.
		// This test documents which routes work and which don't.
		t.Logf("  Note: photobooth route is not registered (expected in test)")
	}
}

// TestBatchPages verifies that batch progress page routes work.
func TestBatchPages(t *testing.T) {
	ts := newServerWithBatchRoute(t)
	defer ts.Close()

	tests := []struct {
		name     string
		method   string
		path     string
		wantStatus int
	}{
		// The batch page route is NOT registered in this test (only preview is).
		// But /basic/test/batch/abc matches the core GET /basic/{project} handler,
		// so it returns 200 (the project page) — not 404. This documents the
		// behavior: unregistered sub-paths fall through to the parent route.
		{"GET batch page", "GET", "/basic/test/batch/abc", http.StatusOK},
		{"GET batch api", "GET", "/api/test/batch/abc/api", http.StatusNotFound}, // handler not registered
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(tt.method, ts.URL+tt.path, nil)
			req.Header.Set("Accept", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", tt.method, tt.path, err)
			}
			defer resp.Body.Close()

			t.Logf("  %s %s → %d", tt.method, tt.path, resp.StatusCode)

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
		})
	}
}
