package server

import (
	"html/template"
	"log/slog"
	"net/http"
	"reflect"
	"strings"

	"seedwright/internal/authz"
)

// claimOwnership handles the control-plane claim-ownership page.
//
// GET /claim-ownership — resolve the current principal via IdentityResolver,
// show a "Claim Global Ownership" button, and explain what happens.
//
// POST /claim-ownership — call ControlPlaneAuthenticator.Authenticate(ctx, r) on
// this request. If true and the enforcer implements OwnershipClaimer, call
// ClaimGlobalOwnership(ctx, principal) and log the outcome.
//
// If the active enforcer doesn't implement OwnershipClaimer, show a clear
// message telling the user to edit auth.principal in config.yaml and restart.
//
// This page never needs to know how authentication happened — if the active
// extension's own flow requires visiting a separate page first, that flow's
// job is to leave some artifact (a short-lived signed cookie is the natural
// choice) that Authenticate() reads when core's page calls it.
//
// Core's default DenyAllAuthenticator denies everything until a real extension
// is configured — the same fail-closed reasoning StaticEnforcer applies to
// the data plane, applied here to the control plane.
func (h *handler) claimOwnership(w http.ResponseWriter, r *http.Request) {
	principal := h.cfg.IdentityResolver.Resolve(r)
	if principal == "" {
		h.renderError(w, r, "unable to resolve current identity — check your auth.mechanism configuration")
		return
	}

	owner, ok := h.cfg.Authz.(authz.OwnershipClaimer)
	canClaim := ok

	var message template.HTML
	var messageClass string
	if r.Method == http.MethodPost {
		// Call the active ControlPlaneAuthenticator on this request.
		// Nil guard — defaults to DenyAllAuthenticator which blocks everything.
		authenticator := h.cfg.ControlPlaneAuthenticator
		if authenticator == nil {
			authenticator = authz.DenyAllAuthenticator{}
		}
		authenticated := authenticator.Authenticate(r.Context(), r)
		if authenticated {
			err := owner.ClaimGlobalOwnership(r.Context(), principal)
			if err != nil {
				slog.Warn("claim global ownership failed", "principal", principal, "error", err)
				messageClass = "error"
				message = template.HTML("Failed to claim ownership: " + err.Error())
			} else {
				slog.Info("ownership claimed",
					"principal", principal,
					"authenticator", authenticatorType(authenticator))
				messageClass = ""
				message = template.HTML("Ownership claimed. You now hold Global Admin (Owner).")
				canClaim = false // already claimed
			}
		} else {
			messageClass = ""
			message = template.HTML("Authentication check failed. Please ensure you have control-plane access to claim ownership.")
		}
	}

	// Build a safe redirect URL that preserves the path prefix.
	redirectURL := "/"
	if h.cfg.PathPrefix != "" {
		redirectURL = h.cfg.PathPrefix + "/"
	}

	h.render(w, "claim_ownership", map[string]any{
		"Title":            h.cfg.Title,
		"Page":             "claim_ownership",
		"Principal":        principal,
		"CanClaim":         canClaim,
		"Message":          message,
		"MessageClass":     messageClass,
		"NoClaimSupport":   !ok && r.Method == http.MethodGet,
		"RedirectURL":      redirectURL,
		"EnabledExtensions": h.cfg.EnabledExtensions,
	})
}

// authenticatorType returns a human-readable type name for an interface value,
// used in logging to identify which ControlPlaneAuthenticator handled a claim.
func authenticatorType(v any) string {
	switch v.(type) {
	case authz.DenyAllAuthenticator:
		return "DenyAllAuthenticator"
	default:
		s := strings.TrimPrefix(reflect.TypeOf(v).String(), "*")
		return s
	}
}
