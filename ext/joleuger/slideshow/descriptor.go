package slideshow

import (
	"context"
	"database/sql"

	"seedwright/internal/app"
	"seedwright/internal/config"
	"seedwright/internal/extdep"
)

// Descriptor is the slideshow extension's entry in ext.Bundled.
type descriptor struct{}

func (descriptor) Key() string { return "joleuger/slideshow" }

func (d descriptor) Enabled(cfg *config.Config) (bool, error) {
	c, err := LoadConfig(cfg)
	return c.Enabled, err
}

// Dependencies is nil — slideshow is a leaf. Its only dependency is the
// core elements API (GET /api/{project}/elements), which is not an
// extension and therefore not expressible as an extdep edge.
func (descriptor) Dependencies() []extdep.Dependency { return nil }

// Migrate is nil — the extension keeps no schema (stateless: the queue
// is always computed from the elements table via the core API).
func (descriptor) Migrate(ctx context.Context, db *sql.DB) error { return nil }

func (d descriptor) Initialize(ctx context.Context, a *app.App) error {
	_, err := NewExtension(ctx, a)
	return err
}

// Sync is nil — no SQLite state to rebuild.
func (descriptor) Sync(ctx context.Context, a *app.App) error { return nil }

// Descriptor is the slideshow extension's entry in ext.Bundled.
var Descriptor = descriptor{}
