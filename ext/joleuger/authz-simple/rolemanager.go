package authz_simple

import (
	"sync"

	"seedwright/internal/authz"
)

// --- RoleManager ---

// RoleManager tracks group memberships for dynamic resolution at evaluation
// time. It deliberately does not handle role-assignment logic — that is the
// Enforcer's concern. RoleManager only answers: "is member a member of group?"
//
// Method names follow Casbin's own API (borrowed on purpose — see
// CASBIN_EXTRACT.md's rationale). Casbin's RoleManager also supports
// transitive inheritance (role A → role B → role C); v1 does not implement
// transitivity — membership is flat.
type RoleManager struct {
	mu    sync.RWMutex
	links map[authz.Principal]map[authz.Principal]struct{} // group → set of members
}

// NewRoleManager creates an empty RoleManager.
func NewRoleManager() *RoleManager {
	return &RoleManager{
		links: make(map[authz.Principal]map[authz.Principal]struct{}),
	}
}

// AddLink declares that member belongs to group. For v1's static config,
// group membership comes from AuthConfig.Groups. The two built-in groups
// (everyone, authenticated) are computed at evaluation time and do not
// go through RoleManager — they are handled in Enforcer.resolvedGroups.
//
// Idempotent: adding the same link twice is a no-op.
func (rm *RoleManager) AddLink(member, group authz.Principal) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if _, ok := rm.links[group]; !ok {
		rm.links[group] = make(map[authz.Principal]struct{})
	}
	rm.links[group][member] = struct{}{}
}

// DeleteLink removes the membership link between member and group.
// Idempotent: removing a non-existent link is a no-op.
func (rm *RoleManager) DeleteLink(member, group authz.Principal) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	members, ok := rm.links[group]
	if !ok {
		return
	}
	delete(members, member)
	// Clean up empty group maps to avoid memory leaks.
	if len(members) == 0 {
		delete(rm.links, group)
	}
}

// HasLink reports whether member belongs to group. For user-defined groups,
// this checks RoleManager's links. For built-in groups, resolution happens
// at Enforcer evaluation time — this method always returns false for them.
func (rm *RoleManager) HasLink(member, group authz.Principal) bool {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	members, ok := rm.links[group]
	if !ok {
		return false
	}
	_, exists := members[member]
	return exists
}

// GetRoles returns all group principals that member belongs to.
// Returns nil (not empty slice) when the member has no group memberships.
func (rm *RoleManager) GetRoles(member authz.Principal) []authz.Principal {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	var groups []authz.Principal
	for group, members := range rm.links {
		if _, ok := members[member]; ok {
			groups = append(groups, group)
		}
	}
	return groups
}

// GetUsers returns all member principals that belong to the given group.
// Returns nil (not empty slice) when the group has no members.
func (rm *RoleManager) GetUsers(group authz.Principal) []authz.Principal {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	members, ok := rm.links[group]
	if !ok {
		return nil
	}
	users := make([]authz.Principal, 0, len(members))
	for m := range members {
		users = append(users, m)
	}
	return users
}

// Clear removes all group membership links. Used for testing and in future
// reload scenarios.
func (rm *RoleManager) Clear() {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.links = make(map[authz.Principal]map[authz.Principal]struct{})
}
