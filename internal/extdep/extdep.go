// Package extdep implements the extension dependency system:
// declaration, startup validation (unknown keys, acyclicity, required
// dependencies enabled), deterministic construction order, and runtime
// queries (IsEnabled, IsInitialized).
//
// Bundled extensions declare dependencies through the Dependencies method
// of the ext.Extension contract. The ext package builds a *Graph at
// startup, validates it, uses it to order Migrate/Initialize, and hands
// it to extensions via app.App.ExtDeps for runtime queries.
//
// The package imports only the standard library so that internal/app can
// hold the graph without an import cycle.
package extdep

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Kind classifies how an extension depends on another bundled extension.
type Kind int

const (
	// CompileRequired: the depending extension imports the dependency's
	// Go package. The dependency must be enabled; startup fails otherwise
	// (the compiler guarantees the code exists, validation guarantees the
	// runtime services exist).
	CompileRequired Kind = iota

	// RuntimeRequired: the depending extension uses the dependency's API
	// at runtime — typically from the UI, e.g. calling the dependency's
	// HTTP endpoints — without importing its Go code. The dependency must
	// be enabled; startup fails otherwise.
	RuntimeRequired

	// RuntimeOptional: the depending extension degrades gracefully when
	// the dependency is disabled (the feature is hidden via an IsEnabled
	// check). Startup succeeds either way and no construction order is
	// imposed.
	RuntimeOptional
)

// String returns a short, config-style name for the kind.
func (k Kind) String() string {
	switch k {
	case CompileRequired:
		return "compile-required"
	case RuntimeRequired:
		return "runtime-required"
	case RuntimeOptional:
		return "runtime-optional"
	default:
		return fmt.Sprintf("kind(%d)", int(k))
	}
}

// Required reports whether a missing dependency must fail startup.
func (k Kind) Required() bool {
	return k == CompileRequired || k == RuntimeRequired
}

// Dependency is a declared dependency of one bundled extension on
// another.
type Dependency struct {
	Key  string // the dependency's extension key, "owner/name"
	Kind Kind
}

// Graph is the process-wide extension dependency registry. It is built
// and validated once at startup (ext.RegisterAll), then handed to
// extensions via app.App.ExtDeps for runtime queries.
type Graph struct {
	mu      sync.RWMutex
	decls   map[string][]Dependency // key -> declared dependencies
	enabled map[string]bool
	initd   map[string]bool
}

// NewGraph returns an empty, ready-to-register graph.
func NewGraph() *Graph {
	return &Graph{
		decls:   make(map[string][]Dependency),
		enabled: make(map[string]bool),
		initd:   make(map[string]bool),
	}
}

// Register declares an extension's dependencies. RegisterAll registers
// the enabled extensions only (a disabled extension's unmet required
// deps are harmless — it isn't running), before calling Validate.
func (g *Graph) Register(key string, deps []Dependency) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.decls[key] = append([]Dependency(nil), deps...)
}

// Validate checks the declared dependency graph:
//
//  1. every declared dependency refers to a known extension key;
//  2. the graph is acyclic (checked over all declared edges);
//  3. every required dependency of a registered extension is enabled.
//
// knownKeys must list every bundled extension key; enabledKeys the ones
// actually running. Validate also records enabledKeys for IsEnabled.
func (g *Graph) Validate(knownKeys, enabledKeys []string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	known := make(map[string]bool, len(knownKeys))
	for _, k := range knownKeys {
		known[k] = true
	}
	g.enabled = make(map[string]bool, len(enabledKeys))
	for _, k := range enabledKeys {
		g.enabled[k] = true
	}

	// 1. Unknown dependency keys (typo protection).
	for key, deps := range g.decls {
		for _, d := range deps {
			if !known[d.Key] {
				return fmt.Errorf("extension %s: unknown dependency key %q", key, d.Key)
			}
		}
	}

	// 2. Acyclicity over all declared edges (dependent -> dependency).
	if cycle := findCycle(g.decls); len(cycle) > 0 {
		return fmt.Errorf("extension dependency graph has a cycle: %s", strings.Join(cycle, " -> "))
	}

	// 3. Required dependencies of registered (enabled) extensions.
	for key, deps := range g.decls {
		for _, d := range deps {
			if d.Kind.Required() && !g.enabled[d.Key] {
				return fmt.Errorf("extension %s requires %s (%s) to be enabled", key, d.Key, d.Kind)
			}
		}
	}

	return nil
}

// Order returns the given keys in dependency-first (topological) order:
// an extension never precedes a dependency it requires. Only required
// kinds create ordering edges; optional dependencies impose no order.
// Ties preserve the argument order, so a dependency-free graph comes
// back exactly as given.
func (g *Graph) Order(keys []string) ([]string, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	pos := make(map[string]bool, len(keys))
	for _, k := range keys {
		pos[k] = true
	}

	// indeg[k] = number of required deps of k that are within `keys`;
	// dependents[dep] = keys within `keys` that require dep.
	indeg := make(map[string]int)
	dependents := make(map[string][]string)
	for _, k := range keys {
		for _, d := range g.decls[k] {
			if !d.Kind.Required() || !pos[d.Key] {
				continue
			}
			indeg[k]++
			dependents[d.Key] = append(dependents[d.Key], k)
		}
	}

	// Kahn's algorithm; among ready nodes pick the earliest in `keys`
	// (deterministic; for a dependency-free set the input order survives).
	done := make(map[string]bool, len(keys))
	out := make([]string, 0, len(keys))
	for len(out) < len(keys) {
		pick := ""
		found := false
		for _, k := range keys {
			if done[k] || indeg[k] != 0 {
				continue
			}
			pick, found = k, true
			break
		}
		if !found {
			var remaining []string
			for _, k := range keys {
				if !done[k] {
					remaining = append(remaining, k)
				}
			}
			return nil, fmt.Errorf("cannot order extensions: cycle among %v", remaining)
		}
		done[pick] = true
		out = append(out, pick)
		for _, dep := range dependents[pick] {
			indeg[dep]--
		}
	}
	return out, nil
}

// IsEnabled reports whether the extension key is in the enabled set
// recorded by Validate. Safe on a nil graph (returns false — nothing is
// enabled).
func (g *Graph) IsEnabled(key string) bool {
	if g == nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.enabled[key]
}

// MarkInitialized records that the extension finished Initialize.
// RegisterAll calls it after each successful Initialize.
func (g *Graph) MarkInitialized(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initd[key] = true
}

// IsInitialized reports whether the extension finished Initialize. With
// dependency-first construction a required dependency is always
// initialized before the dependent runs; this check makes that invariant
// assertable and is what optional dependencies use at request time.
// Safe on a nil graph (returns false).
func (g *Graph) IsInitialized(key string) bool {
	if g == nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.initd[key]
}

// findCycle returns one dependency cycle in the edge set (dependent ->
// dependency) as a path like ["a", "b", "a"], or nil if acyclic.
func findCycle(decls map[string][]Dependency) []string {
	const (
		white = iota // unvisited
		gray         // on the current DFS path
		black        // finished
	)
	color := make(map[string]int)
	var stack []string
	var cycle []string

	var visit func(key string) bool
	visit = func(key string) bool {
		color[key] = gray
		stack = append(stack, key)
		for _, d := range decls[key] {
			switch color[d.Key] {
			case white:
				if visit(d.Key) {
					return true
				}
			case gray:
				i := 0
				for j, s := range stack {
					if s == d.Key {
						i = j
						break
					}
				}
				cycle = append(append([]string{}, stack[i:]...), d.Key)
				return true
			}
		}
		stack = stack[:len(stack)-1]
		color[key] = black
		return false
	}

	keys := make([]string, 0, len(decls))
	for k := range decls {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if color[k] == white && visit(k) {
			return cycle
		}
	}
	return nil
}
