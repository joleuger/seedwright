package authz

import (
	"testing"
)

// --- Principal tests ---

func TestPrincipal_IsAnonymous(t *testing.T) {
	tests := []struct {
		name     string
		p        Principal
		expected bool
	}{
		{"anonymous", "anonymous", true},
		{"user:root", Principal("user:root"), false},
		{"group:everyone", Principal("group:everyone"), false},
		{"svc:bot", Principal("svc:bot"), false},
		{"empty", Principal(""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.IsAnonymous(); got != tt.expected {
				t.Errorf("IsAnonymous() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestPrincipal_HasPrefix(t *testing.T) {
	tests := []struct {
		name     string
		p        Principal
		expected bool
	}{
		{"user:alice", Principal("user:alice"), true},
		{"group:household", Principal("group:household"), true},
		{"svc:bot", Principal("svc:bot"), true},
		{"anonymous", Principal("anonymous"), false},
		{"empty", Principal(""), false},
		{"no_prefix", Principal("no_prefix"), false},
		{"bad:prefix", Principal("bad:prefix"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.HasPrefix(); got != tt.expected {
				t.Errorf("HasPrefix() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestValidatePrincipal(t *testing.T) {
	tests := []struct {
		name    string
		p       Principal
		wantErr bool
	}{
		{"anonymous", "anonymous", false},
		{"user:alice", Principal("user:alice"), false},
		{"group:household", Principal("group:household"), false},
		{"svc:bot", Principal("svc:bot"), false},
		{"empty name user:", Principal("user:"), true},
		{"empty name group:", Principal("group:"), true},
		{"empty name svc:", Principal("svc:"), true},
		{"unknown prefix", Principal("foo:bar"), true},
		{"empty string", Principal(""), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePrincipal(tt.p)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePrincipal(%q) error = %v, wantErr %v", tt.p, err, tt.wantErr)
			}
		})
	}
}

// --- ScopeRef tests ---

func TestScopeRef_String(t *testing.T) {
	tests := []struct {
		name   string
		ref    ScopeRef
		want   string
	}{
		{"global", ScopeRef{Type: ScopeGlobal}, "global"},
		{"project:test", ScopeRef{Type: ScopeProject, ID: "test"}, "project:test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ref.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseScopeRef(t *testing.T) {
	tests := []struct {
		name    string
		s       string
		want    ScopeRef
		wantErr bool
	}{
		{"global", "global", ScopeRef{Type: ScopeGlobal}, false},
		{"project:test", "project:test", ScopeRef{Type: ScopeProject, ID: "test"}, false},
		{"project:empty-id", "project:", ScopeRef{Type: ScopeProject, ID: ""}, false},
		{"bad", "foo", ScopeRef{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseScopeRef(tt.s)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseScopeRef(%q) error = %v, wantErr %v", tt.s, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseScopeRef(%q) = %+v, want %+v", tt.s, got, tt.want)
			}
		})
	}
}

// --- Built-in group tests ---

func TestIsBuiltInGroup(t *testing.T) {
	tests := []struct {
		name     string
		p        Principal
		expected bool
	}{
		{"everyone", Principal("group:everyone"), true},
		{"authenticated", Principal("group:authenticated"), true},
		{"household", Principal("group:household"), false},
		{"user:root", Principal("user:root"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsBuiltInGroup(tt.p); got != tt.expected {
				t.Errorf("IsBuiltInGroup(%q) = %v, want %v", tt.p, got, tt.expected)
			}
		})
	}
}

func TestResolveBuiltInGroups(t *testing.T) {
	got := ResolveBuiltInGroups()
	expected := [2]Principal{"group:everyone", "group:authenticated"}
	if got != expected {
		t.Errorf("ResolveBuiltInGroups() = %+v, want %+v", got, expected)
	}
}

// --- Action tests ---

func TestActions(t *testing.T) {
	expectedActions := []Action{
		ActionView, ActionGenerate, ActionDeleteElement,
		ActionManagePermissions, ActionDeleteProject, ActionCreateProject,
	}
	for _, a := range expectedActions {
		if string(a) == "" {
			t.Errorf("Action %q is empty", a)
		}
	}
}
