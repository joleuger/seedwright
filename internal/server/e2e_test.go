//go:build e2e

package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"seedwright/internal/authz"
	"seedwright/internal/config"
	"seedwright/internal/data"
	"seedwright/internal/data/model"
	"seedwright/internal/data/querybuilder"
	"seedwright/internal/storage"
)

// skipIfNoE2E skips the test when e2e-with-sdcpp is not enabled.
func skipIfNoE2E(t *testing.T) {
	if os.Getenv("SDCPP_E2E") != "1" {
		t.Skip("set SDCPP_E2E=1 to run e2e tests")
	}

	cfg, err := config.Load("config.yaml")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.E2E.EnableSDCPP {
		t.Skip("e2e-with-sdcpp is not enabled in config.yaml")
	}
}

// testServer creates a test server with in-memory SQLite and mock S3.
func testServer(t *testing.T) (*http.Client, string) {
	t.Helper()
	skipIfNoE2E(t)

	cfg, err := config.Load("config.yaml")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	// In-memory SQLite.
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
	_ = db // used for repositories

	// Mock S3 storage.
	store := storage.NewMockStorage()

	// nil is safe: ListElements checks r.qb != nil.
	var nilQB *querybuilder.Builder

	// Create a fresh project.
	ctx := context.Background()
	if err := data.NewProjectRepository(db).CreateProject(ctx, model.NewProject("e2e-test")); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Create HTTP server.
	srv := New(&Config{
		Title:        "seedwright",
		SDCPPBaseURL: cfg.SDCPP.BaseURL,
		Storage:      store,
		ProjectRepo:  data.NewProjectRepository(db),
		ElementRepo:  data.NewElementRepository(db, store, nilQB),
		Authz:        &authz.StaticEnforcer{Principal: "user:root"},
	})

	// Start server on random port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go http.Serve(listener, srv)

	// Wait for server to be ready.
	time.Sleep(100 * time.Millisecond)

	baseURL := "http://" + listener.Addr().String()
	client := &http.Client{Timeout: 30 * time.Second}

	return client, baseURL
}

func TestE2E_GenerateImage(t *testing.T) {
	client, baseURL := testServer(t)

	// Visit project page.
	resp, err := client.Get(baseURL + "/e2e-test/")
	if err != nil {
		t.Fatalf("GET project page: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("project page status: %d", resp.StatusCode)
	}

	// Submit generate POST.
	form := url.Values{
		"prompt":          {"a cat"},
		"negative_prompt": {""},
		"width":           {"512"},
		"height":          {"512"},
		"steps":           {"5"},
		"cfg":             {"7.0"},
		"seed":            {"42"},
	}
	resp, err = client.Post(baseURL+"/e2e-test/generate", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("POST generate: %v", err)
	}
	defer resp.Body.Close()

	// Should redirect to project page (303).
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("generate status: %d (want 303)", resp.StatusCode)
	}

	// Poll for job completion via gallery.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	for i := 0; i < 30; i++ {
		time.Sleep(5 * time.Second)

		// Check gallery for our image.
		resp, err := client.Get(baseURL + "/e2e-test/gallery")
		if err != nil {
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}

		if strings.Contains(string(body), "Seed: 42") {
			t.Log("image found in gallery")
			return
		}
	}

	t.Fatal("image not found in gallery after 5 minutes of polling")
}
