package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"seedwright/internal/authz"

	"gopkg.in/yaml.v3"
)

// Config is the top-level application configuration.
type Config struct {
	Server      ServerConfig             `yaml:"server"`
	SDCPP       SDCPPConfig              `yaml:"sdcpp"`
	Storage     StorageConfig            `yaml:"storage"`
	StorageNode yaml.Node                // raw YAML node for the storage section (passed to storage factory)
	Database    DatabaseConfig           `yaml:"database"`
	Application ApplicationConfig        `yaml:"application"`
	E2E         E2EConfig                `yaml:"e2e"`
	Extensions  map[string]yaml.Node    `yaml:"extensions"` // per-extension opaque config

	// Auth is the parsed auth: block. Populated by Load(). Nil when no auth
	// block is present.
	Auth *authz.AuthConfig
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Listen     string `yaml:"listen"`
	PathPrefix string `yaml:"path_prefix"` // reverse-proxy subpath prefix (default "" = root)
}

// SDCPPConfig holds multiple sdcpp backend configurations.
type SDCPPConfig struct {
	// Backends is a list of named sdcpp backend definitions.
	Backends []SDCPPBackend `yaml:"backends"`
	// LegacyBaseURL captures the old single base_url for backward compatibility.
	LegacyBaseURL string `yaml:"base_url"` // deprecated: use backends instead
}

// SDCPPBackend defines a single sdcpp backend instance.
type SDCPPBackend struct {
	Name         string `yaml:"name"`
	BaseURL      string `yaml:"base_url"`
	Architecture string `yaml:"architecture"` // model architecture for this backend — e.g. "flux2", "sd3", "sdxl"
}

// StorageConfig holds object storage settings (S3-compatible, local
// filesystem, or in-process memory).
type StorageConfig struct {
	// Type is the storage backend type: "s3", "file", or "memory".
	// Defaults to "s3" for backward compatibility (only when a config
	// file is present; a missing config file boots on "memory" — see Load).
	Type string `yaml:"type"`

	// S3 fields (used when type=s3).
	Endpoint       string `yaml:"endpoint"`
	Region         string `yaml:"region"`
	Bucket         string `yaml:"bucket"`
	AccessKey      string `yaml:"access_key"`
	SecretKey      string `yaml:"secret_key"`
	ForcePathStyle bool   `yaml:"force_path_style"`

	// File fields (used when type=file).
	FilePath string `yaml:"file_path"`

	// Capacity (used when type=memory): total-byte limit, human-readable
	// ("10MB") or a bare byte count. Default 10MB. The device is
	// considered full after this limit (no eviction).
	Capacity string `yaml:"capacity"`
}

// DatabaseConfig holds SQLite cache settings.
type DatabaseConfig struct {
	SQLiteDatabase string `yaml:"sqlite_path"`
}

// ApplicationConfig holds application-level settings.
type ApplicationConfig struct {
	Title          string `yaml:"title"`
	DefaultProject string `yaml:"default_project"`
	// Onboarding selects the extension that provides the setup wizard
	// and the Customize page (an extension key, e.g.
	// "joleuger/onboarding"). Absent/empty defaults to the bundled
	// onboarding extension; "none" disables it. Core only parses this
	// key — the selected extension checks it in its Enabled() and no
	// other extension may claim the key.
	Onboarding string `yaml:"onboarding"`
}

// E2EConfig holds end-to-end test configuration flags.
type E2EConfig struct {
	EnableSDCPP bool `yaml:"e2e_with_sdcpp"`
}

// Load reads, parses, and validates a YAML configuration file.
// The storage YAML node is captured for the storage factory.
// The auth: block is parsed into authz.AuthConfig.
//
// A missing config file is not an error: Load returns the first-run
// defaults (ephemeral memory storage + the conventional sdcpp URL) so a
// fresh install always boots. A config file that exists but is invalid
// still fails loudly.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return noConfigDefaults(), nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Parse the auth: block from raw YAML data.
	// This must happen before Validate() so that validation can check auth config.
	authCfg, err := authz.ParseAuthConfig(data)
	if err != nil {
		return nil, fmt.Errorf("parse auth config: %w", err)
	}
	cfg.Auth = authCfg

	// Extract the raw YAML node for the storage section.
	// yaml.Node has pointer-based children, so we decode the whole
	// document into a map first to get a proper copy of the node.
	var raw map[string]yaml.Node
	if err := yaml.Unmarshal(data, &raw); err == nil {
		if n, ok := raw["storage"]; ok {
			cfg.StorageNode = n
		}
	}


	// Migrate legacy single base_url to backends list.
	if len(cfg.SDCPP.Backends) == 0 && cfg.SDCPP.LegacyBaseURL != "" {
		cfg.SDCPP.Backends = []SDCPPBackend{{
			Name:    "default",
			BaseURL: cfg.SDCPP.LegacyBaseURL,
		}}
	}

	if err := Validate(&cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	applyDefaults(&cfg)

	return &cfg, nil
}

// noConfigDefaults builds the configuration used when no config file
// exists: ephemeral memory storage (10 MB) and the conventional sdcpp
// location. A valid StorageNode is attached so the storage factory sees
// type=memory exactly as it would from a config file.
func noConfigDefaults() *Config {
	cfg := &Config{}
	cfg.Storage.Type = "memory"

	var raw map[string]yaml.Node
	if err := yaml.Unmarshal([]byte("storage:\n  type: memory\n"), &raw); err == nil {
		if n, ok := raw["storage"]; ok {
			cfg.StorageNode = n
		}
	}

	applyDefaults(cfg)
	return cfg
}

// BaseURL returns the base URL for the backend with the given name.
// Returns an error if the backend is not found.
func (c *Config) BackendURL(name string) (string, error) {
	for _, b := range c.SDCPP.Backends {
		if b.Name == name {
			return b.BaseURL, nil
		}
	}
	if name == "" && len(c.SDCPP.Backends) > 0 {
		return c.SDCPP.Backends[0].BaseURL, nil
	}
	return "", fmt.Errorf("backend %q not found (available: %d)", name, len(c.SDCPP.Backends))
}

// BackendNames returns the names of all configured backends.
func (c *Config) BackendNames() []string {
	names := make([]string, len(c.SDCPP.Backends))
	for i, b := range c.SDCPP.Backends {
		names[i] = b.Name
	}
	return names
}

// DefaultBackend returns the name of the first backend, or "default".
func (c *Config) DefaultBackend() string {
	if len(c.SDCPP.Backends) > 0 {
		return c.SDCPP.Backends[0].Name
	}
	return "default"
}

// BackendArchitecture returns the model architecture for the backend with the
// given name. Returns empty string when the backend is not found.
func (c *Config) BackendArchitecture(name string) string {
	for _, b := range c.SDCPP.Backends {
		if b.Name == name {
			return b.Architecture
		}
	}
	return ""
}

// ExtensionConfig returns the parsed config for an extension.
// extensionPath must match the registry key format: "owner/name"
// (e.g. "joleuger/batch"). It unmarshals the raw yaml.Node into
// out, which should be a pointer to the extension's config struct.
// Callers should set sensible defaults on out before calling.
// If no config was provided for this extension, out retains its
// default values and the function returns nil.
// Returns an error if the node cannot be decoded into out.
func (c *Config) ExtensionConfig(extensionPath string, out any) error {
	if c.Extensions == nil {
		return nil
	}
	node, ok := c.Extensions[extensionPath]
	if !ok {
		return nil
	}
	if node.Kind == 0 {
		return nil
	}
	return node.Decode(out)
}

// applyDefaults fills in missing optional values.
func applyDefaults(cfg *Config) {
	if cfg.Server.Listen == "" {
		cfg.Server.Listen = ":8080"
	}
	if cfg.Database.SQLiteDatabase == "" {
		cfg.Database.SQLiteDatabase = "cache.db"
	}
	if cfg.Application.Title == "" {
		cfg.Application.Title = "seedwright"
	}
	if cfg.Application.DefaultProject == "" {
		cfg.Application.DefaultProject = "default"
	}

	// Ensure at least one backend is configured.
	// http://127.0.0.1:1234 is the conventional sdcpp location
	// (the app appends /sdcpp/v1/... to it).
	if len(cfg.SDCPP.Backends) == 0 {
		cfg.SDCPP.Backends = []SDCPPBackend{{
			Name:    "default",
			BaseURL: "http://127.0.0.1:1234",
		}}
	}

	// Default S3 region for S3-compatible backends (e.g., Garage, MinIO).
	if cfg.Storage.Region == "" {
		cfg.Storage.Region = "garage"
	}
}

// Validate checks that the configuration has sensible values.
func Validate(cfg *Config) error {
	var errs []string

	if cfg.Server.Listen == "" {
		errs = append(errs, "server.listen is required")
	}
	// Validate storage: type-specific requirements.
	storageType := cfg.Storage.Type
	if storageType == "" {
		storageType = "s3" // default
	}
	if storageType == "s3" {
		if cfg.Storage.Endpoint == "" {
			errs = append(errs, "storage.endpoint is required (type=s3)")
		}
		if !isValidURL(cfg.Storage.Endpoint) {
			errs = append(errs, fmt.Sprintf("storage.endpoint is not a valid URL: %s", cfg.Storage.Endpoint))
		}
		if cfg.Storage.Bucket == "" {
			errs = append(errs, "storage.bucket is required (type=s3)")
		}
		if cfg.Storage.Region == "" {
			errs = append(errs, "storage.region is required (type=s3)")
		}
	} else if storageType == "file" {
		if cfg.Storage.FilePath == "" {
			errs = append(errs, "storage.file_path is required (type=file)")
		}
	} else if storageType == "memory" {
		// Nothing required; capacity is optional (default 10MB).
	} else {
		errs = append(errs, fmt.Sprintf("unknown storage backend type: %q (supported: s3, file, memory)", storageType))
	}
	if cfg.Database.SQLiteDatabase == "" {
		errs = append(errs, "database.sqlite_path is required")
	}

	// Validate backends.
	if len(cfg.SDCPP.Backends) == 0 {
		errs = append(errs, "sdcpp.backends must have at least one entry")
	}
	for i, b := range cfg.SDCPP.Backends {
		if b.Name == "" {
			errs = append(errs, fmt.Sprintf("sdcpp.backends[%d].name is required", i))
		}
		if b.BaseURL == "" {
			errs = append(errs, fmt.Sprintf("sdcpp.backends[%d].base_url is required", i))
		} else if !isValidURL(b.BaseURL) {
			errs = append(errs, fmt.Sprintf("sdcpp.backends[%d].base_url is not a valid URL: %s", i, b.BaseURL))
		}
	}

	// Validate extension keys.
	for k := range cfg.Extensions {
		if !isValidExtensionKey(k) {
			errs = append(errs, fmt.Sprintf("extensions.%q: invalid extension key (must match ^[a-zA-Z0-9][a-zA-Z0-9._-]*$)", k))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed: %s", strings.Join(errs, "; "))
	}
	return nil
}

func isValidURL(s string) bool {
	parts := strings.SplitN(s, "://", 2)
	if len(parts) < 2 {
		return false
	}
	scheme := parts[0]
	if scheme != "http" && scheme != "https" {
		return false
	}
	host := parts[1]
	return len(host) > 0
}

// isValidExtensionKey validates that s matches the "owner/name" format.
// Each segment (owner, name) must start with [a-zA-Z0-9] and contain
// only [a-zA-Z0-9._-].
func isValidExtensionKey(s string) bool {
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		ok, _ := regexp.MatchString(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`, p)
		if !ok {
			return false
		}
	}
	return true
}
