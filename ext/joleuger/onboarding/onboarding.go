// Package onboarding implements the first-run setup wizard and the
// permanent "Customize" page as an extension to seedwright.
//
// It answers three questions, in order:
//  1. What do you actually want? (profiles — the first thing you pick)
//  2. Where do your images live? (memory by default — ephemeral, 10 MB)
//  3. Where is your sdcpp instance? (conventionally http://127.0.0.1:1234)
//
// The wizard shows a live PREVIEW of the config file it would produce and
// offers two ways out: write it (gated — see below) or download it as a
// file. Writing an EXISTING config requires, in addition to the
// manage_permissions authorization:
//   - extensions.joleuger/onboarding.allow_config_write: true in the
//     RUNNING configuration, and
//   - an explicit confirm_overwrite in the request (the UI's checkbox).
//
// A fresh file (no config present) is always written — there is nothing
// to overwrite. Config previews never reveal secrets of the current
// config (S3 keys are masked); the download endpoint, which is
// authorization-gated, returns the real values.
//
// The wizard persists its choices by writing config.yaml to the path the
// app was started with (App.ConfigPath) — the one file it owns.
// Everything else (projects, verification probes) uses public core APIs.
//
// Selected via application.onboarding in config.yaml (default: this
// extension; "none" disables it).
//
// See this package's EXTENSION.md for details.
package onboarding

import (
	"context"
	"html/template"
	"net/http"

	"seedwright/internal/app"
	"seedwright/internal/config"
)

// Config holds onboarding's tunable settings.
type Config struct {
	Enabled bool `yaml:"enabled"`
	// AllowConfigWrite permits the wizard to OVERWRITE an existing
	// config.yaml. Without it the page is preview-only (download still
	// works). A missing config file is always written — the flag guards
	// overwrites only. Default: false.
	AllowConfigWrite bool `yaml:"allow_config_write"`
}

// LoadConfig returns onboarding's config from the global app config.
func LoadConfig(cfg *config.Config) (Config, error) {
	c := Config{Enabled: true}
	if err := cfg.ExtensionConfig(OnboardingKey, &c); err != nil {
		return c, err
	}
	return c, nil
}

// Extension holds the onboarding extension's state.
type Extension struct {
	a          *app.App
	cfg        Config
	pathPrefix string
	mux        *http.ServeMux
}

// NewExtension constructs the onboarding extension from an App instance.
// This is the entrypoint called from ext.RegisterAll.
func NewExtension(_ context.Context, a *app.App) (*Extension, error) {
	c, err := LoadConfig(a.Config)
	if err != nil {
		return nil, err
	}
	e := &Extension{
		a:          a,
		cfg:        c,
		pathPrefix: a.Config.Server.PathPrefix,
		mux:        a.GetServeMux(),
	}
	e.registerHooks(a)
	e.registerRoutes(a)
	return e, nil
}

// registerHooks appends the "Setup & Customize" button to the welcome
// (project selection) page.
func (e *Extension) registerHooks(a *app.App) {
	if a.Hooks != nil {
		href := e.pathPrefix + "/onboarding"
		a.Hooks.WelcomeExtras = append(a.Hooks.WelcomeExtras, func(_ context.Context) (template.HTML, error) {
			return template.HTML(`<div style="display:flex;align-items:center;gap:0.75rem;flex-wrap:wrap;background:#16213e;border:1px solid #0f3460;border-radius:8px;padding:0.9rem 1.25rem">
  <span style="font-size:1.1rem">✨</span>
  <span style="color:#a0a0b0;font-size:0.88rem">New here, or want something that doesn't exist yet? Check your setup, apply a profile, or ask an agent to build a feature.</span>
  <a href="` + href + `" style="margin-left:auto;color:#53d8fb;font-weight:600;font-size:0.9rem;text-decoration:none">Setup &amp; Customize →</a>
</div>`), nil
		})
	}
}
