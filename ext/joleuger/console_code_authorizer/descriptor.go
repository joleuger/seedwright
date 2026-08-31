package console_code_authorizer

import (
	"context"
	"database/sql"

	"seedwright/internal/app"
	"seedwright/internal/config"
	"seedwright/internal/extdep"
)

// Descriptor is the console_code_authorizer extension's entry in
// ext.Bundled.
type descriptor struct{}

func (descriptor) Key() string { return "joleuger/console_code_authorizer" }

func (d descriptor) Enabled(cfg *config.Config) (bool, error) {
	c, err := LoadConfig(cfg)
	return c.Enabled, err
}

func (descriptor) Dependencies() []extdep.Dependency { return nil }

// Migrate is a no-op: the extension stores codes in memory only.
func (descriptor) Migrate(_ context.Context, _ *sql.DB) error { return nil }

func (d descriptor) Initialize(ctx context.Context, a *app.App) error {
	_, err := NewExtension(ctx, a)
	return err
}

func (d descriptor) Sync(ctx context.Context, a *app.App) error {
	return Sync(ctx, a)
}

// Descriptor is the console_code_authorizer extension's entry in
// ext.Bundled.
var Descriptor = descriptor{}
