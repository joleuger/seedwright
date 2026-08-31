package favorites

import (
	"context"
	"database/sql"
)

// Migrate adds the extension column to the base elements table, but only
// on the first run. Subsequent startups find a row in extensions_metadata
// and skip the ALTER TABLE.
func Migrate(ctx context.Context, db *sql.DB) error {
	// Check if migration already happened.
	var version int
	err := db.QueryRowContext(ctx,
		`SELECT version FROM extensions_metadata WHERE extension_key = ?`,
		"ext_joleuger_favorites",
	).Scan(&version)
	if err == nil {
		// Already migrated.
		return nil
	}

	// Not yet migrated — add the column.
	_, err = db.ExecContext(ctx,
		`ALTER TABLE elements ADD COLUMN ext_joleuger_favorites_is_favorite INTEGER DEFAULT 0`,
	)
	if err != nil {
		return err
	}

	// Record the migration so we never run ALTER TABLE again.
	_, err = db.ExecContext(ctx,
		`INSERT OR IGNORE INTO extensions_metadata (extension_key, version) VALUES (?, 1)`,
		"ext_joleuger_favorites",
	)
	return err
}
