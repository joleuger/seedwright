// Package slideshow implements a fullscreen slideshow of gallery elements.
//
// The extension plays the images selected by the gallery's active filters
// (favorites, sort, order, origin, and any extension-registered filter —
// all inherited as query params) one slide at a time. It owns one route
// (the fullscreen page); the queue itself is the core's generic elements
// API (GET /api/{project}/elements), so the extension never re-implements
// element listing or filter parsing.
//
// UI entry point: the "▶ Slideshow" button in the gallery toolbar,
// hard-coded in the core gallery template behind a hasExtension guard
// (the favorites pattern). There is deliberately no generic UI-injection
// hook — the button's placement and style are part of the gallery layout.
//
// See EXTENDING.md for the extension contract.
// See this package's EXTENSION.md for Slideshow-specific docs.
package slideshow

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"seedwright/internal/app"
	"seedwright/internal/authz"
	"seedwright/internal/config"
	"seedwright/internal/data"
)

// Config holds Slideshow's tunable settings.
type Config struct {
	Enabled bool `yaml:"enabled"`
}

// LoadConfig returns Slideshow's config from the global app config.
func LoadConfig(cfg *config.Config) (Config, error) {
	c := Config{Enabled: true}
	if err := cfg.ExtensionConfig("joleuger/slideshow", &c); err != nil {
		return c, fmt.Errorf("slideshow: config: %w", err)
	}
	return c, nil
}

// Extension holds the Slideshow extension's state and dependencies.
type Extension struct {
	mux         *http.ServeMux
	projectRepo data.ProjectRepository
	// pathPrefix is the configured server.path_prefix so the rendered
	// page builds correct URLs when the app is served under a subpath.
	pathPrefix string
}

// New constructs a new Slideshow extension.
func New(mux *http.ServeMux, projectRepo data.ProjectRepository, pathPrefix string) *Extension {
	return &Extension{
		mux:         mux,
		projectRepo: projectRepo,
		pathPrefix:  pathPrefix,
	}
}

// NewExtension constructs a Slideshow extension from an App instance.
// This is the entrypoint called from ext.RegisterAll.
func NewExtension(ctx context.Context, a *app.App) (*Extension, error) {
	// LoadConfig validates the config block (malformed YAML surfaces here).
	if _, err := LoadConfig(a.Config); err != nil {
		return nil, err
	}
	e := New(a.GetServeMux(), a.Projects, a.Config.Server.PathPrefix)
	e.RegisterRoutes(a)
	return e, nil
}

// RegisterRoutes registers Slideshow's HTTP route on the server mux.
// The slideshow page is view-scoped like the gallery it plays from.
func (e *Extension) RegisterRoutes(a *app.App) {
	e.mux.Handle("GET /basic/{project}/slideshow", authz.RequireAction(authz.ActionView, a.Authz)(http.HandlerFunc(e.handlePage)))
	slog.Debug("slideshow: registered routes", "count", 1,
		"routes", []string{"GET /basic/{project}/slideshow"})
}

// inheritedFilterQuery returns the slideshow's incoming query params minus
// the pagination params (page, per_page). The result is the set of gallery
// filters the slideshow inherits for its queue fetch — and the query string
// the exit action (Esc / ✕) returns the user to, so they land back on the
// same filtered gallery view.
func inheritedFilterQuery(q url.Values) string {
	inherited := make(url.Values)
	for key, values := range q {
		if key == "page" || key == "per_page" {
			continue
		}
		for _, v := range values {
			inherited.Add(key, v)
		}
	}
	return inherited.Encode()
}

// handlePage handles GET /basic/{project}/slideshow — the fullscreen
// slideshow page. It renders the shell only; the queue is fetched
// client-side from the core elements API with the inherited filter params.
func (e *Extension) handlePage(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	// Validate project exists (same contract as the other project pages).
	if _, err := e.projectRepo.GetProjectMeta(r.Context(), project); err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	tmpl := SlideShowTemplate()
	if err := tmpl.Execute(w, map[string]any{
		"Title":          "Slideshow",
		"Project":        project,
		"prefix":         e.pathPrefix,
		"InheritedQuery": inheritedFilterQuery(r.URL.Query()),
	}); err != nil {
		slog.Error("slideshow: render", "error", err)
	}
}
