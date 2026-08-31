package onboarding

import (
	"context"
	"database/sql"

	"seedwright/internal/app"
	"seedwright/internal/config"
	"seedwright/internal/extdep"
)

// OnboardingKey is this extension's registry key. It is also the value
// of the application.onboarding config key that selects it.
const OnboardingKey = "joleuger/onboarding"

// descriptor is the onboarding extension's entry in ext.Bundled.
type descriptor struct{}

func (descriptor) Key() string { return OnboardingKey }

func (d descriptor) Enabled(cfg *config.Config) (bool, error) {
	c, err := LoadConfig(cfg)
	if err != nil {
		return false, err
	}
	if !c.Enabled {
		return false, nil
	}
	// application.onboarding selects the active onboarding provider.
	// Empty = default (this extension); "none" or another key = off.
	sel := cfg.Application.Onboarding
	if sel == "" {
		sel = OnboardingKey
	}
	return sel == OnboardingKey, nil
}

func (descriptor) Dependencies() []extdep.Dependency { return nil }

func (descriptor) Migrate(context.Context, *sql.DB) error { return nil }

func (d descriptor) Initialize(ctx context.Context, a *app.App) error {
	_, err := NewExtension(ctx, a)
	return err
}

func (descriptor) Sync(context.Context, *app.App) error { return nil }

// Descriptor is the onboarding extension's entry in ext.Bundled.
var Descriptor = descriptor{}
