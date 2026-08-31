package data

import (
	"context"
	"database/sql"
	"testing"

	"seedwright/internal/data/model"
)

func setupTestDBWithJobs(t *testing.T) *sql.DB {
	t.Helper()
	db, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	// Disable FK in tests — production enforces them, but the real flow
	// (StartJob → CreateJob before CreateElement) would violate them.
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

func TestJobRepo_CreateAndGet(t *testing.T) {
	db := setupTestDBWithJobs(t)
	repo := NewJobRepository(db)
	ctx := context.Background()

	elem := model.NewImageElement("test", "a cat", 512, 512, 20, 7.0, 42, "v1-5", "", "", "", "v1-5.ckpt")
	record := FromDomain(elem, "sdcpp_job_1", "queued")

	if err := repo.CreateJob(ctx, record); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	// Job ID is now a unique UUID (not the element ID).
	found, err := repo.GetJob(ctx, record.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if found.ID != record.ID {
		t.Errorf("job ID = %q, want %q", found.ID, record.ID)
	}
	if found.ElementID != elem.ID {
		t.Errorf("element_id = %q, want %q", found.ElementID, elem.ID)
	}
	if found.SDCPPJobID != "sdcpp_job_1" {
		t.Errorf("sdcpp_job_id = %q, want %q", found.SDCPPJobID, "sdcpp_job_1")
	}
	if found.Status != "queued" {
		t.Errorf("status = %q, want %q", found.Status, "queued")
	}
	if found.Project != "test" {
		t.Errorf("project = %q, want %q", found.Project, "test")
	}
}

func TestJobRepo_GetByElement(t *testing.T) {
	db := setupTestDBWithJobs(t)
	repo := NewJobRepository(db)
	ctx := context.Background()

	elem := model.NewImageElement("test", "a dog", 512, 512, 20, 7.0, 99, "v1-5", "", "", "", "v1-5.ckpt")
	record := FromDomain(elem, "sdcpp_job_2", "generating")
	if err := repo.CreateJob(ctx, record); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	found, err := repo.GetLatestJobByElement(ctx, record.ElementID)
	if err != nil {
		t.Fatalf("GetLatestJobByElement: %v", err)
	}
	if found.SDCPPJobID != "sdcpp_job_2" {
		t.Errorf("sdcpp_job_id = %q, want %q", found.SDCPPJobID, "sdcpp_job_2")
	}
	if found.Status != "generating" {
		t.Errorf("status = %q, want %q", found.Status, "generating")
	}
}

func TestJobRepo_UpdateStatus(t *testing.T) {
	db := setupTestDBWithJobs(t)
	repo := NewJobRepository(db)
	ctx := context.Background()

	elem := model.NewImageElement("test", "a bird", 512, 512, 20, 7.0, 77, "v1-5", "", "", "", "v1-5.ckpt")
	record := FromDomain(elem, "sdcpp_job_3", "queued")
	if err := repo.CreateJob(ctx, record); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	// Update to completed — use the job's UUID, not the element ID.
	if err := repo.UpdateStatus(ctx, record.ID, "completed",
		sql.NullString{Valid: false}, sql.NullTime{}); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	found, err := repo.GetJob(ctx, record.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if found.Status != "completed" {
		t.Errorf("status = %q, want %q", found.Status, "completed")
	}
}

func TestJobRepo_UpdateStatusWithError(t *testing.T) {
	db := setupTestDBWithJobs(t)
	repo := NewJobRepository(db)
	ctx := context.Background()

	elem := model.NewImageElement("test", "a fish", 512, 512, 20, 7.0, 88, "v1-5", "", "", "", "v1-5.ckpt")
	record := FromDomain(elem, "sdcpp_job_4", "generating")
	if err := repo.CreateJob(ctx, record); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	errMsg := "out of memory"
	// Use the job's UUID, not the element ID.
	if err := repo.UpdateStatus(ctx, record.ID, "failed",
		sql.NullString{Valid: true, String: errMsg}, sql.NullTime{}); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	found, err := repo.GetJob(ctx, record.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if found.Status != "failed" {
		t.Errorf("status = %q, want %q", found.Status, "failed")
	}
	if !found.ErrorMessage.Valid || found.ErrorMessage.String != errMsg {
		t.Errorf("error_msg = %v, want %q", found.ErrorMessage, errMsg)
	}
}

func TestJobRepo_ListActiveJobs(t *testing.T) {
	db := setupTestDBWithJobs(t)
	repo := NewJobRepository(db)
	ctx := context.Background()

	// Create 2 active jobs and 1 completed job.
	for i, seed := range []int64{10, 20} {
		elem := model.NewImageElement("test", "active", 512, 512, 20, 7.0, seed, "v1-5", "", "", "", "v1-5.ckpt")
		record := FromDomain(elem, "sdcpp_active_"+string(rune(i)), "queued")
		if err := repo.CreateJob(ctx, record); err != nil {
			t.Fatalf("CreateJob %d: %v", i, err)
		}
	}

	// One completed job.
	elem := model.NewImageElement("test", "done", 512, 512, 20, 7.0, 30, "v1-5", "", "", "", "v1-5.ckpt")
	record := FromDomain(elem, "sdcpp_done", "completed")
	if err := repo.CreateJob(ctx, record); err != nil {
		t.Fatalf("CreateJob completed: %v", err)
	}

	active, err := repo.ListActiveJobs(ctx, "test")
	if err != nil {
		t.Fatalf("ListActiveJobs: %v", err)
	}
	if len(active) != 2 {
		t.Errorf("got %d active jobs, want 2", len(active))
	}
	for _, j := range active {
		if j.Status != "queued" {
			t.Errorf("active job status = %q, want queued", j.Status)
		}
	}
}

func TestJobRepo_DeleteJob(t *testing.T) {
	db := setupTestDBWithJobs(t)
	repo := NewJobRepository(db)
	ctx := context.Background()

	elem := model.NewImageElement("test", "delete test", 512, 512, 20, 7.0, 42, "v1-5", "", "", "", "v1-5.ckpt")
	record := FromDomain(elem, "sdcpp_delete_test", "queued")
	if err := repo.CreateJob(ctx, record); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	// Verify the job exists (use the job's unique ID, not element ID).
	found, err := repo.GetJob(ctx, record.ID)
	if err != nil {
		t.Fatalf("GetJob before delete: %v", err)
	}
	if found.SDCPPJobID != "sdcpp_delete_test" {
		t.Errorf("sdcpp_job_id = %q, want %q", found.SDCPPJobID, "sdcpp_delete_test")
	}

	// Delete the job.
	if err := repo.DeleteJob(ctx, record.ID); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}

	// Verify the job is gone.
	_, err = repo.GetJob(ctx, record.ID)
	if err == nil {
		t.Fatal("expected error for deleted job")
	}
}

func TestJobRepo_GetNotFound(t *testing.T) {
	db := setupTestDBWithJobs(t)
	repo := NewJobRepository(db)
	ctx := context.Background()

	_, err := repo.GetJob(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent job")
	}
}

func TestFromDomain(t *testing.T) {
	elem := model.NewImageElement("test", "prompt", 512, 512, 20, 7.0, 42, "v1-5", "", "", "", "v1-5.ckpt")
	record := FromDomain(elem, "sdcpp_123", "queued")

	// Job ID is now a unique UUID, distinct from element ID.
	if record.ID == "" {
		t.Errorf("record ID is empty, want a UUID")
	}
	if record.ID == elem.ID {
		t.Errorf("record ID = %q, should NOT equal element ID (each job gets a unique UUID)", record.ID)
	}
	if record.SDCPPJobID != "sdcpp_123" {
		t.Errorf("sdcpp_job_id = %q, want %q", record.SDCPPJobID, "sdcpp_123")
	}
	if record.Status != "queued" {
		t.Errorf("status = %q, want %q", record.Status, "queued")
	}
	if record.ElementID != elem.ID {
		t.Errorf("element_id = %q, want %q", record.ElementID, elem.ID)
	}
	if record.Project != "test" {
		t.Errorf("project = %q, want %q", record.Project, "test")
	}
}

