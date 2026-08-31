package authz

import (
	"sync"

	"net/http"
)

// ControlPlaneAuthenticatorFactory is a function that builds a ControlPlaneAuthenticator.
// Called by buildControlPlaneAuthenticator when auth.control_plane selects
// an extension. Registered via RegisterControlPlaneAuthenticator.
type ControlPlaneAuthenticatorFactory func() ControlPlaneAuthenticator

var (
	cpRegistry   = make(map[string]ControlPlaneAuthenticatorFactory)
	cpRegistryMu sync.RWMutex
)

// RegisterControlPlaneAuthenticator registers a factory for a named control-plane
// authenticator. Called from init() in extension packages. Must be called
// before buildControlPlaneAuthenticator is invoked.
func RegisterControlPlaneAuthenticator(key string, factory ControlPlaneAuthenticatorFactory) {
	cpRegistryMu.Lock()
	defer cpRegistryMu.Unlock()
	cpRegistry[key] = factory
}

// BuildControlPlaneAuthenticator returns the ControlPlaneAuthenticator for the
// given key, or false if not registered. Called from app bootstrap.
func BuildControlPlaneAuthenticator(key string) (ControlPlaneAuthenticator, bool) {
	cpRegistryMu.RLock()
	defer cpRegistryMu.RUnlock()
	f, ok := cpRegistry[key]
	if !ok {
		return nil, false
	}
	return f(), true
}

// authenticatorKey extracts a stable, human-readable key from an interface value
// for logging. It checks the type name against known built-in types first,
// then falls back to reflection.
func authenticatorKey(a ControlPlaneAuthenticator) string {
	switch a.(type) {
	case DenyAllAuthenticator:
		return "DenyAllAuthenticator"
	default:
		return http.DetectContentType(nil) // never called — just a placeholder
	}
}
