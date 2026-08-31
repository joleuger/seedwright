package authz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- StaticResolver tests ---

func TestStaticResolver(t *testing.T) {
	resolver := StaticResolver{Principal: "user:alice"}
	req := &http.Request{}
	if got := resolver.Resolve(req); got != "user:alice" {
		t.Errorf("Resolve() = %q, want \"user:alice\"", got)
	}
}

// --- StaticEnforcer tests ---

func TestStaticEnforcer_AllowsConfiguredPrincipal(t *testing.T) {
	e := &StaticEnforcer{Principal: "user:root"}
	ctx := context.Background()

	for _, action := range []Action{ActionView, ActionGenerate, ActionDeleteElement, ActionManagePermissions, ActionDeleteProject, ActionCreateProject} {
		for _, scope := range []ScopeRef{{Type: ScopeGlobal}, {Type: ScopeProject, ID: "any"}} {
			if !e.Enforce(ctx, "user:root", action, scope) {
				t.Errorf("Enforce(user:root, %q, %v) = false, want true", action, scope)
			}
		}
	}
}

func TestStaticEnforcer_DeniesOtherPrincipal(t *testing.T) {
	e := &StaticEnforcer{Principal: "user:root"}
	ctx := context.Background()

	for _, action := range []Action{ActionView, ActionGenerate, ActionDeleteElement, ActionManagePermissions, ActionDeleteProject, ActionCreateProject} {
		for _, scope := range []ScopeRef{{Type: ScopeGlobal}, {Type: ScopeProject, ID: "any"}} {
			if e.Enforce(ctx, "user:bob", action, scope) {
				t.Errorf("Enforce(user:bob, %q, %v) = true, want false", action, scope)
			}
		}
	}
}

func TestStaticEnforcer_DifferentPrincipal(t *testing.T) {
	e := &StaticEnforcer{Principal: "user:alice"}
	ctx := context.Background()

	if !e.Enforce(ctx, "user:alice", ActionView, ScopeRef{Type: ScopeGlobal}) {
		t.Error("Enforce(user:alice, view, global) = false, want true when alice is the configured principal")
	}
	if e.Enforce(ctx, "user:root", ActionView, ScopeRef{Type: ScopeGlobal}) {
		t.Error("Enforce(user:root, view, global) = true, want false when root is not the configured principal")
	}
}

// --- StaticEnforcer end-to-end HTTP middleware tests ---

func newTestRouter(resolver IdentityResolver, enforcer Enforcer) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/project/delete", RequireAction(ActionDeleteProject, enforcer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	return mux
}

// TestRequireAction_EndToEnd proves RequireAction actually calls Enforce,
// and that the result actually governs the response. No extension compiled
// in, no mocks: StaticResolver + StaticEnforcer are core, so this test is too.
func TestRequireAction_EndToEnd(t *testing.T) {
	tests := []struct {
		name       string
		resolvedAs Principal
		wantStatus int
	}{
		{"configured admin principal is granted", "user:root", http.StatusOK},
		{"any other principal is denied", "user:someone-else", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := StaticResolver{Principal: tt.resolvedAs}
			enforcer := &StaticEnforcer{Principal: "user:root"}

			router := newTestRouter(resolver, enforcer)

			req := httptest.NewRequest("POST", "/project/delete", nil)
			req.Header.Set("X-Principal", string(tt.resolvedAs))

			// Verify the enforcer itself denies first, then check the
			// HTTP response for the configured-principal case.
			proj := ScopeRef{Type: ScopeProject, ID: "birthday-2026"}
			if tt.resolvedAs != "user:root" {
				if enforcer.Enforce(req.Context(), tt.resolvedAs, ActionDeleteProject, proj) {
					t.Fatalf("Enforcer itself says allow — test setup is wrong")
				}
			}

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("resolved as %s: got %d, want %d", tt.resolvedAs, rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestPublic(t *testing.T) {
	var called bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	wrapped := Public()(handler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if !called {
		t.Error("Public() middleware did not pass through to handler")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("Public() status = %d, want %d", rr.Code, http.StatusOK)
	}
}

// --- Bootstrap invariant tests ---

func TestVerifyBootstrapInvariant_StaticEnforcer(t *testing.T) {
	e := &StaticEnforcer{Principal: "user:root"}
	ctx := context.Background()

	if err := VerifyBootstrapInvariant(ctx, e); err != nil {
		t.Errorf("VerifyBootstrapInvariant(StaticEnforcer{user:root}) = %v, want nil", err)
	}
}

func TestVerifyBootstrapInvariant_DifferentPrincipalFails(t *testing.T) {
	e := &StaticEnforcer{Principal: "user:alice"}
	ctx := context.Background()

	if err := VerifyBootstrapInvariant(ctx, e); err == nil {
		t.Error("VerifyBootstrapInvariant(&StaticEnforcer{user:alice}) = nil, want error")
	} else {
		t.Logf("Expected failure: %v", err)
	}
}

// --- BuildEnforcer tests ---

func TestBuildEnforcer_WithEngine(t *testing.T) {
	resolver := StaticResolver{Principal: "user:root"}
	ctx := context.Background()

	// Test with "static" engine.
	e, err := BuildEnforcer(ctx, resolver, "static")
	if err != nil {
		t.Fatalf("BuildEnforcer(static) error = %v", err)
	}
	if e == nil {
		t.Fatal("BuildEnforcer(static) returned nil")
	}

	// Verify it works: root should be granted.
	if !e.Enforce(ctx, "user:root", ActionView, ScopeRef{Type: ScopeGlobal}) {
		t.Error("Enforce(root, view, global) = false, want true after BuildEnforcer")
	}
	if e.Enforce(ctx, "user:bob", ActionView, ScopeRef{Type: ScopeGlobal}) {
		t.Error("Enforce(bob, view, global) = true, want false after BuildEnforcer")
	}
}

func TestBuildEnforcer_UnknownEngine(t *testing.T) {
	resolver := StaticResolver{Principal: "user:root"}
	ctx := context.Background()

	_, err := BuildEnforcer(ctx, resolver, "ext/joleuger/authz-simple")
	if err == nil {
		t.Fatal("BuildEnforcer(unknown) = nil, want error")
	}
	if err != ErrUnknownEngine {
		t.Errorf("BuildEnforcer(unknown) error = %v, want %v", err, ErrUnknownEngine)
	}
}

func TestBuildEnforcer_NilResolver(t *testing.T) {
	ctx := context.Background()

	// Should not panic with nil resolver; falls back to "user:root".
	e, err := BuildEnforcer(ctx, nil, "static")
	if err != nil {
		t.Fatalf("BuildEnforcer(nil resolver) error = %v", err)
	}
	if e == nil {
		t.Fatal("BuildEnforcer(nil resolver) returned nil")
	}
}

func TestBuildEnforcerWithConfig(t *testing.T) {
	ctx := context.Background()

	// Test with AuthConfig specifying "static" engine.
	cfg := &AuthConfig{
		Mechanism: "static",
		Engine:    "static",
		Principal: "user:root",
	}
	resolver := StaticResolver{Principal: "user:root"}

	e, err := BuildEnforcerWithConfig(ctx, cfg, resolver, "")
	if err != nil {
		t.Fatalf("BuildEnforcerWithConfig error = %v", err)
	}
	if e == nil {
		t.Fatal("BuildEnforcerWithConfig returned nil")
	}
	if !e.Enforce(ctx, "user:root", ActionView, ScopeRef{Type: ScopeGlobal}) {
		t.Error("Enforce(root, view, global) = false, want true")
	}
}

// --- Compile-time interface checks ---

func TestCompileTime(t *testing.T) {
	var _ Enforcer = (*StaticEnforcer)(nil)
	var _ OwnershipClaimer = (*StaticEnforcer)(nil)
	var _ ControlPlaneAuthenticator = (*DenyAllAuthenticator)(nil)
	var _ IdentityResolver = StaticResolver{}
}
