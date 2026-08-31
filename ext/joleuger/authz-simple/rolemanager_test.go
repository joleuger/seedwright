package authz_simple

import (
	"testing"

	"seedwright/internal/authz"
)

// --- RoleManager tests ---

func TestRoleManager_AddLink_HasLink(t *testing.T) {
	rm := NewRoleManager()
	rm.AddLink("user:alice", "group:household")

	if !rm.HasLink("user:alice", "group:household") {
		t.Error("HasLink(user:alice, group:household) = false, want true")
	}
	if rm.HasLink("user:bob", "group:household") {
		t.Error("HasLink(user:bob, group:household) = true, want false")
	}
}

func TestRoleManager_DefaultEmpty(t *testing.T) {
	rm := NewRoleManager()
	if rm.HasLink("user:alice", "group:household") {
		t.Error("HasLink on unregistered link = true, want false")
	}
}

func TestRoleManager_DeleteLink(t *testing.T) {
	rm := NewRoleManager()
	rm.AddLink("user:alice", "group:household")

	rm.DeleteLink("user:alice", "group:household")
	if rm.HasLink("user:alice", "group:household") {
		t.Error("HasLink after DeleteLink = true, want false")
	}

	// Idempotent: deleting again is a no-op.
	rm.DeleteLink("user:alice", "group:household")
	if rm.HasLink("user:alice", "group:household") {
		t.Error("HasLink after double DeleteLink = true, want false")
	}
}

func TestRoleManager_DeleteLinkNonExistent(t *testing.T) {
	rm := NewRoleManager()

	// Deleting from non-existent group is a no-op.
	rm.DeleteLink("user:alice", "group:nonexistent")

	// Deleting from existing group but non-member is a no-op.
	rm.AddLink("user:bob", "group:household")
	rm.DeleteLink("user:alice", "group:household")
	if !rm.HasLink("user:bob", "group:household") {
		t.Error("HasLink(user:bob) after deleting user:alice = false, want true (bob should still be there)")
	}
}

func TestRoleManager_GetRoles(t *testing.T) {
	rm := NewRoleManager()
	rm.AddLink("user:alice", "group:household")
	rm.AddLink("user:alice", "group:admins")

	roles := rm.GetRoles("user:alice")
	if len(roles) != 2 {
		t.Errorf("GetRoles(user:alice) = %d groups, want 2", len(roles))
	}

	// Verify both groups are present.
	foundHousehold, foundAdmins := false, false
	for _, g := range roles {
		if g == "group:household" {
			foundHousehold = true
		}
		if g == "group:admins" {
			foundAdmins = true
		}
	}
	if !foundHousehold {
		t.Error("group:household not found in GetRoles(user:alice)")
	}
	if !foundAdmins {
		t.Error("group:admins not found in GetRoles(user:alice)")
	}
}

func TestRoleManager_GetRoles_NoMembers(t *testing.T) {
	rm := NewRoleManager()
	roles := rm.GetRoles("user:noone")
	if roles != nil {
		t.Errorf("GetRoles(user:noone) = %v, want nil", roles)
	}
}

func TestRoleManager_GetUsers(t *testing.T) {
	rm := NewRoleManager()
	rm.AddLink("user:alice", "group:household")
	rm.AddLink("user:bob", "group:household")

	users := rm.GetUsers("group:household")
	if len(users) != 2 {
		t.Errorf("GetUsers(group:household) = %d users, want 2", len(users))
	}

	foundAlice, foundBob := false, false
	for _, u := range users {
		if u == "user:alice" {
			foundAlice = true
		}
		if u == "user:bob" {
			foundBob = true
		}
	}
	if !foundAlice {
		t.Error("user:alice not found in GetUsers(group:household)")
	}
	if !foundBob {
		t.Error("user:bob not found in GetUsers(group:household)")
	}
}

func TestRoleManager_GetUsers_NoMembers(t *testing.T) {
	rm := NewRoleManager()
	users := rm.GetUsers("group:nonexistent")
	if users != nil {
		t.Errorf("GetUsers(group:nonexistent) = %v, want nil", users)
	}
}

func TestRoleManager_Clear(t *testing.T) {
	rm := NewRoleManager()
	rm.AddLink("user:alice", "group:household")
	rm.AddLink("user:bob", "group:household")

	rm.Clear()

	if rm.HasLink("user:alice", "group:household") {
		t.Error("HasLink after Clear = true, want false")
	}
	if rm.HasLink("user:bob", "group:household") {
		t.Error("HasLink(user:bob) after Clear = true, want false")
	}
	if rm.GetRoles("user:alice") != nil {
		t.Error("GetRoles after Clear != nil, want nil")
	}
}

func TestRoleManager_IdempotentAddLink(t *testing.T) {
	rm := NewRoleManager()
	rm.AddLink("user:alice", "group:household")
	rm.AddLink("user:alice", "group:household") // second add is a no-op

	if !rm.HasLink("user:alice", "group:household") {
		t.Error("HasLink after idempotent AddLink = false, want true")
	}
	if len(rm.GetUsers("group:household")) != 1 {
		t.Errorf("GetUsers after idempotent AddLink = %d, want 1", len(rm.GetUsers("group:household")))
	}
}

func TestRoleManager_MultipleMembersSameGroup(t *testing.T) {
	rm := NewRoleManager()
	rm.AddLink("user:alice", "group:household")
	rm.AddLink("user:bob", "group:household")
	rm.AddLink("user:charlie", "group:household")

	if len(rm.GetUsers("group:household")) != 3 {
		t.Errorf("GetUsers(group:household) = %d, want 3", len(rm.GetUsers("group:household")))
	}

	for _, member := range []authz.Principal{"user:alice", "user:bob", "user:charlie"} {
		if !rm.HasLink(member, "group:household") {
			t.Errorf("HasLink(%s, group:household) = false, want true", member)
		}
	}
}

func TestRoleManager_MultipleGroupsPerMember(t *testing.T) {
	rm := NewRoleManager()
	rm.AddLink("user:alice", "group:household")
	rm.AddLink("user:alice", "group:admins")
	rm.AddLink("user:alice", "group:reviewers")

	roles := rm.GetRoles("user:alice")
	if len(roles) != 3 {
		t.Errorf("GetRoles(user:alice) = %d, want 3", len(roles))
	}

	// Verify each group membership.
	for _, group := range []authz.Principal{"group:household", "group:admins", "group:reviewers"} {
		if !rm.HasLink("user:alice", group) {
			t.Errorf("HasLink(user:alice, %s) = false, want true", group)
		}
	}
}
