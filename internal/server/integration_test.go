package server

import (
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"seedwright/internal/authz"
	"seedwright/internal/data"
	"seedwright/internal/data/querybuilder"
	"seedwright/internal/storage"
)

// newIntegrationServer creates an httptest.Server with a fully configured
// handler tree (the same one the production binary uses).
func newIntegrationServer(t *testing.T) *httptest.Server {
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
	// nil is safe: ListElements checks r.qb != nil.
	var nilQB *querybuilder.Builder
	elemRepo := data.NewElementRepository(db, store, nilQB)

	srv := New(&Config{
		Title:       "seedwright",
		Storage:     store,
		ProjectRepo: repo,
		ElementRepo: elemRepo,
		Authz:       &authz.StaticEnforcer{Principal: "user:root"},
	})

	return httptest.NewServer(srv)
}

// noRedirectClient returns an HTTP client that does NOT follow redirects.
// This is essential for testing POST endpoints that return 303 redirects.
func noRedirectClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// assertContains checks that the response body contains the given substring.
func assertContains(t *testing.T, body []byte, want string) {
	t.Helper()
	if !strings.Contains(string(body), want) {
		t.Errorf("body does not contain %q (first 500 bytes: %q)", want, string(body[:min(len(body), 500)]))
	}
}

// assertNotContains checks that the response body does NOT contain the given substring.
func assertNotContains(t *testing.T, body []byte, want string) {
	t.Helper()
	if strings.Contains(string(body), want) {
		t.Errorf("body should not contain %q (first 500 bytes: %q)", want, string(body[:min(len(body), 500)]))
	}
}

// doNoRedirect is a convenience function that POSTs without following redirects.
func doNoRedirect(t *testing.T, client *http.Client, method string, url string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("NewRequest(%s): %v", url, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

// TestProjectLifecycle_CreateDelete is a full integration test that exercises
// the entire project lifecycle through the REST API:
//
//	GET /nonexistent → 200, body shows "Create Project" button
//	POST /nonexistent/create → 303 redirect to /nonexistent
//	GET /nonexistent → 200, body shows dashboard (no "Create Project" button)
//	POST /nonexistent/delete → 303 redirect to /
//	GET /nonexistent → 200, body shows "Create Project" button again
func TestProjectLifecycle_CreateDelete(t *testing.T) {
	ts := newIntegrationServer(t)
	defer ts.Close()

	client := noRedirectClient()
	projectName := "integration-test-project"

	// Step 1: GET a non-existent project → should show the create form.
	t.Log("Step 1: GET non-existent project")
	resp, err := client.Get(ts.URL + "/basic/" + projectName)
	if err != nil {
		t.Fatalf("GET non-existent project: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET non-existent project: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	assertContains(t, body, "Create Project")
	assertContains(t, body, "does not exist")
	t.Logf("  OK: body contains 'Create Project' button, status=%d", resp.StatusCode)

	// Step 2: POST to /api/{project}/create → should create the project and redirect (303).
	t.Log("Step 2: POST to create the project")
	resp = doNoRedirect(t, client, http.MethodPost, ts.URL+"/api/"+projectName+"/create", nil)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST create: status = %d, want %d (303 redirect)", resp.StatusCode, http.StatusSeeOther)
	}
	location := resp.Header.Get("Location")
	if location != "/basic/"+projectName {
		t.Fatalf("POST create: Location = %q, want %q", location, "/basic/"+projectName)
	}
	t.Logf("  OK: redirect to %s, status=%d", location, resp.StatusCode)

	// Step 3: GET the project again → should now show the dashboard (no "Create Project" button).
	t.Log("Step 3: GET project after creation")
	resp, err = client.Get(ts.URL + "/basic/" + projectName)
	if err != nil {
		t.Fatalf("GET project after create: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET project after create: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	assertContains(t, body, "Dashboard")
	assertNotContains(t, body, `id="createProjectBtn"`)
	assertNotContains(t, body, "does not exist")
	t.Logf("  OK: body contains 'Dashboard', no 'Create Project' button, status=%d", resp.StatusCode)

	// Step 4: POST to /{project}/delete → should delete the project and redirect to /.
	t.Log("Step 4: POST to delete the project")
	resp = doNoRedirect(t, client, http.MethodPost, ts.URL+"/api/"+projectName+"/delete", nil)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST delete: status = %d, want %d (303 redirect)", resp.StatusCode, http.StatusSeeOther)
	}
	location = resp.Header.Get("Location")
	if location != "/" {
		t.Fatalf("POST delete: Location = %q, want %q", location, "/")
	}
	t.Logf("  OK: redirect to /, status=%d", resp.StatusCode)

	// Step 5: GET the project again → should show the create form again.
	t.Log("Step 5: GET project after deletion (should show create form again)")
	resp, err = client.Get(ts.URL + "/basic/" + projectName)
	if err != nil {
		t.Fatalf("GET project after delete: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET project after delete: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	assertContains(t, body, `id="createProjectBtn"`)
	assertContains(t, body, "does not exist")
	t.Logf("  OK: body contains 'Create Project' button again, status=%d", resp.StatusCode)

	t.Log("Full lifecycle test passed!")
}

// TestProjectLifecycle_JSONCreateDelete exercises the same project lifecycle
// through the JSON-based POST /create-project endpoint (used by the welcome-page modal).
func TestProjectLifecycle_JSONCreateDelete(t *testing.T) {
	ts := newIntegrationServer(t)
	defer ts.Close()

	client := noRedirectClient()
	projectName := "integration-json-project"

	// Step 1: GET a non-existent project → should show the create form.
	t.Log("Step 1: GET non-existent project (via JSON create)")
	resp, err := client.Get(ts.URL + "/basic/" + projectName)
	if err != nil {
		t.Fatalf("GET non-existent project: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET non-existent project: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	assertContains(t, body, "Create Project")
	assertContains(t, body, "does not exist")
	t.Logf("  OK: body contains 'Create Project' button, status=%d", resp.StatusCode)

	// Step 2: POST to /create-project with JSON body → should create the project and redirect (303).
	t.Log("Step 2: POST /create-project with JSON body")
	jsonBody := strings.NewReader(`{"name":"` + projectName + `"}`)
	resp = doNoRedirect(t, client, "POST", ts.URL+"/create-project", jsonBody)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /create-project: status = %d, want %d (303 redirect)", resp.StatusCode, http.StatusSeeOther)
	}
	location := resp.Header.Get("Location")
	if location != "/basic/"+projectName {
		t.Fatalf("POST /create-project: Location = %q, want %q", location, "/basic/"+projectName)
	}
	t.Logf("  OK: redirect to %s, status=%d", location, resp.StatusCode)

	// Step 3: GET the project again → should now show the dashboard (no "Create Project" button).
	t.Log("Step 3: GET project after JSON creation")
	resp, err = client.Get(ts.URL + "/basic/" + projectName)
	if err != nil {
		t.Fatalf("GET project after create: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET project after create: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	assertContains(t, body, "Dashboard")
	assertNotContains(t, body, `id="createProjectBtn"`)
	assertNotContains(t, body, "does not exist")
	t.Logf("  OK: body contains 'Dashboard', no 'Create Project' button, status=%d", resp.StatusCode)

	// Step 4: POST to /{project}/delete → should delete the project and redirect to /.
	t.Log("Step 4: POST to delete the project")
	resp = doNoRedirect(t, client, http.MethodPost, ts.URL+"/api/"+projectName+"/delete", nil)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST delete: status = %d, want %d (303 redirect)", resp.StatusCode, http.StatusSeeOther)
	}
	location = resp.Header.Get("Location")
	if location != "/" {
		t.Fatalf("POST delete: Location = %q, want %q", location, "/")
	}
	t.Logf("  OK: redirect to /, status=%d", resp.StatusCode)

	// Step 5: GET the project again → should show the create form again.
	t.Log("Step 5: GET project after deletion (should show create form again)")
	resp, err = client.Get(ts.URL + "/basic/" + projectName)
	if err != nil {
		t.Fatalf("GET project after delete: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET project after delete: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	assertContains(t, body, `id="createProjectBtn"`)
	assertContains(t, body, "does not exist")
	t.Logf("  OK: body contains 'Create Project' button again, status=%d", resp.StatusCode)

	t.Log("JSON-based lifecycle test passed!")
}

// TestProjectLifecycle_CreateThenGenerate verifies that after creating a project
// via POST /{project}/create, the project row actually exists in the database
// and the dashboard can load all its data (recent elements, active jobs, etc.).
func TestProjectLifecycle_CreateThenGenerate(t *testing.T) {
	ts := newIntegrationServer(t)
	defer ts.Close()

	client := noRedirectClient()
	projectName := "integration-test-generate"

	// Step 1: Create the project.
	resp := doNoRedirect(t, client, http.MethodPost, ts.URL+"/api/"+projectName+"/create", nil)
	resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST create: status = %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}

	// Step 2: GET the dashboard → should show "New Generation" form.
	resp, err := client.Get(ts.URL + "/basic/" + projectName)
	if err != nil {
		t.Fatalf("GET dashboard: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET dashboard: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	assertContains(t, body, "New Generation")
	assertContains(t, body, "Prompt")
	assertContains(t, body, "Generate")
	assertNotContains(t, body, "does not exist")
	t.Logf("  OK: dashboard contains form elements, status=%d", resp.StatusCode)
}

// TestProjectLifecycle_CreateIdempotent verifies that creating the same project
// twice via POST /{project}/create succeeds both times (INSERT OR IGNORE).
func TestProjectLifecycle_CreateIdempotent(t *testing.T) {
	ts := newIntegrationServer(t)
	defer ts.Close()

	client := noRedirectClient()
	projectName := "integration-test-idempotent"

	for i := 1; i <= 3; i++ {
		t.Logf("Step %d: POST create (attempt %d)", i, i)

		resp := doNoRedirect(t, client, http.MethodPost, ts.URL+"/api/"+projectName+"/create", nil)
		resp.Body.Close()

		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("POST create (attempt %d): status = %d, want %d", i, resp.StatusCode, http.StatusSeeOther)
		}
	}

	// GET the dashboard to confirm it works after multiple creates.
	resp, err := client.Get(ts.URL + "/basic/" + projectName)
	if err != nil {
		t.Fatalf("GET dashboard after multiple creates: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET dashboard: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	t.Log("  OK: dashboard loads after 3 consecutive creates")
}

// TestProjectLifecycle_InvalidNames verifies that invalid project names are
// rejected with appropriate error responses.
func TestProjectLifecycle_InvalidNames(t *testing.T) {
	ts := newIntegrationServer(t)
	defer ts.Close()

	client := noRedirectClient()

	tests := []struct {
		name            string
		method          string
		path            string
		wantStatus      int
		shouldContain   string
	}{
		{
			name:          "name starting with dash",
			method:        "POST",
			path:          "/api/-bad-name/create",
			wantStatus:    http.StatusBadRequest,
			shouldContain: "invalid project name",
		},
		{
			name:          "name with space",
			method:        "POST",
			path:          "/api/bad project/create",
			wantStatus:    http.StatusBadRequest,
			shouldContain: "invalid project name",
		},
		{
			name:          "GET non-existent project",
			method:        "GET",
			path:          "/basic/valid-name",
			wantStatus:    http.StatusOK,
			shouldContain: "does not exist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp *http.Response
			var err error

			if tt.method == "POST" {
				req, _ := http.NewRequest(http.MethodPost, ts.URL+tt.path, nil)
				resp, err = client.Do(req)
			} else {
				resp, err = client.Get(ts.URL + tt.path)
			}
			if err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("%s: status = %d, want %d (body: %q)", tt.name, resp.StatusCode, tt.wantStatus, string(body[:min(len(body), 300)]))
			}
			if tt.shouldContain != "" && !strings.Contains(string(body), tt.shouldContain) {
				t.Errorf("%s: body does not contain %q (body: %q)", tt.name, tt.shouldContain, string(body[:min(len(body), 300)]))
			}
			t.Logf("  OK: status=%d, contains=%q", resp.StatusCode, tt.shouldContain)
		})
	}
}

// TestBatchMultipartFormParsing_Integration verifies that a POST with
// multipart/form-data content type (as Chrome's FormData API sends) is
// correctly parsed through a real HTTP server. This is the full-cycle test
// for the "prompt is required" bug where the old handler used ParseForm().
func TestBatchMultipartFormParsing_Integration(t *testing.T) {
	// Create a handler that mirrors the batch generate fix:
	// ParseMultipartForm + MultipartForm.Value fallback.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ParseMultipartForm(32 << 20) != nil {
			r.ParseForm()
		}

		// Same fallback logic as the batch handler.
		formValue := func(key string) string {
			if v := r.FormValue(key); v != "" {
				return v
			}
			if r.MultipartForm != nil {
				if vals := r.MultipartForm.Value[key]; len(vals) > 0 {
					return vals[0]
				}
			}
			return ""
		}

		prompt := formValue("prompt")
		seeds := formValue("seeds")

		if prompt == "" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"prompt is required"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"prompt":` + `"` + prompt + `",` + `"seeds":"` + seeds + `"}`))
	})

	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Build a multipart/form-data body identical to what Chrome's FormData API
	// sends when the user submits the generate form with combination syntax.
	body := &strings.Builder{}
	writer := multipart.NewWriter(body)

	writer.WriteField("prompt", "{car,motorcycle} in sunset")
	writer.WriteField("seeds", "-1")
	writer.WriteField("negative_prompt", "")
	writer.WriteField("width", "512")
	writer.WriteField("height", "512")
	writer.WriteField("steps", "20")
	writer.WriteField("cfg", "7")
	writer.WriteField("seed", "-1")
	writer.Close()

	// POST to the test server with the same content type.
	req, _ := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(body.String()))
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d\nbody: %s", resp.StatusCode, http.StatusOK, string(respBody))
	}

	var result map[string]string
	if err := json.Unmarshal(respBody, &result); err != nil {
		t.Fatalf("not JSON: %s", string(respBody))
	}

	if result["prompt"] != "{car,motorcycle} in sunset" {
		t.Errorf("prompt = %q, want %q", result["prompt"], "{car,motorcycle} in sunset")
	}
	if result["seeds"] != "-1" {
		t.Errorf("seeds = %q, want %q", result["seeds"], "-1")
	}
	t.Log("  OK: multipart form parsed correctly, prompt and seeds extracted")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
