package authz

import (
	"context"
)

// OwnershipClaimer is an optional capability an Enforcer implementation may
// satisfy, checked via type assertion — not part of the core Enforcer
// interface, and not a second required method every implementation has to
// carry. The same shape Go's standard library already uses for optional
// capabilities (io.Closer, http.Flusher), and the same shape this project
// already chose for Card/Step: a trait, not a forced struct.
//
// ClaimGlobalOwnership grants the current authenticated principal Global
// Admin (Owner) role — the control-plane equivalent of the bootstrap row.
// It is the one capability that sits outside the data plane's own bookkeeping,
// so it can bootstrap a real principal into that bookkeeping precisely when
// the bookkeeping's own rules would otherwise make that impossible.
//
// Implementations are expected to treat re-claiming an already-held Admin
// role as a harmless success, not an error.
type OwnershipClaimer interface {
	ClaimGlobalOwnership(ctx context.Context, principal Principal) error
}
