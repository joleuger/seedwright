package authz_simple

import (
	"fmt"

	"seedwright/internal/authz"
	"gopkg.in/yaml.v3"
)

// --- Config shapes for the extension ---

// Config holds the parsed extension config block (extensions.joleuger/authz-simple
// in config.yaml).
type Config struct {
	// Groups maps group names (as Principal values, prefixed with "group:")
	// to their member lists.
	Groups map[authz.Principal][]authz.Principal `yaml:"groups"`

	// ServicePrincipals maps service-principal names to their configuration.
	ServicePrincipals map[authz.Principal]*ServicePrincipalConfig `yaml:"service_principals"`

	// RawRoleAssignments are the unparsed role assignment entries.
	RawRoleAssignments []rawRoleAssignment `yaml:"role_assignments"`
}

// ServicePrincipalConfig holds configuration for a single service principal.
type ServicePrincipalConfig struct {
	TokenEnv string `yaml:"token_env"`
}

// rawRoleAssignment is an intermediate representation of a role_assignment
// entry from config.yaml.
type rawRoleAssignment struct {
	Principal authz.Principal `yaml:"principal"`
	Scope     string          `yaml:"scope"`
	Role      string          `yaml:"role"`
}

// groupYAML is the raw YAML structure for a single group entry.
type groupYAML struct {
	Members []authz.Principal `yaml:"members"`
}

// servicePrincipalYAML is the raw YAML structure for a single service principal.
type servicePrincipalYAML struct {
	TokenEnv string `yaml:"token_env"`
}

// --- Adapter interface ---

// Adapter loads policy from storage. The ConfigAdapter reads from config.yaml.
type Adapter interface {
	LoadPolicy() (assignments []RoleAssignment, groups map[authz.Principal][]authz.Principal, err error)
}

// --- ConfigAdapter ---

// ConfigAdapter reads role_assignments/groups/service_principals from the
// extension config block in config.yaml.
type ConfigAdapter struct {
	assignments []RoleAssignment
	groups      map[authz.Principal][]authz.Principal
}

// NewConfigAdapter builds a ConfigAdapter from the parsed extension Config.
func NewConfigAdapter(cfg *Config) (*ConfigAdapter, error) {
	assignments := make([]RoleAssignment, len(cfg.RawRoleAssignments))
	for i, raw := range cfg.RawRoleAssignments {
		scope, err := authz.ParseScopeRef(raw.Scope)
		if err != nil {
			return nil, fmt.Errorf("role_assignments[%d]: %w", i, err)
		}
		assignments[i] = RoleAssignment{
			Principal: raw.Principal,
			Scope:     scope,
			Role:      Role(raw.Role),
		}
	}

	return &ConfigAdapter{
		assignments: assignments,
		groups:      cfg.Groups,
	}, nil
}

// LoadPolicy implements the Adapter interface.
func (a *ConfigAdapter) LoadPolicy() ([]RoleAssignment, map[authz.Principal][]authz.Principal, error) {
	return a.assignments, a.groups, nil
}

// --- Parsing helpers ---

// ParseConfig decodes the extension config block from the raw YAML node.
func ParseConfig(node yaml.Node) (*Config, error) {
	var rawYAML struct {
		Groups          map[string]groupYAML          `yaml:"groups"`
		ServicePrincipals map[string]servicePrincipalYAML `yaml:"service_principals"`
		RoleAssignments []rawRoleAssignment           `yaml:"role_assignments"`
	}
	if err := node.Decode(&rawYAML); err != nil {
		return nil, fmt.Errorf("decode authz-simple config: %w", err)
	}

	cfg := &Config{
		Groups:          make(map[authz.Principal][]authz.Principal),
		ServicePrincipals: make(map[authz.Principal]*ServicePrincipalConfig),
		RawRoleAssignments: rawYAML.RoleAssignments,
	}

	// Parse groups.
	for name, node := range rawYAML.Groups {
		p := authz.Principal("group:" + name)
		if len(node.Members) == 0 {
			cfg.Groups[p] = nil
		} else {
			members := make([]authz.Principal, len(node.Members))
			for i, m := range node.Members {
				members[i] = authz.Principal(m)
			}
			cfg.Groups[p] = members
		}
	}

	// Parse service principals.
	for name, node := range rawYAML.ServicePrincipals {
		p := authz.Principal("svc:" + name)
		cfg.ServicePrincipals[p] = &ServicePrincipalConfig{
			TokenEnv: node.TokenEnv,
		}
	}

	return cfg, nil
}
