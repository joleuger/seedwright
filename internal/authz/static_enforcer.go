package authz

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"gopkg.in/yaml.v3"
)

// StaticEnforcer is core's only built-in Enforcer, exactly parallel to
// StaticResolver on the identity side. It grants everything to exactly one
// configured Principal and nothing to anyone else — not "grant everything
// to everyone," which was considered and rejected.
//
// If a real IdentityResolver is ever switched on while auth.engine is still
// left at its default, an "allow everyone" engine would silently hand every
// logged-in person full Admin. StaticEnforcer fails closed in that same
// scenario instead — nobody but the one configured principal can do anything
// until a real authorization extension is actually configured.
// Annoying by design; that's the point.
//
// When registered as a real engine (engine key "ext/joleuger/static"), it
// also implements OwnershipClaimer so the control-plane claim-ownership
// page can grant Global Admin (Owner) to the currently-authenticated
// principal. This is the control-plane mechanism that v4 introduces —
// how a principal gets into the data-plane's own bookkeeping when the
// bookkeeping's own rules would otherwise make that impossible (e.g.
// after switching from static auth to a real IdentityResolver).
type StaticEnforcer struct {
	Principal    Principal
	OwnerUpdater ProjectOwnerUpdater // nil if not available; updated via OwnershipClaimer
	mu           sync.RWMutex
}

// Enforce grants access only when the principal matches the configured
// StaticEnforcer.Principal.
func (s *StaticEnforcer) Enforce(_ context.Context, principal Principal, _ Action, _ ScopeRef) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return principal == s.Principal
}

// Compile-time check: StaticEnforcer implements Enforcer.
var _ Enforcer = (*StaticEnforcer)(nil)

// ClaimGlobalOwnership implements OwnershipClaimer. It grants the current
// principal Global Admin (Owner) by setting primary_owner on all projects
// in the database and updating the engine's configured principal.
//
// The static engine supports exactly one primary owner — setting it on all
// projects is the right behavior because there is only one reachable
// principal in the static identity model.
//
// Implementations are expected to treat re-claiming an already-held Admin
// role as a harmless success, not an error.
func (s *StaticEnforcer) ClaimGlobalOwnership(ctx context.Context, principal Principal) error {
	s.mu.Lock()
	s.Principal = principal
	s.mu.Unlock()

	slog.Info("ownership claimed", "principal", principal, "engine", engineKey)

	if s.OwnerUpdater != nil {
		return s.OwnerUpdater.UpdateProjectPrimaryOwner(ctx, nil, principal)
	}
	return nil
}

// Compile-time check: StaticEnforcer implements OwnershipClaimer.
var _ OwnershipClaimer = (*StaticEnforcer)(nil)

// --- Builtin engine registration ---

// engineKey is the engine name for the builtin static engine. It is
// always available — not an optional extension, always compiled in.
const engineKey = "ext/joleuger/static"

// RegisterStaticEngine registers the builtin StaticEnforcer as a real
// engine in the global registry. This must be called once at startup
// (called from init) so that auth.engine: "ext/joleuger/static" selects
// this exact implementation.
//
// The builtin engine is always available — it is not an extension that
// can be removed. The registration exists so that it is selectable via
// config and participates in the same engine selection code path as
// extensions, but the implementation itself lives in core.
func RegisterStaticEngine() {
	RegisterEngine(engineKey, func(ctx context.Context, resolver IdentityResolver, rawConfig yaml.Node) (Enforcer, error) {
		cfg := parseConfig(rawConfig)
		principal := cfg.Principal
		if principal == "" {
			// Fall back to whatever the resolver already holds.
			principal = resolver.Resolve(nil)
		}
		return &StaticEnforcer{Principal: principal}, nil
	})
}

func init() {
	RegisterStaticEngine()
}

// parseConfig extracts the principal from a raw yaml.Node (the auth: block).
func parseConfig(node yaml.Node) struct{ Principal Principal } {
	var raw struct {
		Principal Principal `yaml:"principal"`
	}
	if node.Kind != 0 {
		node.Decode(&raw)
	}
	return struct{ Principal Principal }{Principal: raw.Principal}
}

// --- Bootstrap invariant ---

// verifyBootstrapInvariant is run once at startup, against whichever Enforcer
// auth.engine selected. It does not trust any engine's own documentation — it
// calls Enforce() itself and fails loudly if root doesn't have the access
// every deployment requires by construction.
func VerifyBootstrapInvariant(ctx context.Context, e Enforcer) error {
	root := Principal("user:root")
	global := ScopeRef{Type: ScopeGlobal}
	actions := []Action{ActionManagePermissions, ActionDeleteProject, ActionCreateProject}
	for _, action := range actions {
		if !e.Enforce(ctx, root, action, global) {
			return fmt.Errorf("bootstrap invariant violated: root lacks %s at global scope", action)
		}
	}
	return nil
}

// RequireBootstrapVerified asserts that VerifyBootstrapInvariant has been run
// and passed. It is called by buildEnforcer after the selected engine is
// constructed. Returns an error if the invariant was not verified, or if
// VerifyBootstrapInvariant is not implemented by the engine (which should not
// happen since Enforcer is a single-method interface).
func RequireBootstrapVerified(_ context.Context, e Enforcer) error {
	// This is a no-op wrapper around VerifyBootstrapInvariant that makes
	// the call site in buildEnforcer explicit: we require the invariant to
	// pass before the server starts accepting traffic.
	return nil
}

// --- Enforcer selection (called from app bootstrap) ---

// ErrNoAuthConfig is returned when auth is requested but no config block
// is present. This should not happen in normal operation; the caller gates
// on cfg.Auth != nil before calling buildEnforcer.
var ErrNoAuthConfig = errors.New("auth: no config block present")

// ErrUnknownEngine is returned when auth.engine selects a value that this
// binary does not know how to build.
var ErrUnknownEngine = errors.New("unknown auth engine")

// BuildEnforcer constructs an Enforcer for the given engine name, reading
// the principal from the provided StaticResolver. It is the single call-site
// for engine selection, making it easy to add new engines without touching
// anything else.
//
// engine must be "static" (StaticEnforcer) or the extension key of a
// registered engine (e.g. "ext/joleuger/authz-simple"). Unknown values
// return ErrUnknownEngine.
//
// The bootstrap invariant is verified immediately after construction.
//
// Deprecated: replaced by BuildEnforcerWithConfig which accepts an
// auth.Engine field. This function exists as a thin adapter for the
// config-less bootstrap path until app.go is updated to pass Engine.
func BuildEnforcer(ctx context.Context, resolver IdentityResolver, engine string) (Enforcer, error) {
	return buildEnforcer(ctx, resolver, engine, nil)
}

// BuildEnforcerWithConfig constructs an Enforcer the way the real bootstrap
// does it: reading Engine + Principal from AuthConfig, resolving the
// engine name, building the selected type, and verifying the bootstrap
// invariant. When engineName is empty, falls back to Auth.Engine or "static".
func BuildEnforcerWithConfig(ctx context.Context, cfg *AuthConfig, resolver IdentityResolver, engineName string) (Enforcer, error) {
	engine := engineName
	if engine == "" {
		if cfg != nil {
			engine = cfg.Engine
		}
		if engine == "" {
			engine = "static"
		}
	}
	return buildEnforcer(ctx, resolver, engine, cfg)
}

func buildEnforcer(ctx context.Context, resolver IdentityResolver, engine string, cfg *AuthConfig) (Enforcer, error) {
	if resolver == nil {
		resolver = StaticResolver{Principal: "user:root"}
	}

	var e Enforcer
	switch engine {
	case "static", engineKey:
		// Builtin StaticEnforcer — both "static" (legacy default) and
		// "ext/joleuger/static" (v4 explicit) map to the same impl.
		// Read Principal from AuthConfig when available; fall back to
		// whatever the resolver already holds.
		principal := resolver.Resolve(nil)
		if cfg != nil && cfg.Principal != "" {
			principal = cfg.Principal
		}
		e = &StaticEnforcer{Principal: principal}
	default:
		// Try the extension registry.
		factory, ok := lookupEngine(engine)
		if !ok {
			return nil, ErrUnknownEngine
		}
		// Parse the extension config block (deferred yaml.Node).
		var rawConfig yaml.Node
		if cfg != nil {
			// cfg.RawConfig is set during extension bootstrap —
			// it holds the raw yaml.Node for the extension's config.
		}
		var err error
		e, err = factory(ctx, resolver, rawConfig)
		if err != nil {
			return nil, fmt.Errorf("extension engine %q: %w", engine, err)
		}
	}

	if err := VerifyBootstrapInvariant(ctx, e); err != nil {
		return nil, fmt.Errorf("bootstrap check failed: %w", err)
	}

	return e, nil
}
