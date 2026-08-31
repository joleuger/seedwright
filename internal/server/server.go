package server

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"seedwright/internal/authz"
	"seedwright/internal/data"
	"seedwright/internal/data/model"
	"seedwright/internal/storage"
)

// Section represents one settings content area (e.g. "Core" project settings,
// or an extension's settings tab). It is returned by the SettingsSection hook.
type Section struct {
	// ID is a unique identifier for this section, used in the URL hash
	// and as a key for the section data map. "core" is reserved for
	// the core project settings. Extensions should use the format
	// "owner/extension" (e.g. "joleuger/photobooth").
	ID string

	// Label is the display name shown in the sidebar navigation.
	Label string

	// HTML is the form content for this section. It is rendered directly
	// as template.HTML into the settings page content area.
	HTML template.HTML

	// Fields lists the field keys this section contains, in rendering
	// order. The browser uses this to snapshot/compare values on the
	// client side for change tracking. Each value is the string form
	// of FieldInfo.Type.
	Fields []FieldInfo
}

// FieldInfo describes a single settings field for client-side change
// tracking and DOM querying. The json tags matter: this struct is
// embedded in the settings page's `const sections = {...}` JS object,
// where the browser reads `f.key` / `f.type`.
type FieldInfo struct {
	// Key is the data-field attribute value used by the browser to
	// locate and snapshot the field.
	Key string `json:"key"`

	// Type is the input type: "text", "textarea", "checkbox", "number",
	// "select", or "range".
	Type string `json:"type"`
}

// Hooks holds extension lifecycle callbacks.
// Empty by default — core behaves identically whether or not any extension
// has appended to them. Hook errors are logged, never fatal, never block
// other hooks or other jobs.
type Hooks struct {
	// OnJobTerminal fires exactly once per job, immediately after
	// JobRepository.UpdateStatus persists a terminal status
	// ("completed", "failed", "cancelled"). elem is nil if the job never
	// reached element creation.
	OnJobTerminal []func(ctx context.Context, elem *model.Element, job data.JobRecord) error

	// OnProjectDeleted fires after DeleteProject has removed core's
	// own S3 objects and SQLite rows for the project, before the
	// HTTP response is sent. Extensions must remove their own
	// ext/{owner}/{extension}/ prefix here — SQLite cleanup may
	// already be handled by ON DELETE CASCADE if foreign_keys is on.
	OnProjectDeleted []func(ctx context.Context, project string) error

	// DashboardExtras lets an extension inject a fragment of HTML
	// into the project dashboard (e.g. a "Start Batch" card).
	DashboardExtras []func(ctx context.Context, project string) (template.HTML, error)

	// NavBarItems lets an extension inject navigation bar HTML
	// fragments (e.g. <a href="...">Favorites</a>) into the project
	// navigation bar, rendered between the core nav links and the
	// nav content area. Called on every project-scoped page
	// (dashboard, gallery, element detail, batch progress).
	NavBarItems []func(ctx context.Context, project string) (template.HTML, error)

	// ElementActions lets an extension inject action button HTML
	// into the element detail page, rendered below the primary
	// action buttons ("Reuse Settings", "Retry", "Download").
	// Called on the element detail page only.
	ElementActions []func(ctx context.Context, project, elementID string) (template.HTML, error)

	// MoreNavItems lets an extension inject navigation bar HTML
	// fragments into the "More" dropdown menu, rendered on every
	// project-scoped page. Each item is rendered as an anchor tag
	// (<a href="...">Label</a>). Called on every project-scoped page
	// (dashboard, gallery, element detail, batch progress).
	MoreNavItems []func(ctx context.Context, project string) (template.HTML, error)

	// WelcomeExtras lets an extension inject HTML into the welcome
	// (project selection) page, rendered after the project grid.
	// No project context — the welcome page has none.
	WelcomeExtras []func(ctx context.Context) (template.HTML, error)

	// SettingsField lets an extension inject a fragment of HTML into
	// the project settings modal, rendered after the built-in fields
	// (backend selector, hidden toggle). Called on the settings modal
	// load (GET /api/{project}/settings) — each field is rendered once
	// and the browser populates it from the API response. Called on
	// every project-scoped page that shows the settings modal.
	//
	// Deprecated: use SettingsSection instead. Core adapts SettingsField
	// into a Section entry for backward compatibility.
	SettingsField []func(ctx context.Context, project string) (template.HTML, error)

	// SettingsSection lets an extension return a scoped settings section
	// with its own sidebar nav item, form fields, and field metadata for
	// client-side change tracking. Called once per section during
	// GET /basic/{project}/settings page load. The section ID should use
	// the format "owner/extension" (e.g. "joleuger/photobooth").
	SettingsSection []func(ctx context.Context, project string, delta model.ProjectSettingsDelta) (*Section, error)

	// SettingsSavers maps "owner/extension" to the function that validates
	// and persists that extension's project settings. The core's settings
	// endpoint (POST /api/{project}/settings) dispatches non-core section
	// saves here. The core itself never read-modify-writes an extension's
	// settings delta — the extension owns its own S3 file and only the
	// extension knows how to validate the fields (EXTENDING.md Part 1.5).
	// An extension that registers a SettingsSection must register a saver
	// for the same section ID.
	SettingsSavers map[string]SettingsSaver
}

// SettingsSaver validates and persists one extension's project settings.
// fields is the raw JSON map submitted by the settings page for this
// section (already decoded). Implementations read-modify-write the
// extension's own settings file and mirror hot-path fields to SQLite.
//
// Error contract: wrap invalid-field failures in *ValidationError so the
// core answers 400 with the message; any other error is treated as a
// system/storage failure and answered with 500.
type SettingsSaver func(ctx context.Context, project string, fields map[string]any) error

// ValidationError marks a settings-save failure caused by the submitted
// fields (as opposed to a storage or system failure). The core answers
// 400 with the message; other errors become 500.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// Config holds all dependencies needed by handlers.
type Config struct {
	Title         string
	PathPrefix    string // reverse-proxy subpath prefix (default "" = root)
	Storage       storage.StorageBackend
	ProjectRepo   data.ProjectRepository
	ElementRepo   data.ElementRepository
	JobService    *data.JobService
	BackendNames  []string // names of configured sdcpp backends
	DefaultBackend string  // name of the default/first backend
	BackendArchitecture func(name string) string // returns the model architecture for a backend by name
	Hooks         *Hooks
	Debug         bool   // when true, log registered endpoints at startup
	Authz         authz.Enforcer  // nil when auth is disabled (backward compat)
	// IdentityResolver resolves the current principal from the request.
	// Used by the claim-ownership page to know which principal to grant.
	IdentityResolver authz.IdentityResolver
	// ControlPlaneAuthenticator verifies whether a request carries valid
	// proof of control-plane recovery authority. Nil when auth is disabled
	// or no control plane is configured (defaults to DenyAllAuthenticator).
	ControlPlaneAuthenticator authz.ControlPlaneAuthenticator
	// EnabledExtensions lists the keys of currently-enabled extensions
	// (e.g. "joleuger/batch"). Passed through to templates for conditional
	// rendering.
	EnabledExtensions []string
}

// New creates an HTTP handler tree with REST-style routes.
//
// Route organisation:
//
//	HTML pages (rendered templates) → /basic/…
//	JSON endpoints                 → /api/…
//
// Root routes (/ and /basic/, POST /create-project, POST /switch-backend)
// live outside any project namespace.
func New(cfg *Config) http.Handler {
	mux := http.NewServeMux()

	h := &handler{
		cfg:        cfg,
		templates:  loadTemplates(),
		promptHelp: template.HTML(promptHelpHTML),
	}

	// Wire OnJobTerminal hook into JobService.
	if cfg.Hooks != nil && cfg.JobService != nil {
		cfg.JobService.OnJobTerminal = func(ctx context.Context, elem *model.Element, job data.JobRecord) error {
			for _, fn := range cfg.Hooks.OnJobTerminal {
				if err := fn(ctx, elem, job); err != nil {
					slog.Warn("on-job-terminal hook", "element", job.ElementID, "status", job.Status, "error", err)
				}
			}
			return nil
		}
	}

	// ---- Root routes (no project context) ----

	// GET / — redirect to the app root under /basic/
	mux.HandleFunc("GET /{$}", h.redirectToAppRoot)

	// GET /basic — redirect to /basic/ (trailing slash) so the welcome page
	// renders and project links work correctly.
	mux.HandleFunc("GET /basic", h.redirectBasicToTrailingSlash)

	// GET /basic/ — serves the welcome (project selection) page.
	// This is the app root — the canonical URL for the welcome page.
	mux.HandleFunc("GET /basic/", h.welcome)

	// POST /create-project — create a new project from welcome page (JSON body)
	mux.Handle("POST /create-project", authz.RequireAction(authz.ActionCreateProject, cfg.Authz)(http.HandlerFunc(h.createProjectFromWelcome)))

	// POST /switch-backend — switch active sdcpp backend (JSON)
	mux.HandleFunc("POST /switch-backend", h.switchBackend)

	// claim-ownership is an unauthenticated route — the whole point is
	// to bootstrap a principal into the system when no other path exists.
	h.routeClaimOwnership(mux)

	// ---- HTML pages (GET) — /basic/… ----

	// GET /basic/{project}/ — project dashboard with trailing slash (redirects to non-trailing)
	mux.Handle("GET /basic/{project}/", authz.RequireAction(authz.ActionView, cfg.Authz)(http.HandlerFunc(h.projectPageTrailingSlash)))

	// GET /basic/{project} — project dashboard
	mux.Handle("GET /basic/{project}", authz.RequireAction(authz.ActionView, cfg.Authz)(http.HandlerFunc(h.projectPage)))

	// GET /basic/{project}/gallery — gallery
	mux.Handle("GET /basic/{project}/gallery", authz.RequireAction(authz.ActionView, cfg.Authz)(http.HandlerFunc(h.gallery)))

	// GET /basic/{project}/element/{id} — element detail
	mux.Handle("GET /basic/{project}/element/{id}", authz.RequireAction(authz.ActionView, cfg.Authz)(http.HandlerFunc(h.elementPage)))

	// GET /basic/{project}/element/{id}/image — serve image
	mux.Handle("GET /basic/{project}/element/{id}/image", authz.RequireAction(authz.ActionView, cfg.Authz)(http.HandlerFunc(h.serveImage)))

	// GET /basic/{project}/external — external images page
	mux.Handle("GET /basic/{project}/external", authz.RequireAction(authz.ActionView, cfg.Authz)(http.HandlerFunc(h.externalPage)))

	// GET /basic/{project}/settings — project settings page (HTML)
	mux.Handle("GET /basic/{project}/settings", authz.RequireAction(authz.ActionDeleteProject, cfg.Authz)(http.HandlerFunc(h.projectSettingsPage)))

	// ---- JSON API (POST) — /api/… ----

	// POST /api/{project}/generate — submit job
	mux.Handle("POST /api/{project}/generate", authz.RequireAction(authz.ActionGenerate, cfg.Authz)(http.HandlerFunc(h.generate)))

	// POST /api/{project}/jobs/cancel-all — cancel all stuck jobs
	mux.Handle("POST /api/{project}/jobs/cancel-all", authz.RequireAction(authz.ActionGenerate, cfg.Authz)(http.HandlerFunc(h.cancelAllJobs)))

	// POST /api/{project}/element/{id}/generate-clone — create a new sibling element with the same params but a new random seed
	mux.Handle("POST /api/{project}/element/{id}/generate-clone", authz.RequireAction(authz.ActionGenerate, cfg.Authz)(http.HandlerFunc(h.generateClone)))

	// POST /api/{project}/element/{id}/regenerate-in-place — recreate the image in-place with same params (same element ID)
	mux.Handle("POST /api/{project}/element/{id}/regenerate-in-place", authz.RequireAction(authz.ActionGenerate, cfg.Authz)(http.HandlerFunc(h.regenerateInPlace)))

	// POST /api/{project}/settings — update project settings (JSON)
	mux.Handle("POST /api/{project}/settings", authz.RequireAction(authz.ActionDeleteProject, cfg.Authz)(http.HandlerFunc(h.updateProjectSettings)))

	// POST /api/{project}/create — create a new project
	mux.Handle("POST /api/{project}/create", authz.RequireAction(authz.ActionCreateProject, cfg.Authz)(http.HandlerFunc(h.createProject)))

	// POST /api/{project}/element/{id}/delete — delete an element
	mux.Handle("POST /api/{project}/element/{id}/delete", authz.RequireAction(authz.ActionDeleteElement, cfg.Authz)(http.HandlerFunc(h.deleteElement)))

	// POST /api/{project}/delete — delete a project
	mux.Handle("POST /api/{project}/delete", authz.RequireAction(authz.ActionDeleteProject, cfg.Authz)(http.HandlerFunc(h.deleteProject)))

	// POST /api/{project}/elements/img2img — img2img generation
	mux.Handle("POST /api/{project}/elements/img2img", authz.RequireAction(authz.ActionGenerate, cfg.Authz)(http.HandlerFunc(h.img2img)))

	// POST /api/{project}/elements/upload — upload an external (non-generated) image
	mux.Handle("POST /api/{project}/elements/upload", authz.RequireAction(authz.ActionGenerate, cfg.Authz)(http.HandlerFunc(h.uploadExternal)))

	// ---- JSON API (GET) — /api/… ----

	// GET /api/{project}/jobs/active — active jobs JSON
	mux.Handle("GET /api/{project}/jobs/active", authz.RequireAction(authz.ActionView, cfg.Authz)(http.HandlerFunc(h.activeJobsJSON)))

	// GET /api/{project}/jobs/{jobId} — job status JSON (jobId is a per-submission UUID)
	mux.Handle("GET /api/{project}/jobs/{jobId}", authz.RequireAction(authz.ActionView, cfg.Authz)(http.HandlerFunc(h.jobStatusJSON)))

	// POST /api/{project}/jobs/{jobId}/cancel — cancel job (jobId is a per-submission UUID)
	mux.Handle("POST /api/{project}/jobs/{jobId}/cancel", authz.RequireAction(authz.ActionGenerate, cfg.Authz)(http.HandlerFunc(h.cancelJob)))

	// GET /api/{project}/settings — project settings data (JSON)
	mux.Handle("GET /api/{project}/settings", authz.RequireAction(authz.ActionView, cfg.Authz)(http.HandlerFunc(h.projectSettings)))

	// GET /api/{project}/elements — generic element list (gallery's JSON twin)
	mux.Handle("GET /api/{project}/elements", authz.RequireAction(authz.ActionView, cfg.Authz)(http.HandlerFunc(h.elementsJSON)))

	// Log registered endpoints at debug level.
	if cfg.Debug {
		routes := []string{
			"GET /{$} → redirectToAppRoot",
			"GET /basic → redirectBasicToTrailingSlash",
			"GET /basic/ → welcome",
			"POST /create-project → createProjectFromWelcome",
			"POST /switch-backend → switchBackend",
			"GET /basic/{project}/ → projectPageTrailingSlash",
			"GET /basic/{project} → projectPage",
			"GET /basic/{project}/gallery → gallery",
			"GET /basic/{project}/element/{id} → elementPage",
			"GET /basic/{project}/element/{id}/image → serveImage",
			"GET /basic/{project}/external → externalPage",
			"POST /api/{project}/generate → generate",
			"POST /api/{project}/jobs/cancel-all → cancelAllJobs",
			"POST /api/{project}/element/{id}/generate-clone → generateClone",
			"POST /api/{project}/element/{id}/regenerate-in-place → regenerateInPlace",
			"POST /api/{project}/settings → updateProjectSettings",
			"POST /api/{project}/create → createProject",
			"POST /api/{project}/element/{id}/delete → deleteElement",
			"POST /api/{project}/delete → deleteProject",
			"POST /api/{project}/elements/img2img → img2img",
			"POST /api/{project}/elements/upload → uploadExternal",
			"GET /api/{project}/jobs/active → activeJobsJSON",
			"GET /api/{project}/jobs/{jobId} → jobStatusJSON",
			"POST /api/{project}/jobs/{jobId}/cancel → cancelJob",
			"GET /api/{project}/settings → projectSettings",
			"GET /api/{project}/elements → elementsJSON",
		}
		slog.Debug("registered endpoints", "prefix", cfg.PathPrefix, "count", len(routes))
		for _, r := range routes {
			slog.Debug("endpoint", "route", r)
		}
	}

	return mux
}

type handler struct {
	cfg        *Config
	templates  *template.Template
	promptHelp template.HTML
}

// routeClaimOwnership registers the claim-ownership page route.
// This is an unauthenticated route — the whole point is to bootstrap
// a principal into the system when no other path exists.
func (h *handler) routeClaimOwnership(mux *http.ServeMux) {
	// GET /claim-ownership — show the claim page
	mux.Handle("GET /claim-ownership", http.HandlerFunc(h.claimOwnership))
	// POST /claim-ownership — process the claim
	mux.Handle("POST /claim-ownership", http.HandlerFunc(h.claimOwnership))
}

const promptHelpHTML = `
<h4>Quick prompts</h4>
<ul>
	<li><code>a photograph of a beautiful sunset over the ocean</code></li>
	<li><code>a cyberpunk city at night with neon lights, cinematic, 8k</code></li>
	<li><code>an oil painting of a cottage in a meadow, impressionist style</code></li>
	<li><code>a cat sitting on a windowsill, soft lighting, detailed fur</code></li>
</ul>
<h4>Tips</h4>
<ul>
	<li>Be descriptive — include subject, style, lighting, mood</li>
	<li>Use negative_prompt to exclude unwanted elements</li>
	<li>Higher CFG (7–12) follows prompt more strictly; lower (3–5) is more creative</li>
	<li>Seed -1 = random; use a fixed seed to reproduce results</li>
	<li>Steps: 20–40 for most quality; 50+ for marginal improvement</li>
</ul>
`

// projectFromPath extracts the project name from the request path.
func projectFromPath(path string) string {
	p := strings.TrimPrefix(path, "/")
	parts := strings.SplitN(p, "/", 2)
	return parts[0]
}

// parseBoolSafe parses a query parameter as a boolean.
func parseBoolSafe(val string, def bool) bool {
	if val == "" {
		return def
	}
	switch strings.ToLower(val) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
