package data

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

// CreateSchema creates all tables and indexes in the given database.
// It is idempotent — running it multiple times has no effect.
func CreateSchema(db *sql.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS projects (
	name          TEXT PRIMARY KEY,
	schema_version INTEGER DEFAULT 1,
	version       INTEGER DEFAULT 1,
	created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
	updated_at    DATETIME,
	synced_at     DATETIME,
	hidden        INTEGER DEFAULT 0,
	backend_ref   TEXT DEFAULT '',
	description   TEXT DEFAULT '',
	tags          TEXT DEFAULT '[]',
	friendly_name TEXT DEFAULT '',
	primary_owner TEXT DEFAULT ''
);

CREATE TABLE IF NOT EXISTS elements (
	id               TEXT PRIMARY KEY,
	version          INTEGER DEFAULT 1,
	project          TEXT NOT NULL,
	created_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
	project_location TEXT,
	etag             TEXT,
	synced_at        DATETIME,

	-- Extracted for fast queries. model_name/prompt/seed/width/height/
	-- sample_steps/txt_cfg/duration now come from element.generation.*
	-- (was top-level before the Generation restructure) — null for any
	-- element with no Generation at all (origin = "uploaded").
	origin           TEXT DEFAULT 'generated',
	model_name       TEXT,
	prompt           TEXT,
	seed             INTEGER,
	width            INTEGER,
	height           INTEGER,
	sample_steps     INTEGER,
	txt_cfg          REAL,
	duration         REAL,

	FOREIGN KEY (project) REFERENCES projects(name)
);

CREATE INDEX IF NOT EXISTS idx_elements_project ON elements(project);
CREATE INDEX IF NOT EXISTS idx_elements_project_created ON elements(project, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_elements_model ON elements(model_name);
CREATE INDEX IF NOT EXISTS idx_elements_seed ON elements(seed);
CREATE INDEX IF NOT EXISTS idx_elements_origin ON elements(project, origin);

-- Ordered reference-image list per element, projected from the element's
-- own "generation.reference_images" JSON array for querying/navigation. Core, not an
-- extension — every generated or uploaded element can participate.
CREATE TABLE IF NOT EXISTS element_references (
	element_id     TEXT NOT NULL,   -- the element that HAS references
	position       INTEGER NOT NULL,
	ref_element_id TEXT NOT NULL,   -- the element being referenced (no FK —
	                                -- order is arbitrary during sync; orphaned
	                                -- refs are harmless, the JOIN query
	                                -- silently skips missing elements)
	PRIMARY KEY (element_id, position),
	FOREIGN KEY (element_id) REFERENCES elements(id) ON DELETE CASCADE
	-- ref_element_id intentionally has no FK constraint: S3 object listing
	-- order is arbitrary, and a referenced element may be deleted between
	-- sync runs. Without the FK, sync is a single pass and orphans are safe.
	-- The element_id FK retains ON DELETE CASCADE so deleting an element
	-- still cleans up its own reference rows.
);

-- Forward direction ("this element's references, in order") is already
-- served by the primary key. This index is for the reverse direction —
-- "what else used this element as a reference" — the actual navigation case.
CREATE INDEX IF NOT EXISTS idx_element_references_ref ON element_references(ref_element_id);

CREATE TABLE IF NOT EXISTS jobs (
	id              TEXT PRIMARY KEY,  -- unique job submission UUID (not element_id)
	element_id      TEXT,              -- the element this job belongs to (non-unique)
	project         TEXT NOT NULL,
	sdcpp_job_id    TEXT,
	status          TEXT DEFAULT 'queued',
	error_msg       TEXT,
	sdcpp_started   DATETIME,
	sdcpp_completed DATETIME,
	FOREIGN KEY (project) REFERENCES projects(name)
	-- element_id FK intentionally omitted: jobs are created before elements
	-- in StartJob (CreateJob → submitJob → CreateElement), so the FK would
	-- fail because elements(id) doesn't exist yet. The relationship is
	-- maintained at the application layer: each job has a unique id (UUID)
	-- and an element_id pointing to the element it generates.
);

CREATE INDEX IF NOT EXISTS idx_jobs_element ON jobs(element_id);
CREATE INDEX IF NOT EXISTS idx_jobs_project ON jobs(project);
CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);

-- App-wide settings (key-value store).
CREATE TABLE IF NOT EXISTS app_settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

-- Extension migration tracker.
CREATE TABLE IF NOT EXISTS extensions_metadata (
	extension_key TEXT PRIMARY KEY,
	version       INTEGER NOT NULL
);

`
	_, err := db.Exec(schema)
	return err
}

// MigrateSchema adds any new columns or tables that were introduced after
// this database was first created. It is idempotent — running it on a
// database that already has the latest schema is a no-op.
//
// PAUSED — until the first public release. At that point we only need to
// remove this pause comment and add version-gating (extensions_metadata
// entry for 'core') so migrations only run when the actual schema evolves.
// See PROJECT-META.md for the migration design.
func MigrateSchema(db *sql.DB) error {
	// Add origin column if it doesn't exist.
	_, err := db.Exec(`ALTER TABLE elements ADD COLUMN origin TEXT DEFAULT 'generated'`)
	if err != nil {
		// Column already exists or other error — treat as no-op.
		// SQLite returns "duplicate column name" for existing columns.
	}

	// Add schema_version column if it doesn't exist.
	_, err = db.Exec(`ALTER TABLE elements ADD COLUMN schema_version INTEGER DEFAULT 1`)
	if err != nil {
		// Column already exists.
	}

	// Add idx_elements_origin if it doesn't exist.
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_elements_origin ON elements(project, origin)`)
	if err != nil {
		// Index already exists.
	}

	// Add project.json columns if they don't exist.
	_, err = db.Exec(`ALTER TABLE projects ADD COLUMN schema_version INTEGER DEFAULT 1`)
	if err != nil {
		// Column already exists.
	}
	_, err = db.Exec(`ALTER TABLE projects ADD COLUMN updated_at DATETIME`)
	if err != nil {
		// Column already exists.
	}
	_, err = db.Exec(`ALTER TABLE projects ADD COLUMN description TEXT DEFAULT ''`)
	if err != nil {
		// Column already exists.
	}
	_, err = db.Exec(`ALTER TABLE projects ADD COLUMN tags TEXT DEFAULT '[]'`)
	if err != nil {
		// Column already exists.
	}

	return nil
}

// OpenSQLite opens the database at the given path, creates all tables
// and indexes, enables foreign keys, and returns the usable *sql.DB.
// The database is disposable — it is fully rebuilt from S3 on every startup.
func OpenSQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}

	// SQLite settings.
	db.SetMaxOpenConns(1) // single writer
	db.SetMaxIdleConns(1)

	// Enable foreign key enforcement — required for extension ON DELETE CASCADE.
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	if err := CreateSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	// Run any post-creation migrations (adds columns that didn't exist
	// when the database was first created). PAUSED until public release.
	// MigrateSchema currently only adds columns that CreateSchema already
	// creates, so it's a no-op on a fresh DB. Once version-gated, it will
	// handle upgrades from pre-snapshot databases.
	if err := MigrateSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}
