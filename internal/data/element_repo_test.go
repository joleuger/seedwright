package data

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	"seedwright/internal/data/model"
	"seedwright/internal/data/querybuilder"
	"seedwright/internal/storage"
)

// nilQB is safe: ListElements checks r.qb != nil before using it.
var nilQB *querybuilder.Builder

// setupTestDB creates an in-memory SQLite database with schema and
// extension columns (mimicking what ext.Wire() does at startup).
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	// Disable FK in tests — production enforces them, but the real flow
	// (StartJob → CreateJob before CreateElement) would violate them.
	// Tests verify logic, not FK integrity.
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatalf("foreign_keys OFF: %v", err)
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

func TestProjectRepo_CreateAndList(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewProjectRepository(db, store)
	ctx := context.Background()

	pm := model.NewProject("test-project")
	if err := repo.CreateProject(ctx, pm); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	projects, err := repo.ListProjects(ctx, true) // filter hidden
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("got %d projects, want 1", len(projects))
	}
	if projects[0] != "test-project" {
		t.Errorf("project = %q, want %q", projects[0], "test-project")
	}
}

func TestElementRepo_CreateAndList(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB)
	ctx := context.Background()

	elem := model.NewImageElement("default", "a cat on a rooftop", 512, 512, 20, 7.0, 42, "v1-5", "", "", "", "v1-5.ckpt")

	// Create a mock image file.
	imageData := []byte("PNG-mock-image-data")
	image := io.NopCloser(bytes.NewReader(imageData))

	if err := repo.CreateElement(ctx, elem, image, int64(len(imageData))); err != nil {
		t.Fatalf("CreateElement: %v", err)
	}

	// Verify element is in SQLite.
	elements, total, err := repo.ListElements(ctx, "default", ListOptions{Page: 1, PerPage: 24})
	if err != nil {
		t.Fatalf("ListElements: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if len(elements) != 1 {
		t.Fatalf("got %d elements, want 1", len(elements))
	}
	if elements[0].ID != elem.ID {
		t.Errorf("element id = %q, want %q", elements[0].ID, elem.ID)
	}
	if elements[0].Generation.Prompt != "a cat on a rooftop" {
		t.Errorf("prompt = %q, want %q", elements[0].Generation.Prompt, "a cat on a rooftop")
	}
	if elements[0].Generation.Seed != 42 {
		t.Errorf("seed = %d, want 42", elements[0].Generation.Seed)
	}
	if elements[0].Generation.Model.Name != "v1-5.ckpt" {
		t.Errorf("model = %q, want %q", elements[0].Generation.Model.Name, "v1-5.ckpt")
	}

	// Verify S3 has element JSON.
	objects := store.Objects()
	if _, ok := objects[elem.ElementS3Key()]; !ok {
		t.Errorf("element JSON not found in S3 at %s", elem.ElementS3Key())
	}
}

func TestElementRepo_GetElement(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB)
	ctx := context.Background()

	elem := model.NewImageElement("default", "sunset", 512, 512, 20, 7.0, 99, "v1-5", "", "", "", "v1-5.ckpt")
	image := io.NopCloser(bytes.NewReader([]byte("image")))

	if err := repo.CreateElement(ctx, elem, image, 5); err != nil {
		t.Fatalf("CreateElement: %v", err)
	}

	// Retrieve.
	restored, err := repo.GetElement(ctx, elem.ID)
	if err != nil {
		t.Fatalf("GetElement: %v", err)
	}
	if restored.Generation.Seed != 99 {
		t.Errorf("seed = %d, want 99", restored.Generation.Seed)
	}
	if restored.Generation.Prompt != "sunset" {
		t.Errorf("prompt = %q, want %q", restored.Generation.Prompt, "sunset")
	}
}

func TestElementRepo_ListElementsPagination(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB)
	ctx := context.Background()

	// Create 5 elements.
	for i := 0; i < 5; i++ {
		elem := model.NewImageElement("default", "test", 512, 512, 20, 7.0, int64(i*100), "v1-5", "", "", "", "v1-5.ckpt")
		image := io.NopCloser(bytes.NewReader([]byte("image")))
		if err := repo.CreateElement(ctx, elem, image, 5); err != nil {
			t.Fatalf("CreateElement %d: %v", i, err)
		}
	}

	// Page 1 (24 per page) should return all 5.
	elements, total, err := repo.ListElements(ctx, "default", ListOptions{Page: 1, PerPage: 24})
	if err != nil {
		t.Fatalf("ListElements: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(elements) != 5 {
		t.Errorf("got %d elements, want 5", len(elements))
	}

	// Page 1 (2 per page) should return 2.
	page1, _, err := repo.ListElements(ctx, "default", ListOptions{Page: 1, PerPage: 2})
	if err != nil {
		t.Fatalf("ListElements page1: %v", err)
	}
	if len(page1) != 2 {
		t.Errorf("page1 = %d elements, want 2", len(page1))
	}

	// Page 2 (2 per page) should return 2.
	page2, _, err := repo.ListElements(ctx, "default", ListOptions{Page: 2, PerPage: 2})
	if err != nil {
		t.Fatalf("ListElements page2: %v", err)
	}
	if len(page2) != 2 {
		t.Errorf("page2 = %d elements, want 2", len(page2))
	}

	// Page 3 (2 per page) should return 1.
	page3, _, err := repo.ListElements(ctx, "default", ListOptions{Page: 3, PerPage: 2})
	if err != nil {
		t.Fatalf("ListElements page3: %v", err)
	}
	if len(page3) != 1 {
		t.Errorf("page3 = %d elements, want 1", len(page3))
	}

	// Page 4 should be empty.
	page4, _, err := repo.ListElements(ctx, "default", ListOptions{Page: 4, PerPage: 2})
	if err != nil {
		t.Fatalf("ListElements page4: %v", err)
	}
	if len(page4) != 0 {
		t.Errorf("page4 = %d elements, want 0", len(page4))
	}
}

func TestElementRepo_ListElementsSortSeed(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB)
	ctx := context.Background()

	seeds := []int64{300, 100, 200}
	for _, seed := range seeds {
		elem := model.NewImageElement("default", "test", 512, 512, 20, 7.0, seed, "v1-5", "", "", "", "v1-5.ckpt")
		image := io.NopCloser(bytes.NewReader([]byte("image")))
		if err := repo.CreateElement(ctx, elem, image, 5); err != nil {
			t.Fatalf("CreateElement: %v", err)
		}
	}

	// Sort by seed ascending.
	elements, _, err := repo.ListElements(ctx, "default", ListOptions{Sort: "seed", Order: "asc", PerPage: 10})
	if err != nil {
		t.Fatalf("ListElements: %v", err)
	}
	if len(elements) != 3 {
		t.Fatalf("got %d elements, want 3", len(elements))
	}
	for i, expected := range []int64{100, 200, 300} {
		if elements[i].Generation.Seed != expected {
			t.Errorf("element[%d].seed = %d, want %d", i, elements[i].Generation.Seed, expected)
		}
	}
}

func TestElementRepo_ListElementsSortModel(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB)
	ctx := context.Background()

	models := []string{"v2-1", "v1-5", "v2-1"}
	for _, m := range models {
		elem := model.NewImageElement("default", "test", 512, 512, 20, 7.0, 42, m, "", "", "", m+".ckpt")
		image := io.NopCloser(bytes.NewReader([]byte("image")))
		if err := repo.CreateElement(ctx, elem, image, 5); err != nil {
			t.Fatalf("CreateElement: %v", err)
		}
	}

	// Sort by model_name ascending.
	elements, _, err := repo.ListElements(ctx, "default", ListOptions{Sort: "model_name", Order: "asc", PerPage: 10})
	if err != nil {
		t.Fatalf("ListElements: %v", err)
	}
	if len(elements) != 3 {
		t.Fatalf("got %d elements, want 3", len(elements))
	}
	// Should be v1-5, v2-1, v2-1
	if elements[0].Generation.Model.Name != "v1-5.ckpt" {
		t.Errorf("element[0].model = %q, want %q", elements[0].Generation.Model.Name, "v1-5.ckpt")
	}
}

func TestStorageBackend_New(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	backend := NewStorageBackend(store, db)
	if backend == nil {
		t.Fatal("NewStorageBackend returned nil")
	}
}

// --- ElementModel JSON test ---

func TestElementModel_JSONKeys(t *testing.T) {
	elem := model.NewImageElement("default", "prompt", 512, 512, 20, 7.0, 42, "v1-5", "", "", "", "v1-5.ckpt")
	data, err := json.Marshal(elem)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}

	// Model is now nested under "generation.model" (not top-level).
	genMap, ok := m["generation"].(map[string]any)
	if !ok {
		t.Fatal("generation should be an object")
	}
	modelMap, ok := genMap["model"].(map[string]any)
	if !ok {
		t.Fatal("generation.model should be an object")
	}

	if _, has := modelMap["architecture"]; !has {
		t.Error("generation.model JSON should have 'architecture' field")
	}
	if _, has := modelMap["name"]; !has {
		t.Error("generation.model JSON should have 'name' field")
	}

	// Top-level shims are removed — generation fields live under generation.*.
	// Verify PascalCase keys do NOT appear at top level.
	if _, has := m["Prompt"]; has {
		t.Error("top-level 'Prompt' (PascalCase) should not exist — use generation.prompt")
	}
	if _, has := m["Seed"]; has {
		t.Error("top-level 'Seed' (PascalCase) should not exist — use generation.seed")
	}
}

// --- Time parsing test ---

func TestParseTime(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Time
		wantErr  bool
	}{
		{"2026-07-15T10:30:00Z", time.Date(2026, 7, 15, 10, 30, 0, 0, time.UTC), false},
		{"not-a-time", time.Time{}, true},
		{"", time.Time{}, true},
	}

	for _, tt := range tests {
		got := parseTime(tt.input)
		if tt.wantErr {
			if !got.IsZero() {
				t.Errorf("parseTime(%q) = %v, want zero time", tt.input, got)
			}
		} else {
			if !got.Equal(tt.expected) {
				t.Errorf("parseTime(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		}
	}
}

// --- ListOptions tests ---

func TestListOptions_defaults(t *testing.T) {
	opts := ListOptions{}
	opts.Validate()

	if opts.Page != 1 {
		t.Errorf("page = %d, want 1", opts.Page)
	}
	if opts.PerPage != 50 {
		t.Errorf("per_page = %d, want 50", opts.PerPage)
	}
	if opts.Sort != "created_at" {
		t.Errorf("sort = %q, want %q", opts.Sort, "created_at")
	}
	if opts.Order != "desc" {
		t.Errorf("order = %q, want %q", opts.Order, "desc")
	}
}

func TestListOptions_customSort(t *testing.T) {
	opts := ListOptions{Sort: "seed", Order: "asc"}
	opts.Validate()

	if opts.Sort != "seed" {
		t.Errorf("sort = %q, want %q", opts.Sort, "seed")
	}
	if opts.Order != "asc" {
		t.Errorf("order = %q, want %q", opts.Order, "asc")
	}
}

func TestListOptions_invalidSort(t *testing.T) {
	opts := ListOptions{Sort: "invalid", Order: "desc"}
	opts.Validate()

	if opts.Sort != "created_at" {
		t.Errorf("sort = %q, want default %q", opts.Sort, "created_at")
	}
}

func TestListOptions_perPageBounds(t *testing.T) {
	// Regression: the old clamp ("> 100 && < 1000 → 50") silently broke
	// the gallery's 200 option. All values in [1, MaxPerPage] survive;
	// out-of-range values clamp to the bounds.
	tests := []struct {
		in   int
		want int
	}{
		{24, 24},
		{50, 50},
		{200, 200},
		{500, 500},
		{10000, MaxPerPage},
		{10001, MaxPerPage},
		{999999, MaxPerPage},
		{0, 50},
		{-5, 50},
	}

	for _, tt := range tests {
		opts := ListOptions{PerPage: tt.in}
		opts.Validate()
		if opts.PerPage != tt.want {
			t.Errorf("PerPage(%d) after Validate = %d, want %d", tt.in, opts.PerPage, tt.want)
		}
	}
}

func TestListOptions_totalPages(t *testing.T) {
	tests := []struct {
		total    int
		perPage  int
		expected int
	}{
		{0, 24, 0},
		{24, 24, 1},
		{25, 24, 2},
		{48, 24, 2},
		{49, 24, 3},
		{100, 10, 10},
	}

	for _, tt := range tests {
		opts := ListOptions{PerPage: tt.perPage}
		got := opts.TotalPages(tt.total)
		if got != tt.expected {
			t.Errorf("TotalPages(%d, %d) = %d, want %d", tt.total, tt.perPage, got, tt.expected)
		}
	}
}

func TestListOptions_offset(t *testing.T) {
	tests := []struct {
		page     int
		perPage  int
		expected int
	}{
		{1, 24, 0},
		{2, 24, 24},
		{3, 24, 48},
		{1, 10, 0},
	}

	for _, tt := range tests {
		opts := ListOptions{Page: tt.page, PerPage: tt.perPage}
		got := opts.Offset()
		if got != tt.expected {
			t.Errorf("Offset(page=%d, perPage=%d) = %d, want %d", tt.page, tt.perPage, got, tt.expected)
		}
	}
}

// --- sortedQuery tests ---

func TestSortedQuery(t *testing.T) {
	tests := []struct {
		sort string
		want string
	}{
		{"created_at", "created_at"},
		{"seed", "seed"},
		{"model_name", "model_name"},
		{"invalid", "created_at"},
		{"", "created_at"},
	}

	for _, tt := range tests {
		got := sortedQuery(tt.sort, "desc")
		if got != tt.want {
			t.Errorf("sortedQuery(%q) = %q, want %q", tt.sort, got, tt.want)
		}
	}
}

// TestSyncFromStorage_skipsExtensionDeltaFiles verifies that extension delta
// files under projects/{project}/ext/.../elements/{id}.json are not mistaken
// for actual element JSONs during SyncFromStorage.
func TestSyncFromStorage_skipsExtensionDeltaFiles(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB)
	ctx := context.Background()

	// Insert a real element first so we can verify count after sync.
	elem := model.NewImageElement("default", "real element", 512, 512, 20, 7.0, 42, "v1-5", "", "", "", "v1-5.ckpt")
	elemData, _ := json.Marshal(elem)
	store.PutObject(ctx, elem.ElementS3Key(), bytes.NewReader(elemData), int64(len(elemData)), "application/json")
	store.PutObject(ctx, fmt.Sprintf("projects/%s/%s", elem.Project, elem.ImageProjectLocation()), bytes.NewReader([]byte("image")), 5, "image/png")

	// Add a favorites extension delta file at the path that would have
	// been falsely matched by the old /elements/ filter.
	delta := map[string]any{
		"id":      "f9127340-b48f-4164-b9ad-b9e90e4b5b2f",
		"version": 1,
		"field":   "ext_joleuger_favorites",
	}
	deltaData, _ := json.Marshal(delta)
	store.PutObject(ctx,
		"projects/default/ext/joleuger/favorites/elements/f9127340-b48f-4164-b9ad-b9e90e4b5b2f.json",
		bytes.NewReader(deltaData),
		int64(len(deltaData)),
		"application/json",
	)

	// Sync.
	if err := repo.SyncFromStorage(ctx); err != nil {
		t.Fatalf("SyncFromStorage: %v", err)
	}

	// Verify only one element was synced (the real one, not the delta file).
	var count int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM elements`).Scan(&count)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("elements count = %d, want 1", count)
	}
}
