// Package console_code_authorizer implements the control-plane authenticator
// for the "console-code" pattern (OAuth Device Authorization Grant, RFC 8628).
//
// It generates short-lived, single-use codes that are logged to stdout
// and accepted via a form POST on the extension's own page.
//
// The extension registers itself as a ControlPlaneAuthenticator factory via
// RegisterControlPlaneAuthenticator in its init() function, and registers
// its HTTP routes on the app's serve mux.
//
// See EXTENSION.md for the full extension contract and safety notes.
package console_code_authorizer

import (
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"

	"seedwright/internal/app"
	"seedwright/internal/authz"
	"seedwright/internal/data"
)

const extensionKey = "joleuger/console_code_authorizer"

// Extension holds the console-code authorizer's state.
type Extension struct {
	db          *sql.DB
	authorizer  *Authorizer
	mux         *http.ServeMux
	projectRepo data.ProjectRepository
	cfg         Config
}

// NewExtension constructs a console-code authorizer extension from an App
// instance. This is the entrypoint called from ext.RegisterAll.
func NewExtension(ctx context.Context, a *app.App) (*Extension, error) {
	cfg, err := LoadConfig(a.Config)
	if err != nil {
		return nil, err
	}

	e := &Extension{
		db:         a.DB,
		authorizer: New(),
		mux:        a.GetServeMux(),
		cfg:        cfg,
	}
	e.projectRepo = a.Projects

	e.RegisterRoutes()
	e.RegisterHooks(a)
	return e, nil
}

// RegisterHooks appends the MoreNavItems hook.
func (e *Extension) RegisterHooks(a *app.App) {
	if a.Hooks != nil {
		a.Hooks.MoreNavItems = append(a.Hooks.MoreNavItems, e.MoreNavItems)
	}
}

// Sync is a no-op — the extension has no SQLite state to rebuild from S3.
func Sync(ctx context.Context, a *app.App) error {
	return nil
}

// --- Hooks ---

// MoreNavItems renders the Console Code link for the "More" dropdown menu.
func (e *Extension) MoreNavItems(ctx context.Context, project string) (template.HTML, error) {
	return template.HTML(`<a href="/console_code/` + project + `" style="display:block;padding:0.4rem 1rem;color:#a0a0b0;text-decoration:none;font-size:0.85rem;border-radius:0">🔑 Console Code</a>`), nil
}

// --- Routes ---

// RegisterRoutes registers the extension's HTTP routes.
func (e *Extension) RegisterRoutes() {
	// GET /console_code/{project} — display the console code page
	e.mux.Handle("GET /console_code/", authz.Public()(http.HandlerFunc(e.handleConsoleCodeIndex)))
	e.mux.Handle("GET /console_code/{project}", authz.Public()(http.HandlerFunc(e.handleConsoleCodePage)))
	e.mux.Handle("GET /api/{project}/console_code", authz.Public()(http.HandlerFunc(e.handleConsoleCode)))
	e.mux.Handle("POST /api/{project}/console_code/generate", authz.Public()(http.HandlerFunc(e.handleGenerateCode)))
}

// handleConsoleCodeIndex handles GET /console_code/ — redirects to a
// specific project (defaults to "default").
func (e *Extension) handleConsoleCodeIndex(w http.ResponseWriter, r *http.Request) {
	projects, err := e.projectRepo.ListProjects(r.Context(), false)
	if err != nil || len(projects) == 0 {
		http.Error(w, "no projects found", http.StatusNotFound)
		return
	}
	// Default to the first project.
	http.Redirect(w, r, "/console_code/"+projects[0], http.StatusSeeOther)
}

// handleConsoleCodePage handles GET /console_code/{project} — render the
// console code page. If no valid code exists, generates one and logs it.
func (e *Extension) handleConsoleCodePage(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	if project == "" {
		http.NotFound(w, r)
		return
	}

	// Validate project exists.
	_, err := e.projectRepo.GetProjectMeta(r.Context(), project)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	// Check if there's a valid code; if not, generate one.
	code := e.authorizer.Code()
	hasExpired := e.authorizer.HasExpired()
	if code == "" || hasExpired {
		code = e.authorizer.generateCode()
	}

	// Build redirect URL preserving path prefix.
	redirectURL := "/"

	tmpl := ConsolePageTemplate()
	if err := tmpl.Execute(w, map[string]any{
		"Title":        "Console Code",
		"Page":         "console_code",
		"Project":      project,
		"ConsoleCode":  code,
		"ExpiresIn":    "10m",
		"NoCode":       false,
		"RedirectURL":  redirectURL,
		"Message":      "",
		"MessageClass": "",
	}); err != nil {
		slog.Error("console_code: render page", "error", err)
	}
}

// handleConsoleCode handles GET /api/{project}/console_code — returns the
// current code (if any) and whether it has expired. Used by the "More" menu
// entry to show the code in a modal.
func (e *Extension) handleConsoleCode(w http.ResponseWriter, r *http.Request) {
	code := e.authorizer.Code()
	expired := e.authorizer.HasExpired()

	w.Header().Set("Content-Type", "application/json")
	if code == "" || expired {
		// No valid code — generate one.
		code = e.authorizer.generateCode()
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"code":"%s","has_expired":%s}`, code, boolStr(expired && code != ""))
}

// handleGenerateCode handles POST /api/{project}/console_code/generate —
// forces regeneration of a new code, logs it to stdout.
func (e *Extension) handleGenerateCode(w http.ResponseWriter, r *http.Request) {
	code := e.authorizer.generateCode()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"code":"%s"}`, code)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
