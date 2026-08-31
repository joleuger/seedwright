package data

import (
	"bytes"
	"context"
	"io"
	"testing"

	"seedwright/internal/data/model"
	"seedwright/internal/data/querybuilder"
	"seedwright/internal/storage"
)

var nilQB2 *querybuilder.Builder // nil is safe: ListElements checks r.qb != nil

func TestListRecentPrompts_OrdersByMostRecent(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB2)
	ctx := context.Background()

	// Create elements with different prompts and seeds to ensure distinct created_at values.
	elements := []struct {
		prompt string
		seed   int64
	}{
		{"old prompt", 1},
		{"brand new", 2},
		{"old prompt", 3}, // duplicate prompt, newer
		{"another old", 4},
		{"brand new", 5}, // duplicate prompt, newer
	}

	for _, e := range elements {
		elem := model.NewImageElement("test", e.prompt, 512, 512, 20, 7.0, e.seed, "v1-5", "", "", "", "v1-5.ckpt")
		image := io.NopCloser(bytes.NewReader([]byte("image")))
		if err := repo.CreateElement(ctx, elem, image, 5); err != nil {
			t.Fatalf("CreateElement (%s, seed %d): %v", e.prompt, e.seed, err)
		}
	}

	// ListRecentPrompts should return the most recent occurrence of each prompt,
	// ordered by most recent creation.
	prompts, err := repo.ListRecentPrompts(ctx, "test", 5)
	if err != nil {
		t.Fatalf("ListRecentPrompts: %v", err)
	}

	// Should return 3 distinct prompts.
	if len(prompts) != 3 {
		t.Fatalf("got %d prompts, want 3", len(prompts))
	}

	// The last two elements (brand new seed=5, old prompt seed=3) are the most recent,
	// so they should be first in descending order.
	// The order should be: ["brand new", "old prompt", "another old"] or similar.
	// We check that "brand new" appears before "another old" (since seed=5 > seed=4).
	found := map[string]int{}
	for i, p := range prompts {
		found[p] = i
	}

	if _, has := found["brand new"]; !has {
		t.Errorf("prompts missing %q", "brand new")
	}
	if _, has := found["old prompt"]; !has {
		t.Errorf("prompts missing %q", "old prompt")
	}
	if _, has := found["another old"]; !has {
		t.Errorf("prompts missing %q", "another old")
	}

	// "brand new" (last created) should appear before "another old" (oldest distinct prompt).
	if found["brand new"] >= found["another old"] {
		t.Errorf("'brand new' at index %d should be before 'another old' at index %d",
			found["brand new"], found["another old"])
	}
}

func TestListRecentPrompts_FiltersEmpty(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB2)
	ctx := context.Background()

	// Create an element with an empty prompt.
	elem := model.NewImageElement("test", "", 512, 512, 20, 7.0, 1, "v1-5", "", "", "", "v1-5.ckpt")
	image := io.NopCloser(bytes.NewReader([]byte("image")))
	if err := repo.CreateElement(ctx, elem, image, 5); err != nil {
		t.Fatalf("CreateElement: %v", err)
	}

	// Create a valid element.
	elem2 := model.NewImageElement("test", "valid prompt", 512, 512, 20, 7.0, 2, "v1-5", "", "", "", "v1-5.ckpt")
	image2 := io.NopCloser(bytes.NewReader([]byte("image")))
	if err := repo.CreateElement(ctx, elem2, image2, 5); err != nil {
		t.Fatalf("CreateElement 2: %v", err)
	}

	prompts, err := repo.ListRecentPrompts(ctx, "test", 5)
	if err != nil {
		t.Fatalf("ListRecentPrompts: %v", err)
	}

	if len(prompts) != 1 {
		t.Fatalf("got %d prompts, want 1 (empty prompt should be filtered)", len(prompts))
	}
	if prompts[0] != "valid prompt" {
		t.Errorf("prompt = %q, want %q", prompts[0], "valid prompt")
	}
}

func TestListRecentPrompts_Limit(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB2)
	ctx := context.Background()

	// Create 5 distinct prompts.
	for i := 0; i < 5; i++ {
		elem := model.NewImageElement("test", "prompt-"+string(rune('a'+i)), 512, 512, 20, 7.0, int64(i), "v1-5", "", "", "", "v1-5.ckpt")
		image := io.NopCloser(bytes.NewReader([]byte("image")))
		if err := repo.CreateElement(ctx, elem, image, 5); err != nil {
			t.Fatalf("CreateElement %d: %v", i, err)
		}
	}

	prompts, err := repo.ListRecentPrompts(ctx, "test", 3)
	if err != nil {
		t.Fatalf("ListRecentPrompts: %v", err)
	}

	if len(prompts) != 3 {
		t.Fatalf("got %d prompts, want 3 (limit)", len(prompts))
	}
}

func TestListRecentPrompts_NoPrompts(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB2)
	ctx := context.Background()

	prompts, err := repo.ListRecentPrompts(ctx, "empty-project", 5)
	if err != nil {
		t.Fatalf("ListRecentPrompts: %v", err)
	}

	if len(prompts) != 0 {
		t.Errorf("got %d prompts, want 0", len(prompts))
	}
}

func TestListRecentPrompts_DuplicatePrompts(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB2)
	ctx := context.Background()

	// Create 3 elements with the SAME prompt at different times.
	for i := 0; i < 3; i++ {
		elem := model.NewImageElement("test", "same prompt", 512, 512, 20, 7.0, int64(i*100), "v1-5", "", "", "", "v1-5.ckpt")
		image := io.NopCloser(bytes.NewReader([]byte("image")))
		if err := repo.CreateElement(ctx, elem, image, 5); err != nil {
			t.Fatalf("CreateElement %d: %v", i, err)
		}
	}

	prompts, err := repo.ListRecentPrompts(ctx, "test", 5)
	if err != nil {
		t.Fatalf("ListRecentPrompts: %v", err)
	}

	// Should return exactly 1 distinct prompt.
	if len(prompts) != 1 {
		t.Fatalf("got %d prompts, want 1 (should deduplicate)", len(prompts))
	}
	if prompts[0] != "same prompt" {
		t.Errorf("prompt = %q, want %q", prompts[0], "same prompt")
	}
}
