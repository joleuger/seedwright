package data

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"seedwright/internal/data/model"
	"seedwright/internal/data/querybuilder"
	"seedwright/internal/storage"
)

var nilQB4 *querybuilder.Builder // nil is safe: ListElements checks r.qb != nil

// --- Version-aware sync integration tests ---

// TestSyncFromStorage_newElements tests that new elements from S3 are inserted
// into SQLite with their version during sync.
func TestSyncFromStorage_newElements(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB4)
	ctx := context.Background()

	// Write an element JSON to the mock S3 store.
	elem := model.NewImageElement("default", "sync-test", 512, 512, 20, 7.0, 42, "v1-5", "", "", "", "v1-5.ckpt")
	elem.Version = 1

	elemData, _ := json.Marshal(elem)
	store.PutObject(ctx, elem.ElementS3Key(), bytes.NewReader(elemData), int64(len(elemData)), "application/json")

	// Write a mock image.
	store.PutObject(ctx, fmt.Sprintf("projects/%s/%s", elem.Project, elem.ImageProjectLocation()), bytes.NewReader([]byte("image")), 5, "image/png")

	// Sync.
	if err := repo.SyncFromStorage(ctx); err != nil {
		t.Fatalf("SyncFromStorage: %v", err)
	}

	// Verify element exists in SQLite with version 1.
	var version int
	err := db.QueryRowContext(ctx, `SELECT version FROM elements WHERE id = ?`, elem.ID).Scan(&version)
	if err != nil {
		t.Fatalf("SELECT version: %v", err)
	}
	if version != 1 {
		t.Errorf("version = %d, want 1", version)
	}

	// Verify prompt was synced.
	var prompt string
	err = db.QueryRowContext(ctx, `SELECT prompt FROM elements WHERE id = ?`, elem.ID).Scan(&prompt)
	if err != nil {
		t.Fatalf("SELECT prompt: %v", err)
	}
	if prompt != "sync-test" {
		t.Errorf("prompt = %q, want %q", prompt, "sync-test")
	}
}

// TestSyncFromStorage_versionMatch tests that elements with a matching version
// are NOT updated during sync (no DELETE fires, no cascade).
func TestSyncFromStorage_versionMatch(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB4)
	ctx := context.Background()

	// Create an element directly in SQLite (simulating a previous sync).
	elem := model.NewImageElement("default", "original-prompt", 512, 512, 20, 7.0, 42, "v1-5", "", "", "", "v1-5.ckpt")
	elem.Version = 1

	// Also write the element JSON to S3 with the same version.
	elemData, _ := json.Marshal(elem)
	store.PutObject(ctx, elem.ElementS3Key(), bytes.NewReader(elemData), int64(len(elemData)), "application/json")
	store.PutObject(ctx, fmt.Sprintf("projects/%s/%s", elem.Project, elem.ImageProjectLocation()), bytes.NewReader([]byte("image")), 5, "image/png")

	// First sync: insert the element.
	if err := repo.SyncFromStorage(ctx); err != nil {
		t.Fatalf("SyncFromStorage (first): %v", err)
	}

	// Verify it's there.
	var count int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM elements WHERE id = ?`, elem.ID).Scan(&count)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}

	// Second sync: element has same version, should NOT be updated.
	if err := repo.SyncFromStorage(ctx); err != nil {
		t.Fatalf("SyncFromStorage (second): %v", err)
	}

	// Verify still exactly 1 row (no INSERT OR REPLACE creating a second row).
	err = db.QueryRowContext(ctx, `SELECT count(*) FROM elements WHERE id = ?`, elem.ID).Scan(&count)
	if err != nil {
		t.Fatalf("count after second sync: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 (version match should not trigger UPDATE)", count)
	}

	// Verify the original prompt was NOT overwritten.
	var prompt string
	err = db.QueryRowContext(ctx, `SELECT prompt FROM elements WHERE id = ?`, elem.ID).Scan(&prompt)
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if prompt != "original-prompt" {
		t.Errorf("prompt = %q, want %q (version match should not overwrite)", prompt, "original-prompt")
	}
}

// TestSyncFromStorage_versionMismatch tests that elements with a different
// version from S3 ARE updated during sync.
func TestSyncFromStorage_versionMismatch(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB4)
	ctx := context.Background()

	// Create an element in SQLite with version 1.
	elem := model.NewImageElement("default", "old-prompt", 512, 512, 20, 7.0, 42, "v1-5", "", "", "", "v1-5.ckpt")
	elem.Version = 1

	// Write the element JSON to S3 with version 2 (simulating modification).
	g := elem.Generation
	g.Prompt = "new-prompt"
	g.NegativePrompt = ""
	elem.Version = 2
	elemData, _ := json.Marshal(elem)
	store.PutObject(ctx, elem.ElementS3Key(), bytes.NewReader(elemData), int64(len(elemData)), "application/json")
	store.PutObject(ctx, fmt.Sprintf("projects/%s/%s", elem.Project, elem.ImageProjectLocation()), bytes.NewReader([]byte("image")), 5, "image/png")

	// First sync: insert the element with version 2.
	if err := repo.SyncFromStorage(ctx); err != nil {
		t.Fatalf("SyncFromStorage: %v", err)
	}

	// Verify version and prompt from S3.
	var version int
	var prompt string
	err := db.QueryRowContext(ctx, `SELECT version, prompt FROM elements WHERE id = ?`, elem.ID).Scan(&version, &prompt)
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if version != 2 {
		t.Errorf("version = %d, want 2", version)
	}
	if prompt != "new-prompt" {
		t.Errorf("prompt = %q, want %q", prompt, "new-prompt")
	}
}

// TestSyncFromStorage_legacyJSON tests that JSON files without a version field
// (legacy elements) are treated as version 1 and synced correctly.
func TestSyncFromStorage_legacyJSON(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB4)
	ctx := context.Background()

	// Write legacy JSON (no version field).
	legacyJSON := `{
		"id": "legacy-001",
		"project": "default",
		"kind": "image",
		"created_at": "2026-07-14T10:30:00Z",
		"model": {"architecture": "v1-5", "name": "v1-5.ckpt"},
		"prompt": "legacy prompt",
		"negative_prompt": "",
		"width": 512,
		"height": 512,
		"seed": 99,
		"sample_steps": 20,
		"txt_cfg": 7.0
	}`
	store.PutObject(ctx, "projects/default/elements/legacy-001.json", bytes.NewReader([]byte(legacyJSON)), int64(len(legacyJSON)), "application/json")
	store.PutObject(ctx, "projects/default/images/legacy-001.png", bytes.NewReader([]byte("image")), 5, "image/png")

	if err := repo.SyncFromStorage(ctx); err != nil {
		t.Fatalf("SyncFromStorage: %v", err)
	}

	// Verify legacy element was inserted with version 1.
	var version int
	var prompt string
	err := db.QueryRowContext(ctx, `SELECT version, prompt FROM elements WHERE id = 'legacy-001'`).Scan(&version, &prompt)
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if version != 1 {
		t.Errorf("version = %d, want 1 (legacy JSON should default to version 1)", version)
	}
	if prompt != "legacy prompt" {
		t.Errorf("prompt = %q, want %q", prompt, "legacy prompt")
	}
}

// TestSyncFromStorage_versionPreservesOnCascade tests that version-aware sync
// does NOT fire DELETE, which means ON DELETE CASCADE on extension tables
// is not triggered during a normal sync.
//
// This test simulates the favorites extension scenario: an element is marked
// as a favorite, and then a sync runs. With version-aware sync, the CASCADE
// should NOT fire because no DELETE occurs.
func TestSyncFromStorage_versionPreservesOnCascade(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB4)

	// Create the favorites table (simulating the extension schema).
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS ext_favorites (
    element_id TEXT PRIMARY KEY,
    project    TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (element_id) REFERENCES elements(id) ON DELETE CASCADE,
    FOREIGN KEY (project)    REFERENCES projects(name) ON DELETE CASCADE
)
`)
	if err != nil {
		t.Fatalf("create favorites table: %v", err)
	}

	ctx := context.Background()

	// Create an element and mark it as favorite.
	elem := model.NewImageElement("default", "favorite-test", 512, 512, 20, 7.0, 42, "v1-5", "", "", "", "v1-5.ckpt")
	elem.Version = 1
	elemData, _ := json.Marshal(elem)
	store.PutObject(ctx, elem.ElementS3Key(), bytes.NewReader(elemData), int64(len(elemData)), "application/json")
	store.PutObject(ctx, fmt.Sprintf("projects/%s/%s", elem.Project, elem.ImageProjectLocation()), bytes.NewReader([]byte("image")), 5, "image/png")

	// First sync: insert element.
	if err := repo.SyncFromStorage(ctx); err != nil {
		t.Fatalf("SyncFromStorage (first): %v", err)
	}

	// Mark element as favorite.
	_, err = db.Exec(`INSERT INTO ext_favorites (element_id, project) VALUES (?, ?)`, elem.ID, "default")
	if err != nil {
		t.Fatalf("insert favorite: %v", err)
	}

	// Verify favorite exists.
	var favCount int
	err = db.QueryRowContext(ctx, `SELECT count(*) FROM ext_favorites WHERE element_id = ?`, elem.ID).Scan(&favCount)
	if err != nil {
		t.Fatalf("count favorites: %v", err)
	}
	if favCount != 1 {
		t.Fatalf("favCount = %d, want 1", favCount)
	}

	// Second sync: same element, same version.
	// With version-aware sync, no DELETE fires → no CASCADE → favorites survive.
	if err := repo.SyncFromStorage(ctx); err != nil {
		t.Fatalf("SyncFromStorage (second): %v", err)
	}

	// Verify favorite still exists (CASCADE did NOT fire).
	err = db.QueryRowContext(ctx, `SELECT count(*) FROM ext_favorites WHERE element_id = ?`, elem.ID).Scan(&favCount)
	if err != nil {
		t.Fatalf("count favorites after sync: %v", err)
	}
	if favCount != 1 {
		t.Errorf("favCount = %d, want 1 (CASCADE should NOT fire during version-aware sync)", favCount)
	}
}

// TestSyncFromStorage_versionUpdated tests that the version field in the
// elements table is updated to match S3 after a version-aware sync.
func TestSyncFromStorage_versionUpdated(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB4)
	ctx := context.Background()

	// First sync with version 1.
	elem := model.NewImageElement("default", "sync-test", 512, 512, 20, 7.0, 42, "v1-5", "", "", "", "v1-5.ckpt")
	// SyncFromStorage uses the flattened prompt column for the first sync.
	// The element's flat fields are populated by NewImageElement.
	elem.Version = 1
	elemData, _ := json.Marshal(elem)
	store.PutObject(ctx, elem.ElementS3Key(), bytes.NewReader(elemData), int64(len(elemData)), "application/json")
	store.PutObject(ctx, fmt.Sprintf("projects/%s/%s", elem.Project, elem.ImageProjectLocation()), bytes.NewReader([]byte("image")), 5, "image/png")

	if err := repo.SyncFromStorage(ctx); err != nil {
		t.Fatalf("SyncFromStorage (first): %v", err)
	}

	// Verify version 1.
	var version int
	err := db.QueryRowContext(ctx, `SELECT version FROM elements WHERE id = ?`, elem.ID).Scan(&version)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if version != 1 {
		t.Errorf("version = %d, want 1", version)
	}

	// Now modify the element in S3 (version 2).
	g := elem.Generation
	g.Prompt = "modified-prompt"
	g.NegativePrompt = ""
	elem.Version = 2
	elemData, _ = json.Marshal(elem)
	store.PutObject(ctx, elem.ElementS3Key(), bytes.NewReader(elemData), int64(len(elemData)), "application/json")

	// Second sync: should update version and prompt.
	if err := repo.SyncFromStorage(ctx); err != nil {
		t.Fatalf("SyncFromStorage (second): %v", err)
	}

	// Verify version updated to 2.
	var prompt string
	err = db.QueryRowContext(ctx, `SELECT version, prompt FROM elements WHERE id = ?`, elem.ID).Scan(&version, &prompt)
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if version != 2 {
		t.Errorf("version = %d, want 2", version)
	}
	if prompt != "modified-prompt" {
		t.Errorf("prompt = %q, want %q", prompt, "modified-prompt")
	}
}

// TestSyncFromStorage_projectVersion tests that projects are synced with
// version 1 when discovered from element JSON.
func TestSyncFromStorage_projectVersion(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB4)
	ctx := context.Background()

	// Write element JSON to trigger project sync.
	elem := model.NewImageElement("test-project", "test", 512, 512, 20, 7.0, 42, "v1-5", "", "", "", "v1-5.ckpt")
	elem.Version = 1
	elemData, _ := json.Marshal(elem)
	store.PutObject(ctx, elem.ElementS3Key(), bytes.NewReader(elemData), int64(len(elemData)), "application/json")
	store.PutObject(ctx, fmt.Sprintf("projects/%s/%s", elem.Project, elem.ImageProjectLocation()), bytes.NewReader([]byte("image")), 5, "image/png")

	if err := repo.SyncFromStorage(ctx); err != nil {
		t.Fatalf("SyncFromStorage: %v", err)
	}

	// Verify project was created with version 1.
	var version int
	err := db.QueryRowContext(ctx, `SELECT version FROM projects WHERE name = ?`, "test-project").Scan(&version)
	if err != nil {
		t.Fatalf("SELECT project version: %v", err)
	}
	if version != 1 {
		t.Errorf("project version = %d, want 1", version)
	}
}

// TestCreateElement_versionSet verifies that CreateElement sets the version
// field on the element JSON written to S3.
func TestCreateElement_versionSet(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB4)
	ctx := context.Background()

	elem := model.NewImageElement("default", "version-test", 512, 512, 20, 7.0, 42, "v1-5", "", "", "", "v1-5.ckpt")
	image := io.NopCloser(bytes.NewReader([]byte("image")))

	if err := repo.CreateElement(ctx, elem, image, 5); err != nil {
		t.Fatalf("CreateElement: %v", err)
	}

	// Verify version is set in the element JSON in S3.
	s3Data, ok := store.Objects()[elem.ElementS3Key()]
	if !ok {
		t.Fatalf("element JSON not found in S3")
	}

	var parsed model.Element
	if err := json.Unmarshal(s3Data, &parsed); err != nil {
		t.Fatalf("unmarshal S3 data: %v", err)
	}
	if parsed.Version != 1 {
		t.Errorf("S3 element version = %d, want 1", parsed.Version)
	}
}

// TestElementVersion_GetAndList verifies that version is returned by
// GetElement and ListElements.
func TestElementVersion_GetAndList(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB4)
	ctx := context.Background()

	elem := model.NewImageElement("default", "version-test", 512, 512, 20, 7.0, 42, "v1-5", "", "", "", "v1-5.ckpt")
	elem.Version = 3
	image := io.NopCloser(bytes.NewReader([]byte("image")))

	if err := repo.CreateElement(ctx, elem, image, 5); err != nil {
		t.Fatalf("CreateElement: %v", err)
	}

	// GetElement should return version 3.
	restored, err := repo.GetElement(ctx, elem.ID)
	if err != nil {
		t.Fatalf("GetElement: %v", err)
	}
	if restored.Version != 3 {
		t.Errorf("GetElement version = %d, want 3", restored.Version)
	}

	// ListElements should also return version 3.
	elements, _, err := repo.ListElements(ctx, "default", ListOptions{PerPage: 24})
	if err != nil {
		t.Fatalf("ListElements: %v", err)
	}
	if len(elements) != 1 {
		t.Fatalf("got %d elements, want 1", len(elements))
	}
	if elements[0].Version != 3 {
		t.Errorf("ListElements version = %d, want 3", elements[0].Version)
	}
}

// TestSyncFromStorage_legacyJSONNotOverwritten tests that legacy elements
// (no version in JSON, treated as version 1) are NOT overwritten by a
// subsequent sync when they already exist in SQLite with version 1.
func TestSyncFromStorage_legacyJSONNotOverwritten(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB4)
	ctx := context.Background()

	// First sync with legacy JSON.
	legacyJSON := `{
		"id": "legacy-002",
		"project": "default",
		"kind": "image",
		"created_at": "2026-07-14T10:30:00Z",
		"model": {"architecture": "v1-5", "name": "v1-5.ckpt"},
		"prompt": "legacy",
		"negative_prompt": "",
		"width": 512,
		"height": 512,
		"seed": 99,
		"sample_steps": 20,
		"txt_cfg": 7.0
	}`
	store.PutObject(ctx, "projects/default/elements/legacy-002.json", bytes.NewReader([]byte(legacyJSON)), int64(len(legacyJSON)), "application/json")
	store.PutObject(ctx, "projects/default/images/legacy-002.png", bytes.NewReader([]byte("image")), 5, "image/png")

	if err := repo.SyncFromStorage(ctx); err != nil {
		t.Fatalf("SyncFromStorage (first): %v", err)
	}

	// Verify it was synced with version 1.
	var version int
	var prompt string
	err := db.QueryRowContext(ctx, `SELECT version, prompt FROM elements WHERE id = 'legacy-002'`).Scan(&version, &prompt)
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if version != 1 {
		t.Errorf("version = %d, want 1", version)
	}
	if prompt != "legacy" {
		t.Errorf("prompt = %q, want %q", prompt, "legacy")
	}

	// Second sync with the same legacy JSON — should NOT update (version match).
	if err := repo.SyncFromStorage(ctx); err != nil {
		t.Fatalf("SyncFromStorage (second): %v", err)
	}

	// Verify prompt still "legacy" (not overwritten).
	err = db.QueryRowContext(ctx, `SELECT prompt FROM elements WHERE id = 'legacy-002'`).Scan(&prompt)
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if prompt != "legacy" {
		t.Errorf("prompt = %q, want %q (version match should not overwrite)", prompt, "legacy")
	}
}

// TestSyncFromStorage_multipleElements tests that a batch sync with multiple
// elements handles version-aware upsert correctly for each one.
func TestSyncFromStorage_multipleElements(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB4)
	ctx := context.Background()

	elements := []struct {
		id      string
		version int
		prompt  string
		seed    int64
	}{
		{"multi-001", 1, "element-one", 100},
		{"multi-002", 2, "element-two", 200},
		{"multi-003", 1, "element-three", 300},
	}

	for _, e := range elements {
		elem := model.NewImageElement("default", e.prompt, 512, 512, 20, 7.0, e.seed, "v1-5", "", "", "", "v1-5.ckpt")
		elem.ID = e.id
		elem.Version = e.version
		elemData, _ := json.Marshal(elem)
		store.PutObject(ctx, "projects/default/elements/"+e.id+".json", bytes.NewReader(elemData), int64(len(elemData)), "application/json")
		store.PutObject(ctx, "projects/default/images/"+e.id+".png", bytes.NewReader([]byte("image")), 5, "image/png")
	}

	if err := repo.SyncFromStorage(ctx); err != nil {
		t.Fatalf("SyncFromStorage: %v", err)
	}

	// Verify all elements are present with correct versions.
	for _, e := range elements {
		var version int
		var prompt string
		var seed sql.NullInt64
		err := db.QueryRowContext(ctx,
			`SELECT version, prompt, seed FROM elements WHERE id = ?`, e.id,
		).Scan(&version, &prompt, &seed)
		if err != nil {
			t.Fatalf("SELECT %s: %v", e.id, err)
		}
		if version != e.version {
			t.Errorf("element %s: version = %d, want %d", e.id, version, e.version)
		}
		if prompt != e.prompt {
			t.Errorf("element %s: prompt = %q, want %q", e.id, prompt, e.prompt)
		}
		if !seed.Valid || seed.Int64 != e.seed {
			t.Errorf("element %s: seed = %v, want %d", e.id, seed, e.seed)
		}
	}

	// Total count should be 3.
	var total int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM elements WHERE project = 'default'`).Scan(&total)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
}

// TestSyncFromStorage_skipsEmptyProject verifies that elements with an
// empty project name are skipped during sync and do not create a project row.
func TestSyncFromStorage_skipsEmptyProject(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB4)
	ctx := context.Background()

	// Write an element JSON with an empty project name.
	elem := model.NewImageElement("", "bad-element", 512, 512, 20, 7.0, 42, "", "", "", "", "")
	elemData, _ := json.Marshal(elem)
	store.PutObject(ctx, elem.ElementS3Key(), bytes.NewReader(elemData), int64(len(elemData)), "application/json")

	// Sync.
	if err := repo.SyncFromStorage(ctx); err != nil {
		t.Fatalf("SyncFromStorage: %v", err)
	}

	// Verify no project was created.
	var count int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM projects WHERE name = ''`).Scan(&count)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("empty project count = %d, want 0", count)
	}
}
