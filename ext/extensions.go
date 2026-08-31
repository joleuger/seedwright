// Package ext wires up bundled extensions: builds their services,
// appends hooks, runs schema migration, and registers routes.
// Each extension is gated by an "enabled" config flag (default true).
// When disabled, no tables are created, no routes are registered,
// and no hooks or sync runs.
//
// This is the one file to touch to enable, disable, or reorder an
// extension: add or remove its Descriptor in Bundled.
package ext

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"seedwright/ext/joleuger/batch"
	"seedwright/ext/joleuger/console_code_authorizer"
	"seedwright/ext/joleuger/favorites"
	"seedwright/ext/joleuger/imageproc"
	"seedwright/ext/joleuger/onboarding"
	"seedwright/ext/joleuger/photobooth"
	"seedwright/ext/joleuger/printer"
	"seedwright/ext/joleuger/slideshow"
	"seedwright/internal/app"
	"seedwright/internal/config"
	"seedwright/internal/extdep"
	"seedwright/internal/server"
)

// Bundled lists every bundled extension. Hook/UI registration order
// follows this list; construction order follows the dependency graph
// (topological, ties preserve this order — kept alphabetical, so a
// dependency-free set is constructed in exactly this order).
var Bundled = []Extension{
	batch.Descriptor,
	console_code_authorizer.Descriptor,
	favorites.Descriptor,
	imageproc.Descriptor,
	onboarding.Descriptor,
	photobooth.Descriptor,
	printer.Descriptor,
	slideshow.Descriptor,
}

// RegisterAll registers every *enabled* bundled extension: resolves the
// enabled set, validates the dependency graph (unknown keys, cycles,
// required deps enabled), migrates schemas, and initializes extensions
// in dependency-first order.
func RegisterAll(ctx context.Context, a *app.App, cfg *config.Config) error {
	enabled, allKeys, err := resolveEnabled(cfg)
	if err != nil {
		return err
	}

	// Build + validate the dependency graph.
	g := extdep.NewGraph()
	for _, e := range enabled {
		g.Register(e.Key(), e.Dependencies())
	}
	if err := g.Validate(allKeys, keys(enabled)); err != nil {
		return err
	}

	ordered, err := g.Order(keys(enabled))
	if err != nil {
		return err
	}
	byKey := make(map[string]Extension, len(enabled))
	for _, e := range enabled {
		byKey[e.Key()] = e
	}
	orderedExts := make([]Extension, len(ordered))
	for i, k := range ordered {
		orderedExts[i] = byKey[k]
	}

	// Migrate schema first so querybuilder columns are safe.
	for _, e := range orderedExts {
		if err := e.Migrate(ctx, a.DB); err != nil {
			return fmt.Errorf("%s migrate: %w", e.Key(), err)
		}
	}

	// Initialize in dependency-first order. The graph is handed to
	// extensions via a.ExtDeps before the first Initialize so handlers
	// can query it (IsEnabled / IsInitialized).
	a.ExtDeps = g
	for _, e := range orderedExts {
		slog.Info("initializing extension", "key", e.Key())
		if err := e.Initialize(ctx, a); err != nil {
			return fmt.Errorf("%s: %w", e.Key(), err)
		}
		g.MarkInitialized(e.Key())
		slog.Info("initialized extension", "key", e.Key())
	}

	// Tell templates which extensions are enabled (for hasExtension + JS).
	a.EnabledExtensions = keys(enabled)
	server.SetEnabledExtensions(a.EnabledExtensions)

	// Log enabled/skipped extensions at startup.
	if len(enabled) > 0 {
		slog.Info("enabled extensions", "count", len(enabled), "extensions", strings.Join(a.EnabledExtensions, ", "))
	}
	if len(allKeys) > len(enabled) {
		enabledSet := make(map[string]bool, len(a.EnabledExtensions))
		for _, k := range a.EnabledExtensions {
			enabledSet[k] = true
		}
		var skipped []string
		for _, k := range allKeys {
			if !enabledSet[k] {
				skipped = append(skipped, k)
			}
		}
		slog.Info("skipped extensions", "count", len(skipped), "extensions", strings.Join(skipped, ", "))
	}

	return nil
}

// SyncAll runs after core's SyncFromStorage. Each enabled extension
// rebuilds or re-links its SQLite state against the freshly rebuilt
// elements/projects tables, in reverse construction order.
func SyncAll(ctx context.Context, a *app.App, cfg *config.Config) error {
	enabled, _, err := resolveEnabled(cfg)
	if err != nil {
		return err
	}
	for i := len(enabled) - 1; i >= 0; i-- {
		if err := enabled[i].Sync(ctx, a); err != nil {
			return fmt.Errorf("%s sync: %w", enabled[i].Key(), err)
		}
	}
	return nil
}

// resolveEnabled returns the enabled extensions (in Bundled order) and
// the key list of every bundled extension.
func resolveEnabled(cfg *config.Config) ([]Extension, []string, error) {
	var enabled []Extension
	keys := make([]string, 0, len(Bundled))
	for _, e := range Bundled {
		keys = append(keys, e.Key())
		on, err := e.Enabled(cfg)
		if err != nil {
			return nil, nil, fmt.Errorf("%s config: %w", e.Key(), err)
		}
		if on {
			enabled = append(enabled, e)
		}
	}
	return enabled, keys, nil
}

// keys returns the extension keys in list order.
func keys(extensions []Extension) []string {
	out := make([]string, len(extensions))
	for i, e := range extensions {
		out[i] = e.Key()
	}
	return out
}
