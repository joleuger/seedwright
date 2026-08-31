package data

import (
	"os"
	"testing"
)

func TestOpenSQLite_createsSchema(t *testing.T) {
	path := writeTempDB(t)
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite() error: %v", err)
	}
	defer db.Close()

	// Verify tables exist.
	for _, table := range []string{"projects", "elements", "jobs", "app_settings", "extensions_metadata"} {
		var exists int
		err := db.QueryRow(
			"SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?",
			table,
		).Scan(&exists)
		if err != nil {
			t.Fatal(err)
		}
		if exists != 1 {
			t.Errorf("table %q does not exist", table)
		}
	}

	// Verify indexes exist.
	for _, idx := range []string{
		"idx_elements_project",
		"idx_elements_project_created",
		"idx_elements_model",
		"idx_elements_seed",
		"idx_jobs_element",
		"idx_jobs_project",
		"idx_jobs_status",
	} {
		var exists int
		err := db.QueryRow(
			"SELECT count(*) FROM sqlite_master WHERE type='index' AND name=?",
			idx,
		).Scan(&exists)
		if err != nil {
			t.Fatal(err)
		}
		if exists != 1 {
			t.Errorf("index %q does not exist", idx)
		}
	}
}

func TestOpenSQLite_inMemory(t *testing.T) {
	db, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite() error: %v", err)
	}
	defer db.Close()

	var count int
	err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table'").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 6 {
		t.Errorf("expected 6 tables, got %d", count)
	}
}

func TestCreateSchema_idempotent(t *testing.T) {
	path := writeTempDB(t)
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Running CreateSchema again should not error.
	if err := CreateSchema(db); err != nil {
		t.Fatalf("CreateSchema() second call error: %v", err)
	}
}

func writeTempDB(t *testing.T) string {
	f, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}
