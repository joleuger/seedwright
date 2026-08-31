package authz_simple

import (
	"context"
	"net/http"
	"testing"

	"seedwright/internal/authz"
	"gopkg.in/yaml.v3"
)

// --- ConfigAdapter tests ---

func TestNewConfigAdapter(t *testing.T) {
	cfg := &Config{
		Groups: map[authz.Principal][]authz.Principal{
			"group:household": {"user:alice", "user:bob"},
		},
		RawRoleAssignments: []rawRoleAssignment{
			{Principal: "user:root", Scope: "global", Role: "admin"},
			{Principal: "group:household", Scope: "project:test", Role: "contributor"},
		},
	}

	adapter, err := NewConfigAdapter(cfg)
	if err != nil {
		t.Fatalf("NewConfigAdapter() error = %v", err)
	}

	assignments, groups, err := adapter.LoadPolicy()
	if err != nil {
		t.Fatalf("LoadPolicy() error = %v", err)
	}

	if len(assignments) != 2 {
		t.Errorf("LoadPolicy assignments = %d, want 2", len(assignments))
	}
	if assignments[0].Principal != "user:root" {
		t.Errorf("assignments[0].Principal = %q, want %q", assignments[0].Principal, "user:root")
	}
	if len(groups["group:household"]) != 2 {
		t.Errorf("groups[household] = %d, want 2", len(groups["group:household"]))
	}
}

func TestNewConfigAdapter_InvalidScope(t *testing.T) {
	cfg := &Config{
		RawRoleAssignments: []rawRoleAssignment{
			{Principal: "user:root", Scope: "not_a_scope", Role: "admin"},
		},
	}

	_, err := NewConfigAdapter(cfg)
	if err == nil {
		t.Error("NewConfigAdapter() with invalid scope = nil, want error")
	}
}

func TestParseConfig_Groups(t *testing.T) {
	yamlStr := `
groups:
  household:
    members: [user:alice, user:bob]
  guest_reviewers:
    members: [anonymous, svc:bot]
`
	node := yamlNodeFromString(t, yamlStr)
	cfg, err := ParseConfig(*node)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}

	household, ok := cfg.Groups["group:household"]
	if !ok {
		t.Fatal("group:household not found")
	}
	if len(household) != 2 {
		t.Errorf("household has %d members, want 2", len(household))
	}

	guest, ok := cfg.Groups["group:guest_reviewers"]
	if !ok {
		t.Fatal("group:guest_reviewers not found")
	}
	if len(guest) != 2 {
		t.Errorf("guest_reviewers has %d members, want 2", len(guest))
	}
	if guest[0] != "anonymous" {
		t.Errorf("guest_reviewers[0] = %q, want \"anonymous\"", guest[0])
	}
}

func TestParseConfig_ServicePrincipals(t *testing.T) {
	yamlStr := `
service_principals:
  mcp-agent:
    token_env: MCP_AGENT_TOKEN
`
	node := yamlNodeFromString(t, yamlStr)
	cfg, err := ParseConfig(*node)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}

	sp, ok := cfg.ServicePrincipals["svc:mcp-agent"]
	if !ok {
		t.Fatal("svc:mcp-agent not found")
	}
	if sp.TokenEnv != "MCP_AGENT_TOKEN" {
		t.Errorf("TokenEnv = %q, want \"MCP_AGENT_TOKEN\"", sp.TokenEnv)
	}
}

func TestParseConfig_RoleAssignments(t *testing.T) {
	yamlStr := `
role_assignments:
  - principal: user:root
    scope: global
    role: admin
  - principal: group:everyone
    scope: project:birthday-2026
    role: viewer
`
	node := yamlNodeFromString(t, yamlStr)
	cfg, err := ParseConfig(*node)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	if len(cfg.RawRoleAssignments) != 2 {
		t.Fatalf("RawRoleAssignments has %d entries, want 2", len(cfg.RawRoleAssignments))
	}
	if cfg.RawRoleAssignments[0].Principal != "user:root" {
		t.Errorf("RA[0].Principal = %q, want \"user:root\"", cfg.RawRoleAssignments[0].Principal)
	}
}

// --- SimpleEnforcer tests ---

func TestNewSimpleEnforcer_BootstrapDefault(t *testing.T) {
	// Config with NO role assignments — bootstrap row should be added.
	cfg := &Config{}
	adapter, err := NewConfigAdapter(cfg)
	if err != nil {
		t.Fatalf("NewConfigAdapter() error = %v", err)
	}

	enforcer, err := NewSimpleEnforcer(adapter, &StaticResolver{Principal: "user:root"})
	if err != nil {
		t.Fatalf("NewSimpleEnforcer() error = %v", err)
	}

	assignments := enforcer.SortedAssignments()
	hasRootGlobal := false
	for _, ra := range assignments {
		if ra.Principal == "user:root" && ra.Scope.Type == authz.ScopeGlobal && ra.Role == RoleAdmin {
			hasRootGlobal = true
			break
		}
	}
	if !hasRootGlobal {
		t.Error("Bootstrap default (user:root global admin) not found in assignments")
	}
}

func TestNewSimpleEnforcer_UserAssignments(t *testing.T) {
	cfg := &Config{
		RawRoleAssignments: []rawRoleAssignment{
			{Principal: "user:root", Scope: "global", Role: "admin"},
			{Principal: "user:bob", Scope: "project:test", Role: "viewer"},
		},
	}
	adapter, err := NewConfigAdapter(cfg)
	if err != nil {
		t.Fatalf("NewConfigAdapter() error = %v", err)
	}

	enforcer, err := NewSimpleEnforcer(adapter, &StaticResolver{Principal: "user:root"})
	if err != nil {
		t.Fatalf("NewSimpleEnforcer() error = %v", err)
	}

	// Bob should have viewer role on project:test.
	ctx := context.Background()
	if !enforcer.Enforce(ctx, "user:bob", authz.ActionView, authz.ScopeRef{Type: authz.ScopeProject, ID: "test"}) {
		t.Error("Enforce(user:bob, view, project:test) = false, want true")
	}
}

// --- Enforce tests ---

func TestEnforce_RootAdminGlobal(t *testing.T) {
	config := &Config{
		RawRoleAssignments: []rawRoleAssignment{
			{Principal: "user:root", Scope: "global", Role: "admin"},
		},
	}
	adapter, _ := NewConfigAdapter(config)
	enforcer, _ := NewSimpleEnforcer(adapter, &StaticResolver{Principal: "user:root"})

	// Root should have all actions at global scope.
	global := authz.ScopeRef{Type: authz.ScopeGlobal}
	for _, action := range []authz.Action{
		authz.ActionView, authz.ActionGenerate, authz.ActionDeleteElement,
		authz.ActionManagePermissions, authz.ActionDeleteProject, authz.ActionCreateProject,
	} {
		if !enforcer.Enforce(context.Background(), "user:root", action, global) {
			t.Errorf("Enforce(user:root, %q, global) = false, want true", action)
		}
	}
}

func TestEnforce_UserNoAssignments(t *testing.T) {
	config := &Config{
		RawRoleAssignments: []rawRoleAssignment{
			{Principal: "user:root", Scope: "global", Role: "admin"},
		},
	}
	adapter, _ := NewConfigAdapter(config)
	enforcer, _ := NewSimpleEnforcer(adapter, &StaticResolver{Principal: "user:bob"})

	// Bob has no assignments — should be denied everything.
	for _, action := range []authz.Action{
		authz.ActionView, authz.ActionGenerate, authz.ActionDeleteElement,
	} {
		if enforcer.Enforce(context.Background(), "user:bob", action, authz.ScopeRef{Type: authz.ScopeGlobal}) {
			t.Errorf("Enforce(user:bob, %q, global) = true, want false", action)
		}
	}
}

func TestEnforce_ProjectScoping(t *testing.T) {
	config := &Config{
		RawRoleAssignments: []rawRoleAssignment{
			{Principal: "user:bob", Scope: "project:test", Role: "contributor"},
		},
	}
	adapter, _ := NewConfigAdapter(config)
	enforcer, _ := NewSimpleEnforcer(adapter, &StaticResolver{Principal: "user:bob"})

	// Bob is contributor on project:test — can view and generate.
	testScope := authz.ScopeRef{Type: authz.ScopeProject, ID: "test"}
	if !enforcer.Enforce(context.Background(), "user:bob", authz.ActionView, testScope) {
		t.Error("Enforce(user:bob, view, project:test) = false, want true")
	}
	if !enforcer.Enforce(context.Background(), "user:bob", authz.ActionGenerate, testScope) {
		t.Error("Enforce(user:bob, generate, project:test) = false, want true")
	}
	if enforcer.Enforce(context.Background(), "user:bob", authz.ActionDeleteElement, testScope) {
		t.Error("Enforce(user:bob, delete_element, project:test) = true, want false (contributor)")
	}

	// Bob has no access to project:other.
	otherScope := authz.ScopeRef{Type: authz.ScopeProject, ID: "other"}
	if enforcer.Enforce(context.Background(), "user:bob", authz.ActionView, otherScope) {
		t.Error("Enforce(user:bob, view, project:other) = true, want false")
	}

	// Bob should also have global scope checked — but he has no global assignment.
	globalScope := authz.ScopeRef{Type: authz.ScopeGlobal}
	if enforcer.Enforce(context.Background(), "user:bob", authz.ActionView, globalScope) {
		t.Error("Enforce(user:bob, view, global) = true, want false")
	}
}

func TestEnforce_GroupMembership(t *testing.T) {
	config := &Config{
		Groups: map[authz.Principal][]authz.Principal{
			"group:household": {"user:alice", "user:bob"},
		},
		RawRoleAssignments: []rawRoleAssignment{
			{Principal: "group:household", Scope: "project:test", Role: "contributor"},
		},
	}
	adapter, _ := NewConfigAdapter(config)
	enforcer, _ := NewSimpleEnforcer(adapter, &StaticResolver{Principal: "user:alice"})

	// Alice is in group:household — should get contributor on project:test.
	testScope := authz.ScopeRef{Type: authz.ScopeProject, ID: "test"}
	if !enforcer.Enforce(context.Background(), "user:alice", authz.ActionGenerate, testScope) {
		t.Error("Enforce(user:alice, generate, project:test) via group = false, want true")
	}
}

func TestEnforce_BuiltInEveryone(t *testing.T) {
	config := &Config{
		RawRoleAssignments: []rawRoleAssignment{
			{Principal: "group:everyone", Scope: "project:public", Role: "viewer"},
		},
	}
	adapter, _ := NewConfigAdapter(config)
	// Resolve as anonymous — should match group:everyone.
	enforcer, _ := NewSimpleEnforcer(adapter, &AnonymousResolver{})

	publicScope := authz.ScopeRef{Type: authz.ScopeProject, ID: "public"}
	if !enforcer.Enforce(context.Background(), "anonymous", authz.ActionView, publicScope) {
		t.Error("Enforce(anon, view, project:public via group:everyone) = false, want true")
	}

	// Anonymous should NOT be able to generate.
	if enforcer.Enforce(context.Background(), "anonymous", authz.ActionGenerate, publicScope) {
		t.Error("Enforce(anon, generate, project:public) = true, want false")
	}
}

func TestEnforce_BuiltInAuthenticated(t *testing.T) {
	config := &Config{
		RawRoleAssignments: []rawRoleAssignment{
			{Principal: "group:authenticated", Scope: "project:members-only", Role: "viewer"},
		},
	}
	adapter, _ := NewConfigAdapter(config)
	// Resolve as authenticated user.
	enforcer, _ := NewSimpleEnforcer(adapter, &StaticResolver{Principal: "user:alice"})

	membersScope := authz.ScopeRef{Type: authz.ScopeProject, ID: "members-only"}
	if !enforcer.Enforce(context.Background(), "user:alice", authz.ActionView, membersScope) {
		t.Error("Enforce(user:alice, view, project:members-only via group:authenticated) = false, want true")
	}
}

func TestEnforce_GlobalFallback(t *testing.T) {
	config := &Config{
		RawRoleAssignments: []rawRoleAssignment{
			{Principal: "user:bob", Scope: "project:test", Role: "viewer"},
			{Principal: "user:bob", Scope: "global", Role: "contributor"},
		},
	}
	adapter, _ := NewConfigAdapter(config)
	enforcer, _ := NewSimpleEnforcer(adapter, &StaticResolver{Principal: "user:bob"})

	// Bob has viewer on project:test but contributor globally.
	// For project:test actions, check both project:test AND global.
	testScope := authz.ScopeRef{Type: authz.ScopeProject, ID: "test"}

	// At project:test: viewer (from project:test) + contributor (from global) = max contributor.
	// Contributor can generate.
	if !enforcer.Enforce(context.Background(), "user:bob", authz.ActionGenerate, testScope) {
		t.Error("Enforce(user:bob, generate, project:test) with global contributor fallback = false, want true")
	}

	// At project:other: only global contributor applies.
	otherScope := authz.ScopeRef{Type: authz.ScopeProject, ID: "other"}
	if !enforcer.Enforce(context.Background(), "user:bob", authz.ActionGenerate, otherScope) {
		t.Error("Enforce(user:bob, generate, project:other) with global contributor = false, want true")
	}
}

func TestEnforce_AdditiveRoles(t *testing.T) {
	// Alice is contributor on project:test and member on project:other.
	// Effective role should be the max across all applicable assignments.
	config := &Config{
		RawRoleAssignments: []rawRoleAssignment{
			{Principal: "user:alice", Scope: "project:test", Role: "contributor"},
			{Principal: "user:alice", Scope: "project:other", Role: "member"},
		},
	}
	adapter, _ := NewConfigAdapter(config)
	enforcer, _ := NewSimpleEnforcer(adapter, &StaticResolver{Principal: "user:alice"})

	testScope := authz.ScopeRef{Type: authz.ScopeProject, ID: "test"}
	if !enforcer.Enforce(context.Background(), "user:alice", authz.ActionGenerate, testScope) {
		t.Error("Enforce(user:alice, generate, project:test) = false, want true (contributor)")
	}
	if enforcer.Enforce(context.Background(), "user:alice", authz.ActionDeleteElement, testScope) {
		t.Error("Enforce(user:alice, delete_element, project:test) = true, want false (contributor)")
	}

	otherScope := authz.ScopeRef{Type: authz.ScopeProject, ID: "other"}
	if !enforcer.Enforce(context.Background(), "user:alice", authz.ActionDeleteElement, otherScope) {
		t.Error("Enforce(user:alice, delete_element, project:other) = false, want true (member)")
	}
}

// --- SortedAssignments tests ---

func TestSortedAssignments(t *testing.T) {
	config := &Config{
		RawRoleAssignments: []rawRoleAssignment{
			{Principal: "user:root", Scope: "global", Role: "admin"},
		},
	}
	adapter, _ := NewConfigAdapter(config)
	enforcer, _ := NewSimpleEnforcer(adapter, &StaticResolver{Principal: "user:root"})

	assignments := enforcer.SortedAssignments()
	if len(assignments) != 1 {
		t.Errorf("SortedAssignments() = %d, want 1", len(assignments))
	}
	if assignments[0].Principal != "user:root" {
		t.Errorf("SortedAssignments()[0].Principal = %q, want %q", assignments[0].Principal, "user:root")
	}
}

func TestSortedAssignments_IsCopy(t *testing.T) {
	config := &Config{
		RawRoleAssignments: []rawRoleAssignment{
			{Principal: "user:root", Scope: "global", Role: "admin"},
		},
	}
	adapter, _ := NewConfigAdapter(config)
	enforcer, _ := NewSimpleEnforcer(adapter, &StaticResolver{Principal: "user:root"})

	assignments := enforcer.SortedAssignments()
	// Modify the returned slice — should not affect internal state.
	assignments[0].Principal = "user:modified"

	assignments2 := enforcer.SortedAssignments()
	if assignments2[0].Principal != "user:root" {
		t.Error("SortedAssignments is not a copy — internal state was modified")
	}
}

// --- StaticResolver tests ---

type StaticResolver struct {
	Principal authz.Principal
}

func (s *StaticResolver) Resolve(r *http.Request) authz.Principal {
	return s.Principal
}

// AnonymousResolver is an IdentityResolver that always returns "anonymous".
type AnonymousResolver struct{}

func (a *AnonymousResolver) Resolve(r *http.Request) authz.Principal {
	return "anonymous"
}

// yamlNodeFromString parses a YAML string into a yaml.Node.
func yamlNodeFromString(t *testing.T, s string) *yaml.Node {
	t.Helper()
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(s), &node); err != nil {
		t.Fatalf("yaml.Unmarshal(%q) error = %v", s, err)
	}
	return &node
}
