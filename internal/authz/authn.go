package authz

import (
	"net/http"
)

// --- IdentityResolver interface ---

// IdentityResolver resolves an incoming HTTP request to a Principal. Exactly
// one IdentityResolver is active per deployment, selected by auth.mechanism
// in config.yaml — never stacked, never chained, never tried in fallback
// order.
type IdentityResolver interface {
	Resolve(r *http.Request) Principal
}

// --- StaticResolver ---

// StaticResolver is core's only built-in IdentityResolver. Every request
// resolves to the same configured Principal, regardless of anything in the
// request — no header parsing, no token verification, nothing to get
// wrong.
type StaticResolver struct {
	Principal Principal
}

// Resolve returns the configured principal for every request.
func (s StaticResolver) Resolve(r *http.Request) Principal {
	return s.Principal
}

// Compile-time check: StaticResolver implements IdentityResolver.
var _ IdentityResolver = (*StaticResolver)(nil)

// --- Principal resolution from request ---

// resolvePrincipal extracts a Principal from the request. It reads the
// "X-Principal" header (set by the auth layer during app bootstrap),
// falling back to "user:root" when no auth is configured.
func resolvePrincipal(r *http.Request) Principal {
	p := r.Header.Get("X-Principal")
	if p == "" {
		return "user:root"
	}
	return Principal(p)
}
