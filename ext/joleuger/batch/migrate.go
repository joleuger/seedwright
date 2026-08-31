package batch

import (
	"context"
	"database/sql"
)

// Migrate creates the Batch SQLite tables if they don't already exist.
// Idempotent — safe to call multiple times.
func Migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS ext_joleuger_batch_batches (
    id              TEXT PRIMARY KEY,
    project         TEXT NOT NULL,
    status          TEXT DEFAULT 'queued',
    prompt          TEXT,
    negative_prompt TEXT,
    width           INTEGER,
    height          INTEGER,
    sample_steps    INTEGER,
    txt_cfg         REAL,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (project) REFERENCES projects(name) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS ext_joleuger_batch_items (
    batch_id   TEXT NOT NULL,
    position   INTEGER NOT NULL,
    seed       INTEGER NOT NULL,
    prompt     TEXT,
    element_id TEXT,
    status     TEXT DEFAULT 'pending',
    PRIMARY KEY (batch_id, position),
    FOREIGN KEY (batch_id)   REFERENCES ext_joleuger_batch_batches(id) ON DELETE CASCADE,
    FOREIGN KEY (element_id) REFERENCES elements(id) ON DELETE SET NULL
);
`)
	return err
}

