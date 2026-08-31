package authz_simple

import (
	"testing"

	"seedwright/internal/authz"
)

// --- Role tests ---

func TestRole_IsValid(t *testing.T) {
	valid := []Role{RoleViewer, RoleContributor, RoleMember, RoleAdmin}
	invalid := []Role{"superadmin", "foo", ""} // viewer/contributor/member/admin equal the constants, not invalid

	for _, r := range valid {
		if !r.IsValid() {
			t.Errorf("Role %q.IsValid() = false, want true", r)
		}
	}
	for _, r := range invalid {
		if r.IsValid() {
			t.Errorf("Role %q.IsValid() = true, want false", r)
		}
	}
}

func TestRole_priority(t *testing.T) {
	tests := []struct {
		role Role
		prio int
	}{
		{RoleViewer, 0},
		{RoleContributor, 1},
		{RoleMember, 2},
		{RoleAdmin, 3},
		{Role("unknown"), -1},
		{"", -1},
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			if got := tt.role.priority(); got != tt.prio {
				t.Errorf("priority(%q) = %d, want %d", tt.role, got, tt.prio)
			}
		})
	}
}

func TestMaxRole(t *testing.T) {
	tests := []struct {
		name    string
		roles   []Role
		want    Role
		wantErr bool
	}{
		{"empty", nil, "", false},
		{"single viewer", []Role{RoleViewer}, RoleViewer, false},
		{"admin wins", []Role{RoleViewer, RoleAdmin}, RoleAdmin, false},
		{"member over contributor", []Role{RoleContributor, RoleMember}, RoleMember, false},
		{"all same", []Role{RoleContributor, RoleContributor}, RoleContributor, false},
		{"invalid role", []Role{RoleViewer, Role("foo")}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MaxRole(tt.roles...)
			if (err != nil) != tt.wantErr {
				t.Errorf("MaxRole() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("MaxRole() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- RoleAssignment tests ---

func TestBootstrapDefaultRoleAssignment(t *testing.T) {
	ra := BootstrapDefaultRoleAssignment
	if ra.Principal != "user:root" {
		t.Errorf("BootstrapDefault.Principal = %q, want %q", ra.Principal, "user:root")
	}
	if ra.Scope.Type != authz.ScopeGlobal {
		t.Errorf("BootstrapDefault.Scope.Type = %q, want %q", ra.Scope.Type, authz.ScopeGlobal)
	}
	if ra.Role != RoleAdmin {
		t.Errorf("BootstrapDefault.Role = %q, want %q", ra.Role, RoleAdmin)
	}
}

// --- Action matrix tests ---

func TestRoleActionCovered(t *testing.T) {
	tests := []struct {
		role   Role
		action authz.Action
		want   bool
	}{
		// Viewer (priority 0): can only view
		{RoleViewer, authz.ActionView, true},
		{RoleViewer, authz.ActionGenerate, false},
		{RoleViewer, authz.ActionDeleteElement, false},
		{RoleViewer, authz.ActionCreateProject, false},
		{RoleViewer, authz.ActionDeleteProject, false},
		{RoleViewer, authz.ActionManagePermissions, false},

		// Contributor (priority 1): can view + generate + create project
		{RoleContributor, authz.ActionView, true},
		{RoleContributor, authz.ActionGenerate, true},
		{RoleContributor, authz.ActionDeleteElement, false},
		{RoleContributor, authz.ActionCreateProject, true},
		{RoleContributor, authz.ActionDeleteProject, false},
		{RoleContributor, authz.ActionManagePermissions, false},

		// Member (priority 2): can view + generate + delete elements + manage permissions + create project
		{RoleMember, authz.ActionView, true},
		{RoleMember, authz.ActionGenerate, true},
		{RoleMember, authz.ActionDeleteElement, true},
		{RoleMember, authz.ActionCreateProject, true},
		{RoleMember, authz.ActionDeleteProject, false},
		{RoleMember, authz.ActionManagePermissions, true},

		// Admin (priority 3): can do everything
		{RoleAdmin, authz.ActionView, true},
		{RoleAdmin, authz.ActionGenerate, true},
		{RoleAdmin, authz.ActionDeleteElement, true},
		{RoleAdmin, authz.ActionCreateProject, true},
		{RoleAdmin, authz.ActionDeleteProject, true},
		{RoleAdmin, authz.ActionManagePermissions, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.role)+"_"+string(tt.action), func(t *testing.T) {
			if got := roleActionCovered(tt.role, tt.action); got != tt.want {
				t.Errorf("roleActionCovered(%q, %q) = %v, want %v", tt.role, tt.action, got, tt.want)
			}
		})
	}
}
