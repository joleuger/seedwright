package ext

import (
	"context"
	"database/sql"

	"seedwright/internal/app"
	"seedwright/internal/config"
	"seedwright/internal/extdep"
)

// Extension is the lifecycle contract every bundled extension
// implements. Each extension package (ext/{owner}/{name}) exports its
// implementation as `var Descriptor` and adds it to Bundled. Conformance
// is checked at that assignment (structural typing — extension packages
// never import ext, which keeps the import graph acyclic:
//
//	ext -> extension -> internal/app, internal/extdep
//	ext -> internal/extdep
//	internal/app -> internal/extdep
//
// The dependency *types* live in internal/extdep (stdlib-only) so that
// internal/app can hold the graph (App.ExtDeps) without a cycle.
type Extension interface {
	// Key returns the extension key "owner/name" — the config path
	// (extensions.{key}), the S3 settings prefix, and the template/JS
	// lookup key (hasExtension / window.__EXTENSIONS__).
	Key() string

	// Enabled reports whether the extension is enabled in cfg (the
	// per-extension "enabled" flag, defaulting to true when the config
	// block is absent).
	Enabled(cfg *config.Config) (bool, error)

	// Dependencies declares dependencies on other bundled extensions.
	// Machine-checked by RegisterAll (unknown keys, cycles, required deps
	// enabled) and documented in the extension's EXTENSION.md.
	Dependencies() []extdep.Dependency

	// Migrate creates/updates the extension's schema. Runs before any
	// Initialize, in construction order, so shared registries (e.g.
	// querybuilder columns) are ready. No schema: return nil.
	Migrate(ctx context.Context, db *sql.DB) error

	// Initialize constructs the extension instance: loads config,
	// registers hooks and routes. Runs in dependency-first order; the
	// graph marks the key initialized after it returns.
	Initialize(ctx context.Context, a *app.App) error

	// Sync rebuilds the extension's SQLite state from S3 after the core
	// sync. Runs in reverse construction order. No state: return nil.
	Sync(ctx context.Context, a *app.App) error
}
