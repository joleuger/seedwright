// Package printer implements image printing via the CUPS lp command
// as an extension to seedwright.
package printer

import (
	"context"
	"database/sql"
	"fmt"
)

// Migrate creates the printer extension's tables if they don't exist.
// The printer extension has no persistent state, so this is a no-op
// beyond creating the extensions_metadata row (required by the contract).
func Migrate(ctx context.Context, db *sql.DB) error {
	// Create the metadata table (shared across extensions).
	_, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS extensions_metadata (
	extension_key   TEXT PRIMARY KEY,
	version         INTEGER,
	created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);
`)
	if err != nil {
		return fmt.Errorf("printer: create extensions_metadata: %w", err)
	}

	// Register this extension's metadata row.
	// The key follows the "owner/name" format used in config.
	_, err = db.ExecContext(ctx, `
INSERT OR IGNORE INTO extensions_metadata (extension_key, version)
VALUES ('joleuger/printer', 1)
`)
	return err
}
