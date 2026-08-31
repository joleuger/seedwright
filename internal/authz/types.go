// Package authz implements access control: Principal + Action + Scope → allow/deny.
//
// This is the core contract package — it defines the types, the Enforcer
// interface, and the StaticEnforcer built-in. Everything RBAC-specific
// (Role, RoleAssignment, RoleManager, ConfigAdapter, policy evaluation)
// lives in the extension package: ext/joleuger/authz-simple.
package authz

import (
	"fmt"
	"strings"
)

// --- Core vocabulary ---

// Principal is an opaque string identifying who or whatever is asking to do
// something. Valid values follow the prefix convention:
//
//	user:<name>
//	group:<name>
//	svc:<name>
//	anonymous
//
// "anonymous" is the one bare, un-prefixed reserved value — no user, group,
// or service-principal name may equal "anonymous".
type Principal string

const principalPrefixes = "user:group:svc:"

// IsAnonymous reports whether p is the reserved anonymous principal.
func (p Principal) IsAnonymous() bool {
	return string(p) == "anonymous"
}

// HasPrefix checks whether p uses a known principal prefix (user:, group:, or
// svc:). Returns false for anonymous and for unrecognized prefixes.
func (p Principal) HasPrefix() bool {
	s := string(p)
	if s == "anonymous" {
		return false
	}
	return strings.HasPrefix(s, "user:") || strings.HasPrefix(s, "group:") || strings.HasPrefix(s, "svc:")
}

// Action is a grantable operation on a role definition, mirroring Azure RBAC's
// own term (and also AWS IAM's own term for the same concept).
type Action string

const (
	ActionView              Action = "view"
	ActionGenerate          Action = "generate"
	ActionDeleteElement     Action = "delete_element"
	ActionManagePermissions Action = "manage_permissions"
	ActionDeleteProject     Action = "delete_project"
	ActionCreateProject     Action = "create_project" // global-scoped
)

// ScopeType identifies the kind of scope a RoleAssignment targets.
type ScopeType string

const (
	ScopeGlobal  ScopeType = "global"
	ScopeProject ScopeType = "project"
)

// ScopeRef identifies where a role assignment applies.
type ScopeRef struct {
	Type ScopeType
	ID   string // empty for global, project slug otherwise
}

// String returns the string representation: "global" or "project:<id>".
func (s ScopeRef) String() string {
	if s.Type == ScopeGlobal {
		return "global"
	}
	return "project:" + s.ID
}

// ParseScopeRef parses a string like "global" or "project:birthday-2026"
// into a ScopeRef. Returns an error for unknown scope types.
func ParseScopeRef(s string) (ScopeRef, error) {
	if s == "global" {
		return ScopeRef{Type: ScopeGlobal}, nil
	}
	if !strings.HasPrefix(s, "project:") {
		return ScopeRef{}, fmt.Errorf("scope %q: expected \"global\" or \"project:<id>\"", s)
	}
	return ScopeRef{Type: ScopeProject, ID: s[len("project:"):]}, nil
}

// --- Built-in groups (computed membership, not config entries) ---

// BuiltinEveryone is the "every principal including anonymous" catch-all.
// Named after the Windows well-known SID Everyone (S-1-1-0).
const BuiltinEveryone = "group:everyone"

// BuiltinAuthenticated is the "every principal except anonymous" catch-all.
// Named after the Windows well-known SID Authenticated Users (S-1-5-11).
const BuiltinAuthenticated = "group:authenticated"

// IsBuiltInGroup reports whether p is one of the two reserved built-in
// group names. Neither has a members list in config.
func IsBuiltInGroup(p Principal) bool {
	return p == BuiltinEveryone || p == BuiltinAuthenticated
}

// ResolveBuiltInGroups returns the two built-in group principals for
// convenience (not config entries).
func ResolveBuiltInGroups() [2]Principal {
	return [2]Principal{BuiltinEveryone, BuiltinAuthenticated}
}

// --- Validation helpers ---

// ValidatePrincipal checks that p follows the prefix convention or is the
// reserved anonymous value. It returns an error for unrecognized formats.
func ValidatePrincipal(p Principal) error {
	s := string(p)
	if s == "anonymous" {
		return nil
	}
	if strings.HasPrefix(s, "user:") {
		if strings.Contains(s, ":") && s[5:] == "" {
			return fmt.Errorf("principal %q: empty user name after user: prefix", p)
		}
		return nil
	}
	if strings.HasPrefix(s, "group:") {
		if strings.Contains(s, ":") && s[6:] == "" {
			return fmt.Errorf("principal %q: empty group name after group: prefix", p)
		}
		return nil
	}
	if strings.HasPrefix(s, "svc:") {
		if strings.Contains(s, ":") && s[4:] == "" {
			return fmt.Errorf("principal %q: empty service principal name after svc: prefix", p)
		}
		return nil
	}
	return fmt.Errorf("principal %q: unrecognized format (must match user:<name>, group:<name>, svc:<name>, or anonymous)", p)
}
