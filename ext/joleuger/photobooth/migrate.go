package photobooth

import (
	"context"
	"database/sql"
)

// Migrate adds extension columns to the projects table,
// but only on the first run. Subsequent startups find a row in extensions_metadata
// and skip the ALTER TABLE.
//
// Columns added:
//
//	ext_joleuger_photobooth_post_filter_prompt TEXT
//	ext_joleuger_photobooth_post_filter_reference_image TEXT
//	ext_joleuger_photobooth_trigger_binding TEXT
func Migrate(ctx context.Context, db *sql.DB) error {
	// Check if migration already happened.
	var version int
	err := db.QueryRowContext(ctx,
		`SELECT version FROM extensions_metadata WHERE extension_key = ?`,
		"ext_joleuger_photobooth",
	).Scan(&version)
	if err == nil {
		// Already migrated.
		return nil
	}

	// Not yet migrated — add the columns.
	for _, col := range []string{
		"ext_joleuger_photobooth_post_filter_prompt TEXT",
		"ext_joleuger_photobooth_post_filter_reference_image TEXT",
		"ext_joleuger_photobooth_trigger_binding TEXT",
	} {
		_, err = db.ExecContext(ctx,
			`ALTER TABLE projects ADD COLUMN `+col,
		)
		if err != nil {
			return err
		}
	}

	// Record the migration so we never run ALTER TABLE again.
	_, err = db.ExecContext(ctx,
		`INSERT OR IGNORE INTO extensions_metadata (extension_key, version) VALUES (?, 2)`,
		"ext_joleuger_photobooth",
	)
	return err
}
