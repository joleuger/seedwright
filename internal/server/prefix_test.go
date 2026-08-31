package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"seedwright/internal/authz"
	"seedwright/internal/data"
	"seedwright/internal/data/querybuilder"
	"seedwright/internal/storage"
)

// newPrefixedIntegrationServer creates a server with a configurable path prefix.
func newPrefixedIntegrationServer(t *testing.T, prefix string) *httptest.Server {
	t.Helper()

	db, err := data.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
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
	var nilQB *querybuilder.Builder
	elemRepo := data.NewElementRepository(db, store, nilQB)

	srv := New(&Config{
		Title:       "seedwright",
		PathPrefix:  prefix,
		Storage:     store,
		ProjectRepo: repo,
		ElementRepo: elemRepo,
		Authz:       &authz.StaticEnforcer{Principal: "user:root"},
	})

	// Apply prefix stripping (same as Bootstrap does).
	var handler http.Handler = srv
	if prefix != "" {
		handler = NewStripPrefix(prefix, srv)
	}

	return httptest.NewServer(handler)
}

func TestPrefix_RootRedirect(t *testing.T) {
	ts := newPrefixedIntegrationServer(t, "/sd")
	defer ts.Close()

	client := noRedirectClient()

	// GET /sd/ → 301 redirect to /sd/basic/
	resp, err := client.Get(ts.URL + "/sd/")
	if err != nil {
		t.Fatalf("GET /sd/: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("GET /sd/ status = %d, want %d", resp.StatusCode, http.StatusMovedPermanently)
	}
	location := resp.Header.Get("Location")
	if location != "/sd/basic/" {
		t.Fatalf("GET /sd/ Location = %q, want %q", location, "/sd/basic/")
	}
	t.Logf("  OK: /sd/ → %s (301)", location)
}

func TestPrefix_BasicNoSlashRedirect(t *testing.T) {
	ts := newPrefixedIntegrationServer(t, "/sd")
	defer ts.Close()

	client := noRedirectClient()

	// GET /sd/basic → 301 redirect to /sd/basic/
	resp, err := client.Get(ts.URL + "/sd/basic")
	if err != nil {
		t.Fatalf("GET /sd/basic: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("GET /sd/basic status = %d, want %d", resp.StatusCode, http.StatusMovedPermanently)
	}
	location := resp.Header.Get("Location")
	if location != "/sd/basic/" {
		t.Fatalf("GET /sd/basic Location = %q, want %q", location, "/sd/basic/")
	}
	t.Logf("  OK: /sd/basic → %s (301)", location)
}

func TestPrefix_WelcomePage(t *testing.T) {
	ts := newPrefixedIntegrationServer(t, "/sd")
	defer ts.Close()

	client := noRedirectClient()

	// GET /sd/basic/ → 200, welcome page
	resp, err := client.Get(ts.URL + "/sd/basic/")
	if err != nil {
		t.Fatalf("GET /sd/basic/: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sd/basic/ status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	assertContains(t, body, "Welcome to seedwright")
	t.Logf("  OK: /sd/basic/ returns welcome page (200)")
}

func TestPrefix_CreateProjectAndDashboard(t *testing.T) {
	ts := newPrefixedIntegrationServer(t, "/sd")
	defer ts.Close()

	client := noRedirectClient()

	// Step 1: Create project via API
	resp := doNoRedirect(t, client, http.MethodPost, ts.URL+"/sd/api/testprefix/create", nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST create status = %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}
	location := resp.Header.Get("Location")
	if location != "/sd/basic/testprefix" {
		t.Fatalf("POST create Location = %q, want %q", location, "/sd/basic/testprefix")
	}
	t.Logf("  OK: POST /sd/api/testprefix/create → %s (303)", location)

	// Step 2: Follow redirect to dashboard
	resp, err := client.Get(ts.URL + location)
	if err != nil {
		t.Fatalf("GET /sd/basic/testprefix: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET dashboard status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	assertContains(t, body, "Dashboard")
	assertNotContains(t, body, "does not exist")
	t.Logf("  OK: /sd/basic/testprefix returns dashboard (200)")
}

func TestPrefix_APIRoutes(t *testing.T) {
	ts := newPrefixedIntegrationServer(t, "/sd")
	defer ts.Close()

	client := noRedirectClient()
	projectName := "prefix-api-test"

	// Create the project first
	resp := doNoRedirect(t, client, http.MethodPost, ts.URL+"/sd/api/"+projectName+"/create", nil)
	resp.Body.Close()

	// GET /sd/api/{project}/settings → 200, JSON
	resp, err := client.Get(ts.URL + "/sd/api/" + projectName + "/settings")
	if err != nil {
		t.Fatalf("GET settings: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET settings status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	assertContains(t, body, `"default_backend"`)
	t.Logf("  OK: GET /sd/api/%s/settings returns JSON (200)", projectName)
}

func TestPrefix_UntaggedPathReturns404(t *testing.T) {
	ts := newPrefixedIntegrationServer(t, "/sd")
	defer ts.Close()

	client := noRedirectClient()

	// GET /other/path → 404 (not under the prefix)
	resp, err := client.Get(ts.URL + "/other/path")
	if err != nil {
		t.Fatalf("GET /other/path: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /other/path status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
	t.Logf("  OK: /other/path → 404")
}

func TestPrefix_TemplateURLsIncludePrefix(t *testing.T) {
	ts := newPrefixedIntegrationServer(t, "/sd")
	defer ts.Close()

	client := noRedirectClient()

	// Create a project so the welcome page has links to render.
	resp := doNoRedirect(t, client, http.MethodPost, ts.URL+"/sd/api/testprefix-links/create", nil)
	resp.Body.Close()

	// GET /sd/basic/ → welcome page with correct URLs
	resp, err := client.Get(ts.URL + "/sd/basic/")
	if err != nil {
		t.Fatalf("GET /sd/basic/: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sd/basic/ status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// The welcome page should have project links prefixed with /sd/basic/
	assertContains(t, body, `href="/sd/basic/testprefix-links"`)
	t.Logf("  OK: welcome page links use /sd/basic/ prefix")
}

func TestPrefix_EmptyPrefixIsNoop(t *testing.T) {
	ts := newPrefixedIntegrationServer(t, "")
	defer ts.Close()

	client := noRedirectClient()

	// GET /basic/ → 200 (no prefix means root-level routing)
	resp, err := client.Get(ts.URL + "/basic/")
	if err != nil {
		t.Fatalf("GET /basic/: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /basic/ status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	assertContains(t, body, "Welcome to seedwright")
	t.Logf("  OK: empty prefix serves at root /basic/ (200)")
}

func TestPrefix_PostCreateRedirectUsesPrefix(t *testing.T) {
	ts := newPrefixedIntegrationServer(t, "/sd")
	defer ts.Close()

	client := noRedirectClient()

	// POST /sd/api/testprefix2/create → 303 redirect with prefix in Location
	resp := doNoRedirect(t, client, http.MethodPost, ts.URL+"/sd/api/testprefix2/create", nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST create status = %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}
	location := resp.Header.Get("Location")
	// The redirect Location must include the prefix
	if location != "/sd/basic/testprefix2" {
		t.Fatalf("POST create Location = %q, want %q", location, "/sd/basic/testprefix2")
	}
	// The location must NOT include double-prefix
	if strings.HasPrefix(location, "/sd/sd/") {
		t.Fatalf("POST create Location = %q contains double-prefix", location)
	}
	t.Logf("  OK: POST create Location = %s (includes prefix, no double-prefix)", location)
}

func TestPrefix_DeleteProjectRedirectUsesPrefix(t *testing.T) {
	ts := newPrefixedIntegrationServer(t, "/sd")
	defer ts.Close()

	client := noRedirectClient()
	projectName := "prefix-delete-test"

	// Create the project first
	resp := doNoRedirect(t, client, http.MethodPost, ts.URL+"/sd/api/"+projectName+"/create", nil)
	resp.Body.Close()

	// POST /sd/api/{project}/delete → 303 redirect to / (root)
	resp = doNoRedirect(t, client, http.MethodPost, ts.URL+"/sd/api/"+projectName+"/delete", nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST delete status = %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}
	location := resp.Header.Get("Location")
	if location != "/" {
		t.Fatalf("POST delete Location = %q, want %q", location, "/")
	}
	t.Logf("  OK: POST delete redirects to / (303)")
}
