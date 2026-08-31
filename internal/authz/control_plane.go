package authz

import (
	"context"
	"net/http"
)

// --- ControlPlaneAuthenticator interface ---

// ControlPlaneAuthenticator decides whether the current request carries
// valid proof of control-plane recovery authority — a credential check,
// not a policy decision, and not routed through Enforcer at all. It does
// not receive a Principal; it doesn't need one, since it's establishing an
// additional fact about the requester, not evaluating what a known identity
// is allowed to do. Exactly one is active per deployment. Core's own
// default denies everything: no built-in bypass, matching StaticEnforcer's
// own fail-closed instinct on the data-plane side.
//
// Authenticate receives the raw *http.Request deliberately — an extension
// implementation is free to read whatever it needs from it (a form field,
// a cookie, a header) without core needing to know or care what shape that
// data takes. This keeps core's own claim-ownership page genuinely generic,
// at the cost of leaving what "authenticated" means entirely up to whichever
// extension is active.
type ControlPlaneAuthenticator interface {
	Authenticate(ctx context.Context, r *http.Request) bool
}

// --- DenyAllAuthenticator ---

// DenyAllAuthenticator is core's only built-in ControlPlaneAuthenticator.
// Nobody can claim ownership via this route until a real extension is
// configured — the same fail-closed reasoning StaticEnforcer already
// applies to the data plane, applied here to control-plane authentication.
type DenyAllAuthenticator struct{}

// Authenticate always returns false — nobody can claim ownership via this
// mechanism until a real extension is configured.
func (DenyAllAuthenticator) Authenticate(_ context.Context, _ *http.Request) bool {
	return false
}

// Compile-time check: DenyAllAuthenticator implements ControlPlaneAuthenticator.
var _ ControlPlaneAuthenticator = (*DenyAllAuthenticator)(nil)
