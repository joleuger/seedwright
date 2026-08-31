package authz

import (
	"strings"
	"testing"
)

// --- ParseAuthConfig tests ---

func TestParseAuthConfig(t *testing.T) {
	tests := []struct {
		name     string
		config   string
		wantMech string
		wantEng  string
		wantPrinc Principal
		wantCtrl string
	}{
		{
			name: "empty config",
			config: "server:\n  listen: \":8080\"\n",
			wantMech: "static",
			wantEng:  "static",
			wantPrinc: "user:root",
			wantCtrl: "",
		},
		{
			name: "auth block with engine",
			config: `auth:
  mechanism: static
  engine: ext/joleuger/authz-simple
  principal: user:alice
`,
			wantMech: "static",
			wantEng:  "ext/joleuger/authz-simple",
			wantPrinc: "user:alice",
			wantCtrl: "",
		},
		{
			name: "auth block defaults",
			config: `auth:
  mechanism: static
`,
			wantMech: "static",
			wantEng:  "static",
			wantPrinc: "user:root",
			wantCtrl: "",
		},
		{
			name: "auth block with control plane",
			config: `auth:
  control_plane: ext/joleuger/console_code_authorizer
`,
			wantMech: "static",
			wantEng:  "static",
			wantPrinc: "user:root",
			wantCtrl: "ext/joleuger/console_code_authorizer",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseAuthConfig([]byte(tt.config))
			if err != nil {
				t.Fatalf("ParseAuthConfig() error = %v", err)
			}
			if cfg.Mechanism != tt.wantMech {
				t.Errorf("Mechanism = %q, want %q", cfg.Mechanism, tt.wantMech)
			}
			if cfg.Engine != tt.wantEng {
				t.Errorf("Engine = %q, want %q", cfg.Engine, tt.wantEng)
			}
			if cfg.Principal != tt.wantPrinc {
				t.Errorf("Principal = %q, want %q", cfg.Principal, tt.wantPrinc)
			}
			if cfg.ControlPlane != tt.wantCtrl {
				t.Errorf("ControlPlane = %q, want %q", cfg.ControlPlane, tt.wantCtrl)
			}
		})
	}
}

// --- ParseAuthConfig ControlPlane tests ---

func TestParseAuthConfig_ControlPlane(t *testing.T) {
	tests := []struct {
		name     string
		config   string
		wantCtrl string
	}{
		{
			name:     "empty config has no control plane",
			config:   "server:\n  listen: \":8080\"\n",
			wantCtrl: "",
		},
		{
			name:     "control_plane set",
			config:   "auth:\n  control_plane: ext/joleuger/console_code_authorizer\n",
			wantCtrl: "ext/joleuger/console_code_authorizer",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseAuthConfig([]byte(tt.config))
			if err != nil {
				t.Fatalf("ParseAuthConfig() error = %v", err)
			}
			if cfg.ControlPlane != tt.wantCtrl {
				t.Errorf("ControlPlane = %q, want %q", cfg.ControlPlane, tt.wantCtrl)
			}
		})
	}
}

// --- ValidateAuthConfig tests ---

func TestValidateAuthConfig(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *AuthConfig
		wantErr  bool
		errMatch string
	}{
		{
			name: "valid static config",
			cfg: &AuthConfig{
				Mechanism: "static",
				Engine:    "static",
				Principal: "user:root",
			},
			wantErr: false,
		},
		{
			name: "valid extension engine",
			cfg: &AuthConfig{
				Mechanism: "static",
				Engine:    "ext/joleuger/authz-simple",
				Principal: "user:root",
			},
			wantErr: false,
		},
		{
			name: "unknown mechanism",
			cfg: &AuthConfig{
				Mechanism: "ext/joleuger/auth-header",
				Engine:    "static",
				Principal: "user:root",
			},
			wantErr:     true,
			errMatch:    "no IdentityResolver extension is compiled in",
		},
		{
			name: "valid principal format",
			cfg: &AuthConfig{
				Mechanism: "static",
				Engine:    "static",
				Principal: "user:alice",
			},
			wantErr: false,
		},
		{
			name: "invalid principal format",
			cfg: &AuthConfig{
				Mechanism: "static",
				Engine:    "static",
				Principal: "foo:bar",
			},
			wantErr:     true,
			errMatch:    "unrecognized format",
		},
		{
			name: "valid control_plane extension key",
			cfg: &AuthConfig{
				Mechanism:    "static",
				Engine:       "static",
				Principal:    "user:root",
				ControlPlane: "ext/joleuger/console_code_authorizer",
			},
			wantErr: false,
		},
		{
			name: "empty control_plane is valid (defaults to DenyAll)",
			cfg: &AuthConfig{
				Mechanism:    "static",
				Engine:       "static",
				Principal:    "user:root",
				ControlPlane: "",
			},
			wantErr: false,
		},
		{
			name: "three-segment control_plane key is accepted",
			cfg: &AuthConfig{
				Mechanism:    "static",
				Engine:       "static",
				Principal:    "user:root",
				ControlPlane: "ext/joleuger/console_code_authorizer",
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAuthConfig(tt.cfg, nil) // roleAssignments param is interface{} now
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAuthConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errMatch != "" && !strings.Contains(err.Error(), tt.errMatch) {
				t.Errorf("ValidateAuthConfig() error = %q, want to contain %q", err, tt.errMatch)
			}
		})
	}
}
