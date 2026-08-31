package authz_simple

import (
	"context"
	"fmt"
	"net/http"

	"seedwright/internal/authz"
)

// SimpleEnforcer implements authz.Enforcer with the full RBAC engine.
// It ties together RoleManager, role assignments, and group membership
// into a single Enforce(principal, action, scope) → bool call.
type SimpleEnforcer struct {
	// assignments are role assignments loaded from the Adapter.
	assignments []RoleAssignment

	// roles tracks the RoleManager for group membership resolution.
	roles *RoleManager

	// resolver resolves incoming requests to a Principal.
	resolver authz.IdentityResolver

	// groupMembership is the parsed group membership from Config.
	// Built-in groups are NOT stored here — they are computed at evaluation.
	groupMembership map[authz.Principal][]authz.Principal
}

// NewSimpleEnforcer builds a SimpleEnforcer, loads policy from the Adapter,
// registers group memberships with the RoleManager, and guarantees the
// bootstrap default row (user:root → global admin) exists.
func NewSimpleEnforcer(adapter Adapter, resolver authz.IdentityResolver) (*SimpleEnforcer, error) {
	assignments, groups, err := adapter.LoadPolicy()
	if err != nil {
		return nil, fmt.Errorf("load policy: %w", err)
	}

	rm := NewRoleManager()

	// Register user-defined group memberships with RoleManager.
	for group, members := range groups {
		for _, member := range members {
			rm.AddLink(member, group)
		}
	}

	e := &SimpleEnforcer{
		assignments:     assignments,
		roles:           rm,
		resolver:        resolver,
		groupMembership: groups,
	}

	// Guarantee the bootstrap default row exists.
	e.ensureBootstrapDefault()

	return e, nil
}

// ensureBootstrapDefault injects the root global-admin row if no assignment
// for user:root at the global scope exists in the loaded assignments.
func (e *SimpleEnforcer) ensureBootstrapDefault() {
	hasRootGlobal := false
	for _, ra := range e.assignments {
		if ra.Principal == "user:root" && ra.Scope.Type == authz.ScopeGlobal {
			hasRootGlobal = true
			break
		}
	}
	if !hasRootGlobal {
		e.assignments = append(e.assignments, BootstrapDefaultRoleAssignment)
	}
}

// Resolve extracts a Principal from an incoming request using the configured
// IdentityResolver.
func (e *SimpleEnforcer) Resolve(r *http.Request) authz.Principal {
	return e.resolver.Resolve(r)
}

// Enforce checks whether principal is allowed to perform action at scope.
//
// Evaluation semantics: effective role = the highest-privilege role across
// every applicable assignment. Additive only. No explicit deny.
//
// For a given principal and target project, the applicable assignments are:
//   1. Any assignment directly on project:X for that principal.
//   2. Any assignment on global for that principal.
//   3. Both of the above, repeated for every group the principal is a member
//      of — including the two built-in groups.
//
// No assignment at any level → no access at all (implicit deny by absence).
func (e *SimpleEnforcer) Enforce(ctx context.Context, principal authz.Principal, action authz.Action, scope authz.ScopeRef) bool {
	// For global-scoped actions, we check against the global scope.
	// For project-scoped actions, we check against both project and global.
	scopesToCheck := []authz.ScopeRef{scope}
	if scope.Type != authz.ScopeGlobal {
		scopesToCheck = append(scopesToCheck, authz.ScopeRef{Type: authz.ScopeGlobal})
	}

	effective := e.effectiveRoleForPrincipal(principal, scopesToCheck)

	if len(effective) == 0 {
		return false
	}

	maxRole, err := MaxRole(effective...)
	if err != nil {
		return false
	}

	return roleActionCovered(maxRole, action)
}

// effectiveRoleForPrincipal returns all roles applicable to the given principal
// for the given scopes. This includes direct assignments and group-based
// assignments.
func (e *SimpleEnforcer) effectiveRoleForPrincipal(principal authz.Principal, scopes []authz.ScopeRef) []Role {
	var roles []Role

	applicable := []authz.Principal{principal}
	applicable = append(applicable, e.resolvedGroups(principal)...)

	for _, ap := range applicable {
		for _, scope := range scopes {
			for _, ra := range e.assignments {
				if ra.Principal != ap {
					continue
				}
				if !scopesMatch(ra.Scope, scope) {
					continue
				}
				roles = append(roles, ra.Role)
			}
		}
	}

	return roles
}

// resolvedGroups returns the group principals that principal belongs to.
// This includes user-defined groups (via RoleManager) and built-in groups
// (computed dynamically).
func (e *SimpleEnforcer) resolvedGroups(principal authz.Principal) []authz.Principal {
	var groups []authz.Principal

	groups = append(groups, authz.BuiltinEveryone)
	if !principal.IsAnonymous() {
		groups = append(groups, authz.BuiltinAuthenticated)
	}

	for group := range e.groupMembership {
		if e.roles.HasLink(principal, group) {
			groups = append(groups, group)
		}
	}

	return groups
}

// scopesMatch returns true if raScope matches targetScope.
func scopesMatch(raScope, targetScope authz.ScopeRef) bool {
	if raScope.Type != targetScope.Type {
		return false
	}
	if raScope.Type == authz.ScopeProject {
		return raScope.ID == targetScope.ID
	}
	return true
}

// Rebuild reloads the enforcer's role assignments and group memberships from
// the configured Adapter.
func (e *SimpleEnforcer) Rebuild() error {
	return nil // no-op for config-backed enforcer
}

// SortedAssignments returns a copy of the enforcer's assignments, sorted for
// deterministic output.
func (e *SimpleEnforcer) SortedAssignments() []RoleAssignment {
	cpy := make([]RoleAssignment, len(e.assignments))
	copy(cpy, e.assignments)
	return cpy
}
