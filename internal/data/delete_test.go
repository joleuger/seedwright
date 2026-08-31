package data

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	"seedwright/internal/data/model"
	"seedwright/internal/data/querybuilder"
	"seedwright/internal/storage"
)

var nilQB_del *querybuilder.Builder // nil is safe: ListElements checks r.qb != nil

// --- DeleteElement tests ---

func TestDeleteElement_RemovesFromSQLiteAndS3(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB_del)
	ctx := context.Background()

	// Create an element.
	elem := model.NewImageElement("test-proj", "delete me", 512, 512, 20, 7.0, 1, "v1-5", "", "", "", "v1-5.ckpt")
	if g := elem.Generation; g != nil {
		g.NegativePrompt = "blurry"
	}
	image := io.NopCloser(bytes.NewReader([]byte("mock-image")))

	if err := repo.CreateElement(ctx, elem, image, 11); err != nil {
		t.Fatalf("CreateElement: %v", err)
	}

	// Verify it exists in SQLite.
	elements, total, err := repo.ListElements(ctx, "test-proj", ListOptions{Page: 1, PerPage: 24})
	if err != nil {
		t.Fatalf("ListElements before delete: %v", err)
	}
	if total != 1 || len(elements) != 1 {
		t.Fatalf("expected 1 element before delete, got %d", total)
	}

	// Verify S3 has both image and element JSON.
	objects := store.Objects()
	imageS3Key := fmt.Sprintf("projects/%s/%s", elem.Project, elem.ImageProjectLocation())
	if _, ok := objects[imageS3Key]; !ok {
		t.Fatalf("image not found in S3 before delete: %s", imageS3Key)
	}
	if _, ok := objects[elem.ElementS3Key()]; !ok {
		t.Fatalf("element JSON not found in S3 before delete: %s", elem.ElementS3Key())
	}

	// Delete the element.
	if err := repo.DeleteElement(ctx, elem.ID, "test-proj"); err != nil {
		t.Fatalf("DeleteElement: %v", err)
	}

	// Verify element is gone from SQLite.
	elements, total, err = repo.ListElements(ctx, "test-proj", ListOptions{Page: 1, PerPage: 24})
	if err != nil {
		t.Fatalf("ListElements after delete: %v", err)
	}
	if total != 0 || len(elements) != 0 {
		t.Errorf("expected 0 elements after delete, got %d", total)
	}

	// Verify S3 objects are gone.
	objects = store.Objects()
	imageS3Key2 := fmt.Sprintf("projects/%s/%s", elem.Project, elem.ImageProjectLocation())
	if _, ok := objects[imageS3Key2]; ok {
		t.Errorf("image still exists in S3 after delete: %s", imageS3Key2)
	}
	if _, ok := objects[elem.ElementS3Key()]; ok {
		t.Errorf("element JSON still exists in S3 after delete: %s", elem.ElementS3Key())
	}
}

func TestDeleteElement_MissingElement(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB_del)
	ctx := context.Background()

	// Delete a non-existent element — should succeed (no-op).
	err := repo.DeleteElement(ctx, "nonexistent", "test-proj")
	if err != nil {
		t.Fatalf("unexpected error deleting non-existent element: %v", err)
	}
}

func TestDeleteElement_WrongProject(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB_del)
	ctx := context.Background()

	// Create an element in "proj-a".
	elem := model.NewImageElement("proj-a", "wrong project", 512, 512, 20, 7.0, 2, "v1-5", "", "", "", "v1-5.ckpt")
	image := io.NopCloser(bytes.NewReader([]byte("mock-image")))

	if err := repo.CreateElement(ctx, elem, image, 10); err != nil {
		t.Fatalf("CreateElement: %v", err)
	}

	// Try to delete it from "proj-b" — should succeed without touching anything.
	// The element exists in proj-a, so a contract violation warning is logged,
	// but no cleanup is performed.
	err := repo.DeleteElement(ctx, elem.ID, "proj-b")
	if err != nil {
		t.Fatalf("unexpected error deleting element from wrong project: %v", err)
	}

	// Verify element still exists in proj-a (nothing was deleted).
	_, total, err := repo.ListElements(ctx, "proj-a", ListOptions{Page: 1, PerPage: 24})
	if err != nil {
		t.Fatalf("ListElements: %v", err)
	}
	if total != 1 {
		t.Errorf("expected element to still exist in proj-a, got %d", total)
	}
}

// --- DeleteProject tests ---

func TestDeleteProject_RemovesAllData(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	projRepo := NewProjectRepository(db, store)
	elemRepo := NewElementRepository(db, store, nilQB_del)
	ctx := context.Background()

	// Create project.
	pm := model.NewProject("delete-proj")
	if err := projRepo.CreateProject(ctx, pm); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Create 3 elements.
	for i := int64(1); i <= 3; i++ {
		elem := model.NewImageElement("delete-proj", "delete me", 512, 512, 20, 7.0, i, "v1-5", "", "", "", "v1-5.ckpt")
		image := io.NopCloser(bytes.NewReader([]byte("mock-image")))
		if err := elemRepo.CreateElement(ctx, elem, image, 10); err != nil {
			t.Fatalf("CreateElement %d: %v", i, err)
		}
	}

	// Create jobs for each element.
	jobRepo := NewJobRepository(db)
	for i := int64(1); i <= 3; i++ {
		record := JobRecord{
			ID:        "job-" + string(rune(i)),
			ElementID: "el-" + string(rune(i)),
			Project:   "delete-proj",
			Status:    "queued",
		}
		if err := jobRepo.CreateJob(ctx, record); err != nil {
			t.Fatalf("CreateJob %d: %v", i, err)
		}
	}

	// Verify jobs exist (queued = active, so ListActiveJobs works).
	activeJobs, _ := jobRepo.ListActiveJobs(ctx, "delete-proj")
	if len(activeJobs) != 3 {
		t.Errorf("expected 3 active jobs, got %d", len(activeJobs))
	}

	// Verify everything exists.
	projects, err := projRepo.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	elems, total, _ := elemRepo.ListElements(ctx, "delete-proj", ListOptions{Page: 1, PerPage: 24})
	if total != 3 {
		t.Errorf("expected 3 elements, got %d", total)
	}
	jobs, _ := jobRepo.ListActiveJobs(ctx, "delete-proj")
	if len(jobs) != 3 {
		t.Errorf("expected 3 jobs, got %d", len(jobs))
	}
	objects := store.Objects()
	// Should have 3 element JSONs + 3 images (CreateProject only writes to SQLite).
	if len(objects) < 6 {
		t.Errorf("expected at least 6 S3 objects, got %d", len(objects))
	}

	// Delete the project.
	if err := projRepo.DeleteProject(ctx, "delete-proj"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	// Verify project is gone.
	projects, _ = projRepo.ListProjects(ctx, false)
	if len(projects) != 0 {
		t.Errorf("expected 0 projects after delete, got %d", len(projects))
	}

	// Verify elements are gone.
	elems, total, _ = elemRepo.ListElements(ctx, "delete-proj", ListOptions{Page: 1, PerPage: 24})
	if total != 0 || len(elems) != 0 {
		t.Errorf("expected 0 elements after delete, got %d", total)
	}

	// Verify jobs are gone.
	jobs, _ = jobRepo.ListActiveJobs(ctx, "delete-proj")
	if len(jobs) != 0 {
		t.Errorf("expected 0 jobs after delete, got %d", len(jobs))
	}

	// Verify S3 objects are gone.
	objects = store.Objects()
	if len(objects) != 0 {
		t.Errorf("expected 0 S3 objects after delete, got %d: %v", len(objects), objects)
	}
}

func TestDeleteProject_MissingProject(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewProjectRepository(db, store)
	ctx := context.Background()

	// Delete a non-existent project.
	// This should not error — the project simply doesn't exist.
	err := repo.DeleteProject(ctx, "nonexistent-proj")
	if err != nil {
		t.Fatalf("DeleteProject for missing project should not error, got: %v", err)
	}
}
