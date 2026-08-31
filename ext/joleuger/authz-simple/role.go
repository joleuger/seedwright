// Package authz_simple provides the default RBAC authorization engine.
//
// It is wired in as a substitutive extension: to enable, set
//
//	auth:
//	  engine: ext/joleuger/authz-simple
//
// in config.yaml, and populate the extension config block:
//
//	extensions:
//	  joleuger/authz-simple:
//	    groups: ...
//	    service_principals: ...
//	    role_assignments: ...
package authz_simple

import (
	"fmt"

	"seedwright/internal/authz"
)

// --- Role ---

// Role is one of four fixed roles. Named to read like Azure RBAC's own
// Owner/Contributor/Reader tier directly enough that "how does this work"
// can be answered by pointing at Microsoft's own documentation for it.
type Role string

const (
	RoleViewer      Role = "viewer"
	RoleContributor Role = "contributor"
	RoleMember      Role = "member"
	RoleAdmin       Role = "admin"
)

// rolePriority returns a numeric priority for each role. Higher = more
// privileged. The effective role for a principal is the maximum across
// every applicable assignment (additive only, no explicit deny).
func (r Role) priority() int {
	switch r {
	case RoleViewer:
		return 0
	case RoleContributor:
		return 1
	case RoleMember:
		return 2
	case RoleAdmin:
		return 3
	default:
		return -1 // unknown role
	}
}

// IsValid reports whether r is one of the four recognized roles.
func (r Role) IsValid() bool {
	switch r {
	case RoleViewer, RoleContributor, RoleMember, RoleAdmin:
		return true
	default:
		return false
	}
}

// MaxRole returns the highest-privilege role among the provided roles.
// If any role is invalid, MaxRole returns an error. If the list is empty,
// it returns the zero value.
func MaxRole(roles ...Role) (Role, error) {
	if len(roles) == 0 {
		return "", nil
	}
	var best Role
	for _, r := range roles {
		if !r.IsValid() {
			return "", fmt.Errorf("invalid role %q", r)
		}
		if r.priority() > best.priority() {
			best = r
		}
	}
	return best, nil
}

// --- RoleAssignment ---

// RoleAssignment is the literal triple: a principal, bound to a role, at a
// scope. Named after Azure RBAC's own term for the exact same concept.
type RoleAssignment struct {
	Principal authz.Principal
	Scope     authz.ScopeRef
	Role      Role
}

// --- Bootstrap default ---

// BootstrapDefaultRoleAssignment is the single row guaranteed to exist on
// first boot: root gets global admin via a real, auditable row.
var BootstrapDefaultRoleAssignment = RoleAssignment{
	Principal: "user:root",
	Scope:     authz.ScopeRef{Type: authz.ScopeGlobal},
	Role:      RoleAdmin,
}

// --- Action matrix ---

// roleActionCovered returns true if the given role grants the given action.
// This is a closed matrix — four roles, six actions.
func roleActionCovered(role Role, action authz.Action) bool {
	rp := role.priority()
	switch action {
	case authz.ActionView:
		return rp >= RoleViewer.priority()
	case authz.ActionGenerate:
		return rp >= RoleContributor.priority()
	case authz.ActionDeleteElement:
		return rp >= RoleMember.priority()
	case authz.ActionCreateProject:
		return rp >= RoleContributor.priority()
	case authz.ActionDeleteProject:
		return rp >= RoleAdmin.priority()
	case authz.ActionManagePermissions:
		return rp >= RoleMember.priority()
	default:
		return false
	}
}
