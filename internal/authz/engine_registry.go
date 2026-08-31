package authz

import (
	"context"
	"sync"

	"gopkg.in/yaml.v3"
)

// EngineFactory is a function that builds an Enforcer for a named engine.
// Called by buildEnforcer when auth.engine selects an engine (built-in or
// extension). Registered via RegisterEngine and looked up by key.
//
// Extensions implement this type and register a factory in their init()
// function. The factory receives the configured IdentityResolver (already
// built by core) and a raw yaml.Node containing the extension's own config
// block (deferred decoding), so it can parse whatever configuration it needs.
type EngineFactory func(ctx context.Context, resolver IdentityResolver, rawConfig yaml.Node) (Enforcer, error)

// engineRegistry holds extension engine factories keyed by engine name
// (e.g. "ext/joleuger/authz-simple").
var (
	engineRegistry   = make(map[string]EngineFactory)
	engineRegistryMu sync.RWMutex
)

// RegisterEngine registers a factory for a named extension engine.
// Called from init() in extension packages. Must be called before
// BuildEnforcerWithConfig is invoked.
func RegisterEngine(name string, factory EngineFactory) {
	engineRegistryMu.Lock()
	defer engineRegistryMu.Unlock()
	engineRegistry[name] = factory
}

// lookupEngine returns the factory for the given engine name, or false if
// not registered.
func lookupEngine(name string) (EngineFactory, bool) {
	engineRegistryMu.RLock()
	defer engineRegistryMu.RUnlock()
	f, ok := engineRegistry[name]
	return f, ok
}
