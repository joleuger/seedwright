package authz

import (
	"context"
	"fmt"
	"net/http"
)

// --- Enforcer interface ---

// Enforcer is the one substitutable decision engine — the only swap boundary
// in this package. Core ships StaticEnforcer as the default (single-principal
// allow/deny). The full RBAC engine lives in ext/joleuger/authz-simple.
//
// The signature is deliberately narrow — three fixed types in, bool out — for
// a reason that already has precedent in this project. A genuinely
// attribute-based engine doesn't fit this signature without a redesign.
type Enforcer interface {
	Enforce(ctx context.Context, principal Principal, action Action, scope ScopeRef) bool
}

// --- RequireAction — route-level middleware ---

// RequireAction returns an http.Handler middleware that checks the configured
// Action against the resolved principal before dispatching to the handler.
// When e is nil, all requests are allowed (auth effectively disabled).
func RequireAction(action Action, e Enforcer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Nil enforcer means auth is disabled — allow everything.
			if e == nil {
				next.ServeHTTP(w, r)
				return
			}

			principal := resolvePrincipal(r)
			project := r.PathValue("project")

			var scope ScopeRef
			if project == "" {
				scope = ScopeRef{Type: ScopeGlobal}
			} else {
				scope = ScopeRef{Type: ScopeProject, ID: project}
			}

			if !e.Enforce(r.Context(), principal, action, scope) {
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprintf(w, "Forbidden: principal %q lacks %q at scope %q", principal, action, scope)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// --- Public ---

// Public returns an http.Handler middleware that bypasses all authorization.
// Used for routes that have nothing to check (e.g., photobooth index).
func Public() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return next
	}
}
