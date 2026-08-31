// Package favorites implements element bookmarking as an extension
// to seedwright. Elements can be marked as favorites from the
// gallery or element detail page, and a dedicated favorites gallery
// page lists them.
//
// Each favorite is persisted as a per-element delta file in S3:
// `projects/{project}/ext/joleuger/favorites/elements/{element_id}.json`
// containing an id, version, and favorite flag. Files are created
// lazily on first set-to-true; default state (not favorite) has no file.
//
// See EXTENDING.md for the extension contract.
// See this package's EXTENSION.md for Favorites-specific docs.
package favorites

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"seedwright/internal/app"
	"seedwright/internal/config"
	"seedwright/internal/data/querybuilder"
	"seedwright/internal/storage"
)

// Config holds Favorites's tunable settings.
type Config struct {
	Enabled bool `yaml:"enabled"`
}

// LoadConfig returns Favorites's config from the global app config.
func LoadConfig(cfg *config.Config) (Config, error) {
	c := Config{Enabled: true}
	if err := cfg.ExtensionConfig("joleuger/favorites", &c); err != nil {
		return c, fmt.Errorf("favorites: config: %w", err)
	}
	return c, nil
}

// deltaKey returns the S3 key for an element's favorite delta file.
func deltaKey(project, elementID string) string {
	return "projects/" + project + "/ext/joleuger/favorites/elements/" + elementID + ".json"
}

// deltaPrefix returns the S3 prefix for all delta files in a project.
func deltaPrefix(project string) string {
	return "projects/" + project + "/ext/joleuger/favorites/elements/"
}

// Extension holds the Favorites extension's state and dependencies.
type Extension struct {
	db      *sql.DB
	storage storage.StorageBackend
	mux     *http.ServeMux
}

// New constructs a new Favorites extension.
func New(db *sql.DB, s storage.StorageBackend, mux *http.ServeMux) *Extension {
	return &Extension{
		db:      db,
		storage: s,
		mux:     mux,
	}
}

// favoriteDelta represents the S3 delta file for one element's favorite state.
type favoriteDelta struct {
	ID          string `json:"id"`
	Version     int    `json:"version"`
	IsFavorite  bool   `json:"is_favorite"`
}

// IsFavorite checks whether an element is marked as a favorite.
// Reads from the column on the elements table (populated by Sync at startup).
func (e *Extension) IsFavorite(ctx context.Context, project, elementID string) (bool, error) {
	var isFav int
	err := e.db.QueryRowContext(ctx,
		`SELECT ext_joleuger_favorites_is_favorite FROM elements WHERE id = ?`,
		elementID,
	).Scan(&isFav)
	if err != nil {
		return false, err
	}
	return isFav != 0, nil
}

// ToggleFavorite adds or removes a favorite. Returns the new state.
// Write direction: S3 delta file first, then SQLite column.
func (e *Extension) ToggleFavorite(ctx context.Context, elementID, project string) (bool, error) {
	key := deltaKey(project, elementID)

	// 1. Read current delta from S3.
	var d favoriteDelta
	body, _, err := e.storage.GetObject(ctx, key)
	if err == nil && body != nil {
		data, rErr := io.ReadAll(body)
		body.Close()
		if rErr == nil {
			json.Unmarshal(data, &d)
		}
	}
	// If no delta file exists, default is not favorite.

	// 2. Toggle the favorite flag.
	var favorite bool
	if d.IsFavorite {
		// Remove: delete the delta file (back to default).
		_ = e.storage.DeleteObject(ctx, key)
		favorite = false
	} else {
		// Add: create or update delta file.
		d.ID = elementID
		d.IsFavorite = true
		d.Version++
		if d.Version == 0 {
			d.Version = 1
		}
		data, _ := json.Marshal(d)
		if err := e.storage.PutObject(ctx, key, strings.NewReader(string(data)), int64(len(data)), "application/json"); err != nil {
			return false, err
		}
		favorite = true
	}

	// 3. Update the column on the elements table.
	_, err = e.db.ExecContext(ctx,
		`UPDATE elements SET ext_joleuger_favorites_is_favorite = ? WHERE id = ?`,
		map[bool]int{false: 0, true: 1}[favorite], elementID,
	)
	if err != nil {
		return false, err
	}

	return favorite, nil
}

// ListFavorites returns all favorite element IDs for a project.
// Reads from the column on the elements table (populated by Sync at startup).
func (e *Extension) ListFavorites(ctx context.Context, project string) ([]string, error) {
	rows, err := e.db.QueryContext(ctx,
		`SELECT id FROM elements
		 WHERE project = ? AND ext_joleuger_favorites_is_favorite = 1
		 ORDER BY created_at DESC`,
		project,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

// CountFavorites returns the number of favorites for a project.
func (e *Extension) CountFavorites(ctx context.Context, project string) (int, error) {
	var count int
	err := e.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM elements WHERE project = ? AND ext_joleuger_favorites_is_favorite = 1`,
		project,
	).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// Register adds the favorites filter, column, and populate callback
// to the query builder registry. Extensions call this during Bootstrap
// so the core repository can use them when building ListElements queries.
func (e *Extension) Register(b *querybuilder.Builder) {
	// Filter: "favorites" query param maps to ext_joleuger_favorites_is_favorite = 1.
	b.AddFilter(querybuilder.Filter{
		Name: "favorites",
		Apply: func(q *querybuilder.Query, value any) {
			if _, ok := value.(string); !ok {
				return
			}
			q.AddWhere("e.ext_joleuger_favorites_is_favorite = ?", 1)
		},
	})

	// Column: always select the extension column so ListElements can
	// populate elem.IsFavorite after scanning.
	b.AddColumn("e.ext_joleuger_favorites_is_favorite")

	// Populate callback: set elem.IsFavorite from the scanned column value.
	// This is registered as a column because the core's ListElements
	// iterates over registered columns and populates element fields.
}

// NewExtension constructs a Favorites extension from an App instance.
// This is the entrypoint called from ext.RegisterAll.
func NewExtension(ctx context.Context, a *app.App) (*Extension, error) {
	ext := New(a.DB, a.Storage, a.GetServeMux())
	// Register the query builder contributions.
	ext.Register(a.QueryBuilder)
	// Register hooks (NavBar, ElementActions).
	ext.RegisterHooks(a)
	// Register HTTP routes.
	ext.RegisterRoutes(a)
	return ext, nil
}

// RegisterHooks appends favorites' hooks to the app's hook slices.
func (e *Extension) RegisterHooks(a *app.App) {
	if a.Hooks != nil {
		// NavBarItems: render the Favorites nav link.
		a.Hooks.NavBarItems = append(a.Hooks.NavBarItems, func(ctx context.Context, project string) (template.HTML, error) {
			count, err := e.CountFavorites(ctx, project)
			if err != nil {
				slog.Warn("favorites: count", "project", project, "error", err)
				count = 0
			}
			label := "Favorites"
			if count > 0 {
				label = "Favorites (" + itoa(count) + ")"
			}
			return template.HTML(`<a href="/basic/` + project + `/gallery?favorites=true" title="` + label + `">` + label + `</a>`), nil
		})

		// ElementActions: render the star/favorite toggle button.
		a.Hooks.ElementActions = append(a.Hooks.ElementActions, func(ctx context.Context, project, elementID string) (template.HTML, error) {
			isFav, err := e.IsFavorite(ctx, project, elementID)
			if err != nil {
				slog.Warn("favorites: is_favorite", "element", elementID, "error", err)
				isFav = false
			}
			icon := "☆"
			if isFav {
				icon = "⭐"
			}
			return template.HTML(`<button class="btn btn-secondary" onclick="toggleFavorite(` + quote(project) + `, ` + quote(elementID) + `, this)" title="Toggle favorite" style="padding:0.4rem 0.8rem;">` + icon + ` Favorite</button>`), nil
		})
	}
}

// quote wraps a string in single quotes for inline JS.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "\\'") + "'"
}

// Sync runs after core's SyncFromStorage. It reads all delta files from S3
// and populates the SQLite favorites table.
func Sync(ctx context.Context, a *app.App) error {
	// List all projects from SQLite (populated by core's SyncFromStorage).
	rows, err := a.DB.QueryContext(ctx, `SELECT name FROM projects`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var projects []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		projects = append(projects, name)
	}

	// Sync each project.
	for _, project := range projects {
		if err := syncFromS3(ctx, a.Storage, a.DB, project); err != nil {
			slog.Warn("favorites: sync failed", "project", project, "error", err)
		}
	}

	return nil
}

// syncFromS3 loads all delta files for a project from S3 and populates the
// ext_joleuger_favorites_is_favorite column on the elements table.
func syncFromS3(ctx context.Context, store storage.StorageBackend, db *sql.DB, project string) error {
	// List all delta files for the project.
	objects, err := store.ListObjects(ctx, deltaPrefix(project))
	if err != nil {
		return err
	}

	var dangling []string
	for _, obj := range objects {
		body, _, err := store.GetObject(ctx, obj.Key)
		if err != nil {
			slog.Warn("favorites: read delta file", "key", obj.Key, "error", err)
			continue
		}
		data, rErr := io.ReadAll(body)
		body.Close()
		if rErr != nil {
			slog.Warn("favorites: read delta file", "key", obj.Key, "error", rErr)
			continue
		}

		var d favoriteDelta
		if err := json.Unmarshal(data, &d); err != nil {
			slog.Warn("favorites: parse delta file", "key", obj.Key, "error", err)
			continue
		}

		// Check that the element still exists in the elements table.
		var exists int
		err = db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM elements WHERE id = ?`, d.ID,
		).Scan(&exists)
		if err != nil || exists == 0 {
			dangling = append(dangling, obj.Key)
			continue
		}

		// Populate the column (0 = not favorite, 1 = favorite).
		_, err = db.ExecContext(ctx,
			`UPDATE elements SET ext_joleuger_favorites_is_favorite = ? WHERE id = ?`,
			map[bool]int{false: 0, true: 1}[d.IsFavorite], d.ID,
		)
		if err != nil {
			slog.Warn("favorites: update column from S3", "element", d.ID, "error", err)
		}
	}

	// Clean up orphaned delta files (elements that no longer exist).
	for _, key := range dangling {
		_ = store.DeleteObject(ctx, key)
	}
	if len(dangling) > 0 {
		slog.Warn("favorites: cleaned up orphaned delta files", "removed", len(dangling))
	}

	return nil
}
