package console_code_authorizer

import (
	"fmt"

	"seedwright/internal/config"
)

// Config holds the extension's tunable settings.
type Config struct {
	Enabled bool `yaml:"enabled"`
}

// LoadConfig returns the extension's config from the global app config.
// Sets defaults before reading, so callers get sensible values even
// when the extension section is absent.
func LoadConfig(cfg *config.Config) (Config, error) {
	c := Config{Enabled: true}
	if err := cfg.ExtensionConfig("joleuger/console_code_authorizer", &c); err != nil {
		return c, fmt.Errorf("console_code_authorizer: config: %w", err)
	}
	return c, nil
}
