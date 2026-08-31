package authz

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// isValidExtensionKey validates that s matches the "owner/name" format.
// Each segment (owner, name) must start with [a-zA-Z0-9] and contain
// only [a-zA-Z0-9._-].
var extensionKeyRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func isValidExtensionKey(s string) bool {
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		if !extensionKeyRe.MatchString(p) {
			return false
		}
	}
	return true
}

// --- Config shapes ---

// AuthConfig holds the parsed auth: block from config.yaml. It is intentionally
// minimal: only the fields the core needs to select and configure the
// built-in StaticEnforcer. All RBAC-specific fields (groups,
// service_principals, role_assignments) live in the extension's own config
// block: extensions.joleuger/authz-simple in config.yaml.
type AuthConfig struct {
	// Mechanism selects which IdentityResolver is active. Exactly one per
	// deployment: "static" (core built-in) or an extension name like
	// "ext/joleuger/auth-header". For v1, only "static" is available.
	Mechanism string

	// Engine selects which Enforcer implementation runs. Defaults to
	// "static" (StaticEnforcer). To enable the full RBAC engine, set to
	// "ext/joleuger/authz-simple".
	Engine string

	// Principal is the fixed principal used by both StaticResolver and
	// StaticEnforcer. Defaults to "user:root".
	Principal Principal

	// ControlPlane selects which ControlPlaneAuthenticator is active.
	// Empty string (the default) means DenyAllAuthenticator — no one can
	// claim ownership until a real extension is configured. Set to an
	// extension key (e.g. "ext/joleuger/console_code_authorizer") to
	// enable dynamic ownership claims via that mechanism.
	ControlPlane string
}

// --- Config parsing ---

// authConfigYAML is the raw YAML structure for the core auth: block only.
type authConfigYAML struct {
	Mechanism    string `yaml:"mechanism"`
	Engine       string `yaml:"engine"`
	Principal    string `yaml:"principal"`
	ControlPlane string `yaml:"control_plane"`
}

// ParseAuthConfig extracts and decodes only the core auth: fields from the
// top-level YAML config data. It does NOT parse groups, service_principals,
// or role_assignments — those belong in the extension config block.
//
// Returns a zero-value AuthConfig (mechanism="static", engine="static",
// principal="user:root") when no auth: block exists.
func ParseAuthConfig(rawConfigData []byte) (*AuthConfig, error) {
	var raw map[string]yaml.Node
	if err := yaml.Unmarshal(rawConfigData, &raw); err != nil {
		return nil, fmt.Errorf("parse config for auth section: %w", err)
	}

	var authYAML authConfigYAML
	if node, ok := raw["auth"]; ok {
		if err := node.Decode(&authYAML); err != nil {
			return nil, fmt.Errorf("decode auth config: %w", err)
		}
	}

	cfg := &AuthConfig{
		Mechanism:    authYAML.Mechanism,
		Engine:       authYAML.Engine,
		Principal:    Principal(authYAML.Principal),
		ControlPlane: authYAML.ControlPlane,
	}

	// Default: mechanism is "static" if not specified.
	if cfg.Mechanism == "" {
		cfg.Mechanism = "static"
	}

	// Default: engine is "static" if not specified.
	if cfg.Engine == "" {
		cfg.Engine = "static"
	}

	// Default: principal is "user:root" if not specified.
	if cfg.Principal == "" {
		cfg.Principal = "user:root"
	}

	return cfg, nil
}

// --- Validation ---

// ValidateAuthConfig checks the parsed AuthConfig for correctness and returns
// errors for any violations. Validation runs at startup and prevents silent
// degradation.
//
// Checks:
//   - mechanism is "static" (v1 only; any other value fails)
//   - engine is a known value ("static" or an extension key)
//   - principal is a valid principal value
//
// Reserved-name collisions on role_assignments, groups, and service
// principals are validated by the extension that owns those fields.
// The roleAssignments parameter is accepted for API compatibility but
// ignored by core validation — extensions validate their own assignments.
func ValidateAuthConfig(cfg *AuthConfig, _ interface{}) error {
	var errs []string

	// Mechanism validation: v1 only supports "static".
	if cfg.Mechanism != "static" {
		errs = append(errs, fmt.Sprintf(
			"auth.mechanism %q: no IdentityResolver extension is compiled in; v1 only supports \"static\"",
			cfg.Mechanism))
	}

	// Engine validation: must be "static" or a known extension key.
	if cfg.Engine != "static" {
		// Accept any non-empty engine value — if an extension doesn't
		// register itself, buildEnforcer returns ErrUnknownEngine at
		// startup, which is a clear enough failure.
		if cfg.Engine == "" {
			errs = append(errs, "auth.engine is required when auth is configured")
		}
	}

	// ControlPlane validation: if set, must be a non-empty string.
	// Empty string is allowed (defaults to DenyAllAuthenticator).
	// Accept any non-empty value — if an extension doesn't register
	// itself, core will log a warning at startup.
	if cfg.ControlPlane == "" {
		// OK — defaults to DenyAllAuthenticator.
	}

	// Principal validation.
	if err := ValidatePrincipal(cfg.Principal); err != nil {
		errs = append(errs, err.Error())
	}

	if len(errs) > 0 {
		return fmt.Errorf("auth config validation failed: %s", strings.Join(errs, "; "))
	}
	return nil
}
