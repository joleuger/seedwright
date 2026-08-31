package batch

import (
	"context"
	"database/sql"

	"seedwright/internal/app"
	"seedwright/internal/config"
	"seedwright/internal/extdep"
)

// Descriptor is the batch extension's entry in ext.Bundled.
type descriptor struct{}

func (descriptor) Key() string { return "joleuger/batch" }

func (d descriptor) Enabled(cfg *config.Config) (bool, error) {
	c, err := LoadConfig(cfg)
	return c.Enabled, err
}

func (descriptor) Dependencies() []extdep.Dependency { return nil }

func (d descriptor) Migrate(ctx context.Context, db *sql.DB) error {
	return Migrate(ctx, db)
}

func (d descriptor) Initialize(ctx context.Context, a *app.App) error {
	_, err := NewExtension(ctx, a)
	return err
}

func (d descriptor) Sync(ctx context.Context, a *app.App) error {
	return Sync(ctx, a)
}

// Descriptor is the batch extension's entry in ext.Bundled.
var Descriptor = descriptor{}
