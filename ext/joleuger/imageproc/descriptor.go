package imageproc

import (
	"context"
	"database/sql"

	"seedwright/internal/app"
	"seedwright/internal/config"
	"seedwright/internal/extdep"
)

// descriptor is the imageproc extension's entry in ext.Bundled.
type descriptor struct{}

func (descriptor) Key() string { return "joleuger/imageproc" }

func (d descriptor) Enabled(cfg *config.Config) (bool, error) {
	c, err := LoadConfig(cfg)
	return c.Enabled, err
}

// imageproc is a leaf extension: it depends on nothing.
func (descriptor) Dependencies() []extdep.Dependency { return nil }

// imageproc is stateless: no schema.
func (descriptor) Migrate(ctx context.Context, db *sql.DB) error { return nil }

func (d descriptor) Initialize(ctx context.Context, a *app.App) error {
	_, err := NewExtension(ctx, a)
	return err
}

// imageproc is stateless: nothing to rebuild.
func (descriptor) Sync(ctx context.Context, a *app.App) error { return nil }

// Descriptor is the imageproc extension's entry in ext.Bundled.
var Descriptor = descriptor{}
