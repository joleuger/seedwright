// Package photobooth provides a fullscreen camera UI for quick
// image capture via the device's front or rear camera.
//
// See EXTENDING.md for the extension contract.
package photobooth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"seedwright/internal/app"
	"seedwright/internal/authz"
	"seedwright/internal/config"
	"seedwright/internal/data"
	"seedwright/internal/data/model"
	"seedwright/internal/extdep"
	"seedwright/internal/server"
	"seedwright/internal/storage"
)

// PostFilterConfig holds the project-level post_filter settings stored in a
// S3 delta file at projects/{project}/ext/joleuger/photobooth/settings.json.
//
// post_filter is optional — if empty, no post-filter pass runs after the
// photo capture.  The prompt is required when post_filter is present
// (the extension validates this before saving).  reference_image is an
// optional element ID that becomes the first entry in the generation's
// reference_images array, so a txt2img prompt like "cartoonify this"
// knows which captured photo to use.
type PostFilterConfig struct {
	// Prompt is the txt2img prompt for the post-filter pass.
	// When set (non-empty), a second generation step runs after the
	// photo is captured.  Example: "Please cartoonify" or "remove background".
	Prompt string `json:"prompt,omitempty"`

	// ReferenceImage is an element ID that gets added to the generation's
	// reference_images array.  It is the second reference image besides
	// the captured photo.  When set, the prompt can reference it:
	// "Please add the person from the first reference image into the second reference image".
	ReferenceImage string `json:"reference_image,omitempty"`
}

// Config holds Photobooth's tunable settings.
type Config struct {
	Enabled     bool             `yaml:"enabled"`
	PostFilter  PostFilterConfig `yaml:"post_filter"`
}

// LoadConfig returns Photobooth's config from the global app config.
func LoadConfig(cfg *config.Config) (Config, error) {
	c := Config{Enabled: true}
	if err := cfg.ExtensionConfig("joleuger/photobooth", &c); err != nil {
		return c, fmt.Errorf("photobooth: config: %w", err)
	}
	return c, nil
}

// Extension holds the Photobooth extension's state and dependencies.
type Extension struct {
	db          *sql.DB
	storage     storage.StorageBackend
	jobService  *data.JobService
	elementRepo data.ElementRepository
	mux         *http.ServeMux
	cfg         Config
	projectRepo data.ProjectRepository
	// deps is the shared extension dependency graph, set in NewExtension
	// (nil for Extensions built via New in tests). It provides the
	// runtime check for the optional printer extension.
	deps *extdep.Graph
	// pathPrefix is the configured server.path_prefix so rendered pages
	// build correct URLs when the app is served under a subpath.
	pathPrefix string
}

// Overlay-settings defaults. The project delta (S3) overrides them when it
// sets the corresponding fields.
const (
	defaultPrintEnabled = true
	defaultKeepOnCancel = true
	defaultMaxPhotos    = 5
	maxPhotosCap        = 10
)

// overlaySettings is the per-project state the capture-overlay print
// controls need.
type overlaySettings struct {
	PrintAvailable bool
	MaxPhotos      int
	KeepOnCancel   bool
	// PrinterURI is the configured printer (CUPS URI) used for printing in
	// the capture preview. In photobooth mode the user is never asked which
	// printer to use — the choice is made once in the project settings.
	PrinterURI string
}

// overlaySettingsFromDelta resolves the overlay settings from a project's
// photobooth delta. printerAvailable reports whether the optional printer
// extension is enabled at runtime; the print controls only render when the
// project setting is on AND the printer extension is present.
//
// max_photos is accepted as a JSON number (float64) or a string — the
// settings page's JS submits all non-checkbox inputs as strings. Values
// outside [1, maxPhotosCap] are clamped.
func overlaySettingsFromDelta(delta model.ProjectSettingsDelta, printerAvailable bool) overlaySettings {
	s := overlaySettings{
		PrintAvailable: printerAvailable,
		MaxPhotos:      defaultMaxPhotos,
		KeepOnCancel:   defaultKeepOnCancel,
	}
	if v, ok := delta.Field("print_enabled").(bool); ok {
		s.PrintAvailable = printerAvailable && v
	}
	switch v := delta.Field("max_photos").(type) {
	case float64:
		s.MaxPhotos = int(v)
	case int:
		s.MaxPhotos = v
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			s.MaxPhotos = n
		}
	}
	if v, ok := delta.Field("keep_on_cancel").(bool); ok {
		s.KeepOnCancel = v
	}
	if v, ok := delta.Field("print_printer").(string); ok {
		s.PrinterURI = v
	}
	if s.MaxPhotos < 1 {
		s.MaxPhotos = 1
	}
	if s.MaxPhotos > maxPhotosCap {
		s.MaxPhotos = maxPhotosCap
	}
	return s
}

// overlaySettingsForProject fetches the project's photobooth delta from S3
// and resolves the overlay settings against the printer extension's
// runtime state. On read errors it falls back to defaults.
func (e *Extension) overlaySettingsForProject(ctx context.Context, project string) overlaySettings {
	printerAvailable := e.deps.IsEnabled("joleuger/printer")
	delta, err := e.projectRepo.GetExtensionSettings(ctx, project, "joleuger", "photobooth")
	if err != nil {
		slog.Warn("photobooth: get extension settings", "project", project, "error", err)
		return overlaySettingsFromDelta(model.ProjectSettingsDelta{}, printerAvailable)
	}
	return overlaySettingsFromDelta(delta, printerAvailable)
}

// SettingsSection returns the photobooth settings section for the project
// settings page. Reads from the S3 delta (ProjectSettingsDelta parameter).
// The SQLite column is only used by the post-filter job path (handleSaveImage).
func (e *Extension) SettingsSection(ctx context.Context, project string, delta model.ProjectSettingsDelta) (*server.Section, error) {
	// Read values from the S3 delta (authoritative source for settings).
	promptVal, _ := delta.Field("post_filter_prompt").(string)
	refImgVal, _ := delta.Field("post_filter_reference_image").(string)
	triggerVal, _ := delta.Field("capture_trigger_binding").(string)

	// Fall back to SQLite for projects where S3 delta hasn't been written yet
	// but SQLite columns exist (migration path).
	if promptVal == "" || refImgVal == "" {
		snap, refImage, err := e.GetPostFilter(ctx, project)
		if err == nil {
			if promptVal == "" && snap != "" {
				promptVal = snap
			}
			if refImgVal == "" && refImage != "" {
				refImgVal = refImage
			}
		}
	}

	// Capture-overlay print settings (S3 delta only; unset = defaults).
	// The printer-availability flag is irrelevant here: the form always
	// offers the print_enabled toggle, and the page combines it with the
	// printer extension's runtime state when rendering the overlay.
	overlay := overlaySettingsFromDelta(delta, true)
	printChk := ""
	if overlay.PrintAvailable {
		printChk = " checked"
	}
	keepChk := ""
	if overlay.KeepOnCancel {
		keepChk = " checked"
	}

	// Configured printer (CUPS URI). Server-rendered as a selected option so
	// the value survives even if the printer list cannot be loaded (e.g. the
	// printer extension is disabled); the section script replaces the list
	// with live printer options when the API responds.
	printerOptions := `<option value="">— not configured —</option>`
	if overlay.PrinterURI != "" {
		esc := template.HTMLEscapeString(overlay.PrinterURI)
		printerOptions += fmt.Sprintf(`<option value="%s" selected>%s</option>`, esc, esc)
	}
	// Prefix-aware API path embedded server-side: the section script runs
	// before the settings page's own url() helper is defined. Project names
	// are validated against ^[a-zA-Z0-9][a-zA-Z0-9._-]*$, so embedding is safe.
	// ?configured=true: the settings page selects the photobooth printer
	// from configured printers only (no lpstat discovery).
	printersAPI := e.pathPrefix + "/api/" + project + "/ext/joleuger/printer/printers?configured=true"

	return &server.Section{
		ID:    "joleuger/photobooth",
		Label: "Photobooth",
		HTML: template.HTML(fmt.Sprintf(`
			<div class="form-group">
				<label for="settingsPBPostFilter">Post-filter (optional txt2img pass)</label>
				<input type="text" id="settingsPBPostFilter"
					   data-section="joleuger/photobooth" data-field="post_filter_prompt"
					   placeholder="e.g. Please cartoonify"
					   value="%s"
					   style="width:100%%;background:#0d1b36;border:1px solid #0f3460;color:#e0e0e0;padding:0.5rem 0.75rem;border-radius:6px;font-size:0.9rem;box-sizing:border-box">
				<p class="help-text">
					When set, a txt2img pass runs after the photo is captured.  Example:
					<code>cartoonify</code>, <code>remove background</code>.
				</p>
			</div>
			<div class="form-group">
				<label for="settingsPBRefImage">Reference image element ID (optional)</label>
				<input type="text" id="settingsPBRefImage"
					   data-section="joleuger/photobooth" data-field="post_filter_reference_image"
					   placeholder="element-id"
					   value="%s"
					   style="width:100%%;background:#0d1b36;border:1px solid #0f3460;color:#e0e0e0;padding:0.5rem 0.75rem;border-radius:6px;font-size:0.9rem;box-sizing:border-box">
				<p class="help-text">
					Optional second reference image element ID.  The captured photo
					always serves as the first reference image.
				</p>
			</div>
			<div class="form-group">
				<label>Capture trigger key</label>
				<div style="display:flex;gap:0.5rem;align-items:center;margin-top:0.3rem;">
					<button type="button" id="pbBindKey"
							style="background:#0d1b36;border:1px solid #0f3460;color:#e0e0e0;padding:0.4rem 0.75rem;border-radius:6px;font-size:0.85rem;cursor:pointer;">Bind key…</button>
					<button type="button" id="pbClearKey"
							style="background:#0d1b36;border:1px solid #0f3460;color:#e0e0e0;padding:0.4rem 0.75rem;border-radius:6px;font-size:0.85rem;cursor:pointer;" title="Clear binding">✕</button>
					<span id="pbTriggerLabel" style="color:#a0a0b0;font-size:0.9rem;">— none —</span>
				</div>
				<p class="help-text">
					Optional Bluetooth remote key that triggers capture.  Press <strong>Bind key…</strong> then press the key.  The binding is saved locally and synced when you click Save.
				</p>
				<input type="hidden" id="pbCaptureTriggerCode"
					   data-section="joleuger/photobooth" data-field="capture_trigger_binding"
					   value="%s">
			</div>
			<div class="form-group">
				<label class="switch-label">
					<span class="switch">
						<input type="checkbox" id="settingsPBPrintEnabled" data-section="joleuger/photobooth" data-field="print_enabled"%s>
						<span class="slider"></span>
					</span>
					<span class="switch-label">Print in capture preview</span>
				</label>
				<p class="help-text">
					When enabled, the capture preview shows a copy-count selector and a print
					button (requires the printer extension to be enabled).  When disabled,
					the preview shows Retake and Keep instead.
				</p>
			</div>
			<div class="form-group">
				<label for="settingsPBPrinter">Printer</label>
				<select id="settingsPBPrinter" data-section="joleuger/photobooth" data-field="print_printer">
					%s
				</select>
				<p class="help-text" id="settingsPBPrinterHint">
					The printer the capture preview prints to.  In photobooth mode the
					user is never asked which printer to use — the choice is made here.
					The list is loaded from the printer extension.
				</p>
			</div>
			<div class="form-group">
				<label for="settingsPBMaxPhotos">Max copies</label>
				<input type="number" id="settingsPBMaxPhotos" min="1" max="10"
					   data-section="joleuger/photobooth" data-field="max_photos"
					   value="%d"
					   style="width:120px;background:#0d1b36;border:1px solid #0f3460;color:#e0e0e0;padding:0.5rem 0.75rem;border-radius:6px;font-size:0.9rem;box-sizing:border-box">
				<p class="help-text">
					Upper bound of the copy-count buttons in the capture preview (1–10).
				</p>
			</div>
			<div class="form-group">
				<label class="switch-label">
					<span class="switch">
						<input type="checkbox" id="settingsPBKeepOnCancel" data-section="joleuger/photobooth" data-field="keep_on_cancel"%s>
						<span class="slider"></span>
					</span>
					<span class="switch-label">Keep photo when closing the preview</span>
				</label>
				<p class="help-text">
					When enabled, closing the preview with the ✕ button saves the photo as an
					element.  Retake always discards the shot.
				</p>
			</div>
			<script>
			(function() {
				const STORAGE_KEY = 'photobooth.captureKeyCode';
				var boundKeyLabel = document.getElementById('pbTriggerLabel');
				var bindKeyBtn = document.getElementById('pbBindKey');
				var clearKeyBtn = document.getElementById('pbClearKey');
				var hiddenInput = document.getElementById('pbCaptureTriggerCode');

				var MODIFIER_CODES = new Set([
					'ShiftLeft','ShiftRight','ControlLeft','ControlRight',
					'AltLeft','AltRight','MetaLeft','MetaRight',
				]);

				function getBoundKeyCode() {
					return localStorage.getItem(STORAGE_KEY);
				}

				function setBoundKeyCode(code) {
					if (code) {
						localStorage.setItem(STORAGE_KEY, code);
					} else {
						localStorage.removeItem(STORAGE_KEY);
					}
					var map = {
						'Space':'Space','Enter':'Enter',
						'ArrowLeft':'←','ArrowRight':'→','ArrowUp':'↑','ArrowDown':'↓',
						'PageUp':'Page Up','PageDown':'Page Down',
					};
					if (map[code]) {
						boundKeyLabel.textContent = map[code];
					} else if (code && code.startsWith('Key')) {
						boundKeyLabel.textContent = code.slice(3);
					} else if (code && code.startsWith('Digit')) {
						boundKeyLabel.textContent = code.slice(5);
					} else {
						boundKeyLabel.textContent = code || '— none —';
					}
					hiddenInput.value = code || '';
					// Trigger the settings page's change tracking
					hiddenInput.dispatchEvent(new Event('input', {bubbles:true}));
				}

				function startBinding() {
					bindKeyBtn.textContent = 'Press a key…';
					bindKeyBtn.disabled = true;

					function onKeyDown(e) {
						if (MODIFIER_CODES.has(e.code)) return;
						e.preventDefault();
						e.stopPropagation();
						window.removeEventListener('keydown', onKeyDown, true);
						setBoundKeyCode(e.code);
						bindKeyBtn.textContent = 'Bind key…';
						bindKeyBtn.disabled = false;
					}

					window.addEventListener('keydown', onKeyDown, true);
				}

				bindKeyBtn.addEventListener('click', startBinding);
				clearKeyBtn.addEventListener('click', function() {
					setBoundKeyCode(null);
				});

				// Restore from localStorage or hidden input on page load
				var saved = getBoundKeyCode();
				if (!saved && hiddenInput.value) {
					setBoundKeyCode(hiddenInput.value);
				}
			})();

			// Printer select: load the printer list from the printer
			// extension's API (same origin). The saved value is
			// server-rendered as the selected option, so it survives even
			// when the list cannot be loaded (e.g. printer extension
			// disabled) — the setting is never silently wiped on save.
			(function() {
				var sel = document.getElementById('settingsPBPrinter');
				if (!sel) return;
				var hint = document.getElementById('settingsPBPrinterHint');
				fetch('%s', { headers: { 'Accept': 'application/json' } })
					.then(function (r) { return r.ok ? r.json() : null; })
					.then(function (data) {
						if (!data || !Array.isArray(data.printers)) {
							if (hint) {
								hint.textContent = 'Printer extension is not enabled — printing in the capture preview is unavailable.';
							}
							return;
						}
						var saved = sel.value;
						sel.innerHTML = '';
						var none = document.createElement('option');
						none.value = '';
						none.textContent = '— not configured —';
						sel.appendChild(none);
						data.printers.forEach(function (p) {
							var opt = document.createElement('option');
							opt.value = p.uri;
							opt.textContent = p.name + (p.status && p.status !== 'unknown' ? ' (' + p.status + ')' : '');
							sel.appendChild(opt);
						});
						if (saved) {
							var found = false;
							for (var i = 0; i < sel.options.length; i++) {
								if (sel.options[i].value === saved) { found = true; break; }
							}
							if (!found) {
								var opt = document.createElement('option');
								opt.value = saved;
								opt.textContent = saved + ' (unavailable)';
								sel.appendChild(opt);
							}
							sel.value = saved;
						}
					})
					.catch(function () {
						if (hint) {
							hint.textContent = 'Could not load the printer list — the saved printer is kept as-is.';
						}
					});
			})();
			</script>
		`, promptVal, refImgVal, triggerVal, printChk, printerOptions, overlay.MaxPhotos, keepChk, printersAPI)),
		Fields: []server.FieldInfo{
			{Key: "post_filter_prompt", Type: "text"},
			{Key: "post_filter_reference_image", Type: "text"},
			{Key: "capture_trigger_binding", Type: "text"},
			{Key: "print_enabled", Type: "checkbox"},
			{Key: "print_printer", Type: "text"},
			{Key: "max_photos", Type: "number"},
			{Key: "keep_on_cancel", Type: "checkbox"},
		},
	}, nil
}

// New constructs a new Photobooth extension.
func New(db *sql.DB, storage storage.StorageBackend, jobService *data.JobService, elementRepo data.ElementRepository, mux *http.ServeMux, cfg Config) *Extension {
	return &Extension{
		db:          db,
		storage:     storage,
		jobService:  jobService,
		elementRepo: elementRepo,
		mux:         mux,
		cfg:         cfg,
	}
}

// NewExtension constructs a Photobooth extension from an App instance.
// This is the entrypoint called from ext.RegisterAll.
func NewExtension(ctx context.Context, a *app.App) (*Extension, error) {
	cfg, err := LoadConfig(a.Config)
	if err != nil {
		return nil, err
	}
	p := New(a.DB, a.Storage, a.JobService, a.Elements, a.GetServeMux(), cfg)
	p.projectRepo = a.Projects
	p.deps = a.ExtDeps
	p.pathPrefix = a.Config.Server.PathPrefix
	p.RegisterHooks(a)
	p.RegisterRoutes(a)
	return p, nil
}

// RegisterHooks appends Photobooth's hooks to the app's hook slices.
func (e *Extension) RegisterHooks(a *app.App) {
	if a.Hooks != nil {
		// MoreNavItems: render the Photobooth link in the "More" dropdown.
		a.Hooks.MoreNavItems = append(a.Hooks.MoreNavItems, e.MoreNavItems)
		// SettingsSection: render post_filter settings in the project settings page.
		a.Hooks.SettingsSection = append(a.Hooks.SettingsSection, e.SettingsSection)
		// SettingsSavers: the extension owns the validation and persistence
		// of its own project settings (the core endpoint dispatches here).
		if a.Hooks.SettingsSavers == nil {
			a.Hooks.SettingsSavers = map[string]server.SettingsSaver{}
		}
		a.Hooks.SettingsSavers["joleuger/photobooth"] = e.saveProjectSettings
	}
}

// Sync delegates to post_filter sync (populates SQLite from S3 delta).
func Sync(ctx context.Context, a *app.App) error {
	return extensionSync(ctx, a)
}

// extensionSync lists projects from SQLite and syncs post_filter from S3.
func extensionSync(ctx context.Context, a *app.App) error {
	rows, err := a.DB.QueryContext(ctx, `SELECT name FROM projects`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var projects []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		projects = append(projects, name)
	}

	for _, project := range projects {
		if err := syncPostFilterFromS3(ctx, a.Storage, a.DB, project); err != nil {
			slog.Warn("photobooth: sync failed", "project", project, "error", err)
		}
	}

	return nil
}

// --- Hooks ---

// MoreNavItems renders the Photobooth link for the "More" dropdown menu.
func (e *Extension) MoreNavItems(ctx context.Context, project string) (template.HTML, error) {
	return template.HTML(`<a href="/photobooth/` + project + `/" style="display:block;padding:0.4rem 1rem;color:#a0a0b0;text-decoration:none;font-size:0.85rem;border-radius:0">📷 Photobooth</a>`), nil
}

// RegisterRoutes registers Photobooth's HTTP routes on the server mux.
// All Photobooth routes are marked Public() — they bypass authorization.
func (e *Extension) RegisterRoutes(a *app.App) {
	e.mux.Handle("GET /photobooth/", authz.Public()(http.HandlerFunc(e.handlePhotoboothIndex)))
	e.mux.Handle("GET /photobooth/{project}", authz.Public()(http.HandlerFunc(e.handlePhotoboothPage)))
	e.mux.Handle("GET /photobooth/{project}/", authz.Public()(http.HandlerFunc(e.handlePhotoboothTrailingSlash)))
	e.mux.Handle("POST /api/{project}/ext/joleuger/photobooth/save", authz.Public()(http.HandlerFunc(e.handleSaveImage)))
	slog.Debug("photobooth: registered routes", "count", 4,
		"routes", []string{
			"GET /photobooth/",
			"GET /photobooth/{project}",
			"GET /photobooth/{project}/",
			"POST /api/{project}/ext/joleuger/photobooth/save",
		})
}

// handlePhotoboothTrailingSlash handles GET /photobooth/{project}/ (trailing slash) —
// redirects to /photobooth/{project} (no trailing slash), matching the pattern used
// by /basic/{project}/ → /basic/{project} for the core project pages.
func (e *Extension) handlePhotoboothTrailingSlash(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	target := "/photobooth/" + project
	if q := r.URL.RawQuery; q != "" {
		target += "?" + q
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

// handlePhotoboothIndex handles GET /photobooth/ — project selection start page.
func (e *Extension) handlePhotoboothIndex(w http.ResponseWriter, r *http.Request) {
	projects, err := e.projectRepo.ListProjects(r.Context(), true)
	if err != nil {
		slog.Error("photobooth: list projects", "error", err)
		http.Error(w, "failed to load projects", http.StatusInternalServerError)
		return
	}

	tmpl := PhotoboothIndexTemplate()
	if err := tmpl.Execute(w, map[string]any{
		"Title":    "Photobooth",
		"Projects": projects,
		"prefix":   e.pathPrefix,
	}); err != nil {
		slog.Error("photobooth: render index", "error", err)
	}
}

// handlePhotoboothPage handles GET /photobooth/{project}.
func (e *Extension) handlePhotoboothPage(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	// Validate project exists.
	_, err := e.projectRepo.GetProjectMeta(r.Context(), project)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	overlay := e.overlaySettingsForProject(r.Context(), project)

	tmpl := PhotoboothTemplate()
	if err := tmpl.Execute(w, map[string]any{
		"Title":          "Photobooth",
		"Project":        project,
		"prefix":         e.pathPrefix,
		"PrintAvailable": overlay.PrintAvailable,
		"MaxPhotos":      overlay.MaxPhotos,
		"KeepOnCancel":   overlay.KeepOnCancel,
		"PrinterURI":     overlay.PrinterURI,
	}); err != nil {
		slog.Error("photobooth: render", "error", err)
	}
}

// handleSaveImage handles POST /api/{project}/ext/joleuger/photobooth/save.
// Receives a base64-encoded image, saves it to S3, creates an element record,
// and redirects to the element detail page.
func (e *Extension) handleSaveImage(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	// Validate project exists.
	_, err := e.projectRepo.GetProjectMeta(r.Context(), project)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "project not found"})
		return
	}

	// Parse JSON body: {"image": "data:image/png;base64,..."}
	var body struct {
		Image string `json:"image"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}

	if body.Image == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "no image data"})
		return
	}

	// Extract base64 data from data URI if present.
	dataStr := body.Image
	if idx := strings.Index(dataStr, ","); idx != -1 {
		dataStr = dataStr[idx+1:]
	}

	// Decode base64.
	imgData, err := base64.StdEncoding.DecodeString(dataStr)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid base64"})
		return
	}

	if len(imgData) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "empty image"})
		return
	}

	// Create element record with generated params.
	now := time.Now().UTC()
	elem := model.NewImageElement(project, "Photobooth capture", 512, 512, 1, 1.0, -1, "unknown", "", "", "", "photobooth")
	if g := elem.Generation; g != nil {
		g.Prompt = "Photobooth capture"
		g.Width = 512
		g.Height = 512
		g.SampleSteps = 1
		g.TxtCfg = 1.0
		g.Seed = -1
	}
	elem.ID = fmt.Sprintf("photobooth_%x", now.UnixNano())

	// Write image to S3.
	elem.Image = &model.ImageInfo{
		ProjectLocation: fmt.Sprintf("elements/%s.png", elem.ID),
		Format:          "png",
		Width:           512,
		Height:          512,
		SizeBytes:       int64(len(imgData)),
	}

	// Create element in DB and S3 directly (no sdcpp job needed).
	if err := e.elementRepo.CreateElement(r.Context(), elem, io.NopCloser(bytes.NewReader(imgData)), int64(len(imgData))); err != nil {
		slog.Error("photobooth: create element", "project", project, "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to save: " + err.Error()})
		return
	}

	elemID := elem.ID

	// Read post_filter settings from the SQLite column (populated by Sync
	// at startup from the S3 delta).
	postFilterPrompt, optionalRefElemID, _ := e.GetPostFilter(r.Context(), project)

	// If post_filter is configured, start a txt2img job using the captured
	// photo as a reference image.  The captured photo is saved as a
	// standalone element first; the post-filter job creates a second element
	// that references it.
	if postFilterPrompt != "" {
		e.startPostFilterJob(r.Context(), project, elemID, postFilterPrompt, optionalRefElemID)
	}

	// If it's an AJAX request, return JSON with element ID.
	if r.Header.Get("Accept") == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"element_id": elemID})
		return
	}

	// Otherwise redirect to element detail.
	http.Redirect(w, r, "/basic/"+project+"/element/"+elemID, http.StatusSeeOther)
}

// startPostFilterJob creates a txt2img generation job that uses the captured
// photo as a reference image.  It runs asynchronously so the user can
// immediately see the captured photo.
func (e *Extension) startPostFilterJob(ctx context.Context, project, capturedElemID, prompt, optionalRefElemID string) {
	if e.jobService == nil {
		return
	}

	// Build reference_images: captured photo is always first,
	// optional reference image is second (if configured and valid).
	var refs []model.ElementRef
	refs = append(refs, model.ElementRef{ElementID: capturedElemID})
	if optionalRefElemID != "" {
		refs = append(refs, model.ElementRef{ElementID: optionalRefElemID})
	}

	// Create element for the post-filter generation.
	postElem := model.NewImageElement(project, prompt, 512, 512, 20, 7.0, -1, "unknown", "", "", "", "photobooth")
	postElem.Origin = "photobooth/post_filter"
	if g := postElem.Generation; g != nil {
		g.ReferenceImages = refs
	}

	// Start the job.  Errors are logged but not propagated — the captured
	// photo is already saved successfully.
	if _, err := e.jobService.StartJob(ctx, postElem); err != nil {
		slog.Warn("photobooth: post_filter job failed", "project", project, "captured_elem", capturedElemID, "error", err)
	}
}

// --- S3 helpers ---

// deletePrefix deletes all objects under the given S3 prefix.
func deletePrefix(ctx context.Context, s storage.StorageBackend, prefix string) error {
	objects, err := s.ListObjects(ctx, prefix)
	if err != nil {
		return err
	}
	for _, obj := range objects {
		if err := s.DeleteObject(ctx, obj.Key); err != nil {
			slog.Warn("photobooth: delete prefix object", "key", obj.Key, "error", err)
		}
	}
	return nil
}

// HandleProjectDeleted implements the OnProjectDeleted hook.
func (e *Extension) HandleProjectDeleted(ctx context.Context, project string) error {
	prefix := fmt.Sprintf("projects/%s/elements/photobooth_", project)
	return deletePrefix(ctx, e.storage, prefix)
}

// HandleJobTerminal implements the OnJobTerminal hook (no-op for photobooth).
func (e *Extension) HandleJobTerminal(ctx context.Context, elem *model.Element, job data.JobRecord) error {
	return nil
}
