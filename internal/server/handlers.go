package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"seedwright/internal/data"
	"seedwright/internal/data/model"
)

// validProjectName matches allowed project name characters.
var validProjectName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// settingsSaver looks up the settings saver registered by an extension for
// the given "owner/extension" section ID. Nil-safe: configs without hooks
// (e.g. minimal test setups) simply have no savers.
func settingsSaver(cfg *Config, sectionID string) (SettingsSaver, bool) {
	if cfg == nil || cfg.Hooks == nil {
		return nil, false
	}
	saver, ok := cfg.Hooks.SettingsSavers[sectionID]
	return saver, ok
}

// ---- Handlers ----

// redirectToAppRoot handles GET / — redirects to the app root under /basic/.
func (h *handler) redirectToAppRoot(w http.ResponseWriter, r *http.Request) {
	target := "/basic/"
	if h.cfg.PathPrefix != "" {
		target = h.cfg.PathPrefix + target
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

// redirectBasicToTrailingSlash handles GET /basic — redirects to /basic/
// so the trailing-slash handler serves the welcome page with project links.
func (h *handler) redirectBasicToTrailingSlash(w http.ResponseWriter, r *http.Request) {
	target := "/basic/"
	if h.cfg.PathPrefix != "" {
		target = h.cfg.PathPrefix + target
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

// welcome handles GET /basic/
func (h *handler) welcome(w http.ResponseWriter, r *http.Request) {
	projects, err := h.cfg.ProjectRepo.ListProjects(r.Context(), true)
	if err != nil {
		slog.Error("list projects", "error", err)
		h.renderError(w, r, "failed to load projects")
		return
	}

	// Collect SettingsFields from extensions.
	var settingsFields []map[string]any
	if h.cfg.Hooks != nil && h.cfg.Hooks.SettingsField != nil {
		settingsFields = make([]map[string]any, 0)
		for _, fn := range h.cfg.Hooks.SettingsField {
			fieldHTML, err := fn(r.Context(), "")
			if err != nil {
				slog.Warn("settings field hook", "error", err)
				continue
			}
			if fieldHTML != "" {
				var extKey string
				if attr := extractAttr(string(fieldHTML), "data-extension"); attr != "" {
					extKey = attr
				}
				settingsFields = append(settingsFields, map[string]any{
					"HTML":      fieldHTML,
					"Extension": extKey,
				})
			}
		}
	}

	// Collect WelcomeExtras from extensions (e.g. the onboarding link).
	var welcomeExtras template.HTML
	if h.cfg.Hooks != nil {
		for _, fn := range h.cfg.Hooks.WelcomeExtras {
			items, err := fn(r.Context())
			if err != nil {
				slog.Warn("welcome extras hook", "error", err)
				continue
			}
			welcomeExtras += items
		}
	}

	renderData := map[string]any{
		"Title":           h.cfg.Title,
		"Page":            "welcome",
		"Projects":        projects,
		"BackendNames":    h.cfg.BackendNames,
		"DefaultBackend":  h.cfg.DefaultBackend,
		"SettingsFields":  settingsFields,
		"WelcomeExtras":   welcomeExtras,
	}

	h.render(w, "welcome", renderData)
}

// createProjectInternal creates a project given its name. Shared by createProject and createProjectFromWelcome.
func (h *handler) createProjectInternal(w http.ResponseWriter, r *http.Request, project string) {
	// Validate project name.
	if !validProjectName.MatchString(project) {
		http.Error(w, "invalid project name", http.StatusBadRequest)
		return
	}

	pm := model.NewProject(project)
	if err := h.cfg.ProjectRepo.CreateProject(r.Context(), pm); err != nil {
		slog.Error("create project", "project", project, "error", err)
		http.Error(w, "failed to create project", http.StatusInternalServerError)
		return
	}

	target := "/basic/" + project
	if h.cfg.PathPrefix != "" {
		target = h.cfg.PathPrefix + target
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// createProjectFromWelcome handles POST /create-project — creates a new project from the welcome page (JSON body).
func (h *handler) createProjectFromWelcome(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Name         string `json:"name"`
		FriendlyName string `json:"friendly_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	pm := model.NewProject(body.Name)
	pm.FriendlyName = body.FriendlyName
	if err := h.cfg.ProjectRepo.CreateProject(r.Context(), pm); err != nil {
		slog.Error("create project", "project", body.Name, "error", err)
		http.Error(w, "failed to create project", http.StatusInternalServerError)
		return
	}

	target := "/basic/" + body.Name
	if h.cfg.PathPrefix != "" {
		target = h.cfg.PathPrefix + target
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// projectPageTrailingSlash handles GET /basic/{project}/ (trailing slash) — redirects to /basic/{project}
func (h *handler) projectPageTrailingSlash(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	target := "/basic/" + project
	if q := r.URL.RawQuery; q != "" {
		target += "?" + q
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

// projectPage handles GET /{project}
func (h *handler) projectPage(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	// Validate project name — reject paths that look like file requests (favicon.ico, etc.)
	if !validProjectName.MatchString(project) {
		h.renderError(w, r, fmt.Sprintf("invalid project name: %s", project))
		return
	}

	// Check if project exists (don't auto-create).
	projMeta, err := h.cfg.ProjectRepo.GetProjectMeta(r.Context(), project)
	if err != nil {
		// Project doesn't exist — show error with create option.
		renderData := map[string]any{
			"Title":      h.cfg.Title,
			"Project":    project,
			"Page":       "dashboard",
			"Error":      fmt.Sprintf("Project '%s' does not exist.", project),
			"ShowCreate": true,
			"Width":      512,
			"Height":     512,
			"Steps":      20,
			"Cfg":        7.0,
			"Seed":       int64(-1),
		}
		h.render(w, "project", renderData)
		return
	}

	// Ensure project row exists (defensive).
	if err := h.cfg.ProjectRepo.CreateProject(r.Context(), model.NewProject(project)); err != nil {
		slog.Warn("create project (may already exist)", "project", project, "error", err)
	}

	// Get recent elements (last 8)
	recent, _, err := h.cfg.ElementRepo.ListElements(r.Context(), project, data.ListOptions{
		Page:    1,
		PerPage: 8,
		Sort:    "created_at",
		Order:   "desc",
	})
	if err != nil {
		slog.Warn("list recent", "error", err)
	}

	// Get active jobs for this project.
	var activeJobs []data.JobInfoJSON
	if h.cfg.JobService != nil {
		records, _ := h.cfg.JobService.ListActiveJobs(r.Context(), project)
		encoded := make([]data.JobInfoJSON, len(records))
		for i, j := range records {
			encoded[i] = j.ToJSON()
		}
		activeJobs = encoded
	}

	// Determine the effective backend URL for the project.
	var backendURL string
	if projMeta.BackendRef != "" && h.cfg.JobService != nil && h.cfg.JobService.BackendResolver != nil {
		if url, err := h.cfg.JobService.BackendResolver(projMeta.BackendRef); err == nil {
			backendURL = url
		}
	} else if h.cfg.JobService != nil {
		backendURL = h.cfg.JobService.SDCPPBase
	}

	// Collect NavBarItems from extensions.
	var navItems template.HTML
	if h.cfg.Hooks != nil {
		for _, fn := range h.cfg.Hooks.NavBarItems {
			items, err := fn(r.Context(), project)
			if err != nil {
				slog.Warn("nav bar items hook", "project", project, "error", err)
				continue
			}
			navItems += items
		}
	}

	// Collect MoreNavItems from extensions.
	var moreNavItems template.HTML
	if h.cfg.Hooks != nil {
		for _, fn := range h.cfg.Hooks.MoreNavItems {
			items, err := fn(r.Context(), project)
			if err != nil {
				slog.Warn("more nav items hook", "project", project, "error", err)
				continue
			}
			moreNavItems += items
		}
	}

	// Collect SettingsFields from extensions.
	var settingsFields []map[string]any
	if h.cfg.Hooks != nil && h.cfg.Hooks.SettingsField != nil {
		settingsFields = make([]map[string]any, 0)
		for _, fn := range h.cfg.Hooks.SettingsField {
			fieldHTML, err := fn(r.Context(), project)
			if err != nil {
				slog.Warn("settings field hook", "error", err)
				continue
			}
			if fieldHTML != "" {
				// Extract extension key from field HTML.
				var extKey string
				if attr := extractAttr(string(fieldHTML), "data-extension"); attr != "" {
					extKey = attr
				}
				settingsFields = append(settingsFields, map[string]any{
					"HTML":       fieldHTML,
					"Extension":  extKey,
				})
			}
		}
	}

	renderData := map[string]any{
		"Title":           h.cfg.Title,
		"Project":         project,
		"Page":            "dashboard",
		"NavItems":        navItems,
		"MoreNavItems":    moreNavItems,
		"SettingsFields":  settingsFields,
		"Width":           512,
		"Height":          512,
		"Steps":           20,
		"Cfg":             7.0,
		"Seed":            int64(-1),
		"RecentElements":  recent,
		"ActiveJobs":      activeJobs,
		"PromptHelp":      h.promptHelp,
		"BackendNames":    h.cfg.BackendNames,
		"DefaultBackend":  h.cfg.DefaultBackend,
		"ProjectHidden":   projMeta.Hidden,
		"ProjectBackend":  projMeta.BackendRef,
		"BackendURL":      backendURL,
	}

	h.render(w, "project", renderData)
}

// generate handles POST /{project}/generate
func (h *handler) generate(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	// Parse form
	r.ParseForm()
	prompt := r.FormValue("prompt")
	negativePrompt := r.FormValue("negative_prompt")

	width, _ := strconv.Atoi(r.FormValue("width"))
	height, _ := strconv.Atoi(r.FormValue("height"))
	steps, _ := strconv.Atoi(r.FormValue("steps"))
	cfg, _ := strconv.ParseFloat(r.FormValue("cfg"), 64)
	seed, _ := strconv.ParseInt(r.FormValue("seed"), 10, 64)

	// Parse reference IDs from JSON array in form value.
	var refIDs []string
	if rid := r.FormValue("reference_ids"); rid != "" {
		_ = json.Unmarshal([]byte(rid), &refIDs)
	}

	if width == 0 { width = 512 }
	if height == 0 { height = 512 }
	if steps == 0 { steps = 20 }
	if cfg == 0 { cfg = 7.0 }
	if seed == 0 { seed = -1 }

	if prompt == "" {
		if r.Header.Get("Accept") == "application/json" {
			http.Error(w, `{"error":"prompt is required"}`, http.StatusBadRequest)
		} else {
			h.renderError(w, r, "prompt is required")
		}
		return
	}

	// Log project row existence before creating job.
	_, err := h.cfg.ProjectRepo.GetProjectMeta(r.Context(), project)
	if err != nil {
		slog.Warn("generate: project row missing before CreateProject", "project", project, "error", err)
	} else {
		slog.Info("generate: project row exists", "project", project)
	}

	// Ensure project row exists (defensive — jobs table has FK to projects).
	if err := h.cfg.ProjectRepo.CreateProject(r.Context(), model.NewProject(project)); err != nil {
		slog.Warn("create project (may already exist)", "project", project, "error", err)
	}

	// Verify project row exists after CreateProject.
	_, err = h.cfg.ProjectRepo.GetProjectMeta(r.Context(), project)
	if err != nil {
		slog.Error("generate: project row missing AFTER CreateProject — FK constraint will fail", "project", project, "error", err)
	} else {
		slog.Info("generate: project row exists after CreateProject", "project", project)
	}

	// Look up the project's backend architecture.
	arch := ""
	if projMeta, err := h.cfg.ProjectRepo.GetProjectMeta(r.Context(), project); err == nil && h.cfg.BackendArchitecture != nil {
		arch = h.cfg.BackendArchitecture(projMeta.BackendRef)
	}
	// Create element record.
	elem := model.NewImageElement(project, prompt, width, height, steps, cfg, seed, arch, "", "", "", "unknown")
	if g := elem.Generation; g != nil {
		g.NegativePrompt = negativePrompt
	}

	// Set reference images from form.
	if len(refIDs) > 0 {
		refs := make([]model.ElementRef, len(refIDs))
		for i, id := range refIDs {
			refs[i] = model.ElementRef{ElementID: id}
		}
		if g := elem.Generation; g != nil {
			g.ReferenceImages = refs
		}
	}

	// Set the project's selected backend for generation.
	if projMeta, err := h.cfg.ProjectRepo.GetProjectMeta(r.Context(), project); err == nil {
		if g := elem.Generation; g != nil {
			g.BackendRef = projMeta.BackendRef
		}
	}

	// Start job via JobService (creates element, submits to sdcpp, starts poller).
	if h.cfg.JobService == nil {
		http.Error(w, "job service not available", http.StatusInternalServerError)
		return
	}

	elemID, err := h.cfg.JobService.StartJob(r.Context(), elem)
	if err != nil {
		slog.Error("start job", "error", err)
		h.renderError(w, r, "failed to start job: "+err.Error())
		return
	}

	// Redirect to project page with element ID for status tracking.
	http.Redirect(w, r, fmt.Sprintf("/basic/%s?job=%s", project, url.QueryEscape(elemID)), http.StatusSeeOther)
}

// gallery handles GET /{project}/gallery
func (h *handler) gallery(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	// Parse query params via the shared parser (list_options.go) —
	// same filter semantics as the elements API.
	opts := parseListOptions(r.URL.Query(), 24)

	elements, total, err := h.cfg.ElementRepo.ListElements(r.Context(), project, opts)
	if err != nil {
		slog.Error("list elements", "error", err)
		h.renderError(w, r, "failed to load gallery")
		return
	}

	// Collect NavBarItems from extensions.
	var navItems template.HTML
	if h.cfg.Hooks != nil {
		for _, fn := range h.cfg.Hooks.NavBarItems {
			items, err := fn(r.Context(), project)
			if err != nil {
				slog.Warn("nav bar items hook", "project", project, "error", err)
				continue
			}
			navItems += items
		}
	}

	// Collect MoreNavItems from extensions.
	var moreNavItems template.HTML
	if h.cfg.Hooks != nil {
		for _, fn := range h.cfg.Hooks.MoreNavItems {
			items, err := fn(r.Context(), project)
			if err != nil {
				slog.Warn("more nav items hook", "project", project, "error", err)
				continue
			}
			moreNavItems += items
		}
	}

	renderData := map[string]any{
		"Title":           h.cfg.Title,
		"Project":         project,
		"Page":            "gallery",
		"Gallery":         elements,
		"Total":           total,
		"PageNum":         opts.Page,
		"TotalPages":      opts.TotalPages(total),
		"PrevPage":        opts.Page - 1,
		"NextPage":        opts.Page + 1,
		"PerPage":         opts.PerPage,
		"PerPageCurrent":  perPageLabel(opts.PerPage),
		"FavoritesActive": opts.Filters["favorites"] == "1",
		"NavItems":        navItems,
		"MoreNavItems":    moreNavItems,
	}

	h.render(w, "gallery", renderData)
}

// elementsJSON handles GET /api/{project}/elements — the generic element
// list API. It is the gallery's JSON twin: same filter semantics (shared
// parser, extension filters via the query builder registry), JSON response.
// Client-side element lists (slideshow queue, reference image picker, …)
// fetch here instead of parsing HTML or registering their own endpoints.
func (h *handler) elementsJSON(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	opts := parseListOptions(r.URL.Query(), 50)
	elements, total, err := h.cfg.ElementRepo.ListElements(r.Context(), project, opts)
	if err != nil {
		slog.Error("list elements", "error", err)
		http.Error(w, "failed to list elements", http.StatusInternalServerError)
		return
	}
	if elements == nil {
		elements = []model.Element{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"elements":    elements,
		"total":       total,
		"page":        opts.Page,
		"per_page":    opts.PerPage,
		"total_pages": opts.TotalPages(total),
	})
}

// elementPage handles GET /{project}/element/{id}
func (h *handler) elementPage(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	id := r.PathValue("id")

	elem, err := h.cfg.ElementRepo.GetElement(r.Context(), id)
	if err != nil {
		slog.Warn("get element", "id", id, "error", err)
	}

	elemJSON, _ := json.Marshal(elem)

	// Collect NavBarItems from extensions.
	var navItems template.HTML
	if h.cfg.Hooks != nil {
		for _, fn := range h.cfg.Hooks.NavBarItems {
			items, err := fn(r.Context(), project)
			if err != nil {
				slog.Warn("nav bar items hook", "project", project, "error", err)
				continue
			}
			navItems += items
		}
	}

	// Collect MoreNavItems from extensions.
	var moreNavItems template.HTML
	if h.cfg.Hooks != nil {
		for _, fn := range h.cfg.Hooks.MoreNavItems {
			items, err := fn(r.Context(), project)
			if err != nil {
				slog.Warn("more nav items hook", "project", project, "error", err)
				continue
			}
			moreNavItems += items
		}
	}

	// Collect ElementActions from extensions.
	var elementActions template.HTML
	if h.cfg.Hooks != nil {
		for _, fn := range h.cfg.Hooks.ElementActions {
			actions, err := fn(r.Context(), project, id)
			if err != nil {
				slog.Warn("element actions hook", "element", id, "error", err)
				continue
			}
			elementActions += actions
		}
	}

	renderData := map[string]any{
		"Title":          h.cfg.Title,
		"Project":        project,
		"Page":           "element",
		"Element":        elem,
		"ElementJSON":    string(elemJSON),
		"NavItems":       navItems,
		"MoreNavItems":   moreNavItems,
		"ElementActions": elementActions,
	}

	h.render(w, "element", renderData)
}

// serveImage handles GET /{project}/element/{id}/image — serves raw PNG from S3
func (h *handler) serveImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	elem, err := h.cfg.ElementRepo.GetElement(r.Context(), id)
	if err != nil {
		slog.Warn("serve image: get element", "id", id, "error", err)
		http.Error(w, "element not found", http.StatusNotFound)
		return
	}

	if elem.Image == nil {
		http.Error(w, "no image available", http.StatusNotFound)
		return
	}

	// elem.Image.ProjectLocation is project-relative (images/{id}.png),
	// so we construct the full S3 key for the storage backend.
	imageS3Key := fmt.Sprintf("projects/%s/%s", elem.Project, elem.Image.ProjectLocation)
	rdr, _, err := h.cfg.Storage.GetObject(r.Context(), imageS3Key)
	if err != nil {
		slog.Error("serve image: get object", "error", err)
		http.Error(w, "image not found", http.StatusNotFound)
		return
	}
	defer rdr.Close()

	imgData, err := io.ReadAll(rdr)
	if err != nil {
		slog.Error("serve image: read", "error", err)
		http.Error(w, "failed to read image", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.FormatInt(int64(len(imgData)), 10))
	w.Header().Set("Cache-Control", "public, max-age=31536000")
	w.Write(imgData)
}

// ---- Job API Handlers ----

// activeJobsJSON handles GET /{project}/jobs/active
func (h *handler) activeJobsJSON(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	if h.cfg.JobService == nil {
		http.Error(w, "job service not available", http.StatusInternalServerError)
		return
	}

	jobs, err := h.cfg.JobService.ListActiveJobs(r.Context(), project)
	if err != nil {
		slog.Error("list active jobs", "error", err)
		http.Error(w, "failed to list jobs", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	encoded := make([]data.JobInfoJSON, len(jobs))
	for i, j := range jobs {
		encoded[i] = j.ToJSON()
	}
	json.NewEncoder(w).Encode(encoded)
}

// jobStatusJSON handles GET /{project}/jobs/{jobId} — job UUID lookup
func (h *handler) jobStatusJSON(w http.ResponseWriter, r *http.Request) {
	_ = r.PathValue("project")
	jobUUID := r.PathValue("jobId")

	if h.cfg.JobService == nil {
		http.Error(w, "job service not available", http.StatusInternalServerError)
		return
	}

	record, err := h.cfg.JobService.GetJobStatus(r.Context(), jobUUID)
	if err != nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(record.ToJSON())
}

// cancelJob handles POST /{project}/jobs/{jobId}/cancel — cancel by job UUID
func (h *handler) cancelJob(w http.ResponseWriter, r *http.Request) {
	_ = r.PathValue("project")
	jobUUID := r.PathValue("jobId")

	if h.cfg.JobService == nil {
		http.Error(w, "job service not available", http.StatusInternalServerError)
		return
	}

	err := h.cfg.JobService.CancelJob(r.Context(), jobUUID)
	if err != nil {
		slog.Error("cancel job", "job_uuid", jobUUID, "error", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
}

// cancelAllJobs handles POST /{project}/jobs/cancel-all
func (h *handler) cancelAllJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.cfg.JobService == nil {
		http.Error(w, "job service not available", http.StatusInternalServerError)
		return
	}

	project := r.PathValue("project")
	err := h.cfg.JobService.CancelStuckJobs(r.Context(), project)
	if err != nil {
		slog.Error("cancel stuck jobs", "project", project, "error", err)
		http.Error(w, "failed to cancel stuck jobs: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "cancelled_all"})
}

// generateClone handles POST /{project}/element/{id}/generate-clone —
// creates a new element (a sibling) with the same parameters but a new random seed.
func (h *handler) generateClone(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	project := r.PathValue("project")
	id := r.PathValue("id")

	// Get the original element.
	elem, err := h.cfg.ElementRepo.GetElement(r.Context(), id)
	if err != nil {
		slog.Warn("get element for generate-clone", "id", id, "error", err)
		http.Error(w, "element not found", http.StatusNotFound)
		return
	}

	// Create a new element with the same parameters but a random seed (if original was random).
	newSeed := elem.Generation.Seed
	if newSeed == -1 {
		newSeed = time.Now().UnixNano()
	}
	newElem := model.NewImageElement(
		project,
		elem.Generation.Prompt,
		elem.Generation.Width,
		elem.Generation.Height,
		elem.Generation.SampleSteps,
		elem.Generation.TxtCfg,
		newSeed,
		elem.Generation.Model.Architecture,
		elem.Generation.Model.Variant,
		elem.Generation.Model.Params,
		elem.Generation.Model.Quantization,
		elem.Generation.Model.Name,
	)
	if g := newElem.Generation; g != nil {
		g.NegativePrompt = elem.Generation.NegativePrompt
		g.BackendRef = elem.Generation.BackendRef
	}

	// Start a new job — generates a sibling element with a new ID.
	if h.cfg.JobService == nil {
		http.Error(w, "job service not available", http.StatusInternalServerError)
		return
	}

	elemID, err := h.cfg.JobService.StartJob(r.Context(), newElem)
	if err != nil {
		slog.Error("start generate-clone job", "error", err)
		http.Error(w, "failed to start generate-clone job: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"element_id": elemID})
}

// ---- Project Settings Handlers ----

// projectSettings handles GET /{project}/settings (returns JSON)
func (h *handler) projectSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	project := r.PathValue("project")
	ps, err := h.cfg.ProjectRepo.GetProjectSettings(r.Context(), project)
	if err != nil {
		slog.Warn("get project settings for settings", "project", project, "error", err)
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	// Collect extension delta settings.
	extensionSettings := make(map[string]map[string]any)
	if h.cfg.Hooks != nil && h.cfg.Hooks.SettingsField != nil {
		for _, fn := range h.cfg.Hooks.SettingsField {
			fieldHTML, err := fn(r.Context(), project)
			if err != nil {
				slog.Warn("settings field hook", "error", err)
				continue
			}
			if fieldHTML != "" {
				// Extract extension key from field HTML (format: data-extension="owner/extension").
				var extKey, extOwner, extName string
				if attr := extractAttr(string(fieldHTML), "data-extension"); attr != "" {
					extKey = attr
					parts := strings.SplitN(attr, "/", 2)
					if len(parts) == 2 {
						extOwner, extName = parts[0], parts[1]
					}
				}
				if extOwner == "" || extName == "" {
					continue
				}

				delta, err := h.cfg.ProjectRepo.GetExtensionSettings(r.Context(), project, extOwner, extName)
				if err != nil {
					slog.Warn("get extension settings", "extension", extKey, "error", err)
					continue
				}

				extensionSettings[extKey] = delta.Fields()
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"hidden":          ps.Hidden,
		"backend_ref":     ps.BackendRef,
		"backends":        h.cfg.BackendNames,
		"default_backend": h.cfg.DefaultBackend,
		"description":     ps.Description,
		"tags":            ps.Tags,
		"friendly_name":   ps.FriendlyName,
		"extension_settings": extensionSettings,
	})
}

// projectSettingsPage handles GET /{project}/settings — renders the project settings
// page with sidebar navigation and per-section save.
func (h *handler) projectSettingsPage(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	ps, err := h.cfg.ProjectRepo.GetProjectSettings(r.Context(), project)
	if err != nil {
		slog.Warn("get project settings for settings page", "project", project, "error", err)
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	// Collect sections from SettingsSection hook.
	var sections []Section
	if h.cfg.Hooks != nil && h.cfg.Hooks.SettingsSection != nil {
		for _, fn := range h.cfg.Hooks.SettingsSection {
			sec, err := fn(r.Context(), project, model.ProjectSettingsDelta{})
			if err != nil {
				slog.Warn("settings section hook", "error", err)
				continue
			}
			if sec == nil {
				continue
			}
			// Two-pass delta fetch: the first call above used an empty
			// delta because the hook's Section.ID (the owning extension)
			// is only known after it returns. For extension sections
			// ("owner/extension"), re-fetch the real S3 delta and re-invoke
			// the hook so its HTML renders persisted values.
			if owner, ext, ok := strings.Cut(sec.ID, "/"); ok && owner != "" && ext != "" {
				if realDelta, derr := h.cfg.ProjectRepo.GetExtensionSettings(r.Context(), project, owner, ext); derr == nil {
					if retry, rerr := fn(r.Context(), project, realDelta); rerr == nil && retry != nil {
						sec = retry
					} else if rerr != nil {
						slog.Warn("settings section hook (delta retry)", "section", sec.ID, "error", rerr)
					}
				} else {
					slog.Warn("get extension settings for settings page", "extension", sec.ID, "error", derr)
				}
			}
			sections = append(sections, *sec)
		}
	}

	// Backward compat: adapt deprecated SettingsField hooks into Section entries.
	if h.cfg.Hooks != nil && h.cfg.Hooks.SettingsField != nil {
		for _, fn := range h.cfg.Hooks.SettingsField {
			fieldHTML, err := fn(r.Context(), project)
			if err != nil {
				slog.Warn("settings field hook (legacy)", "error", err)
				continue
			}
			if fieldHTML == "" {
				continue
			}
			htmlStr := string(fieldHTML)
			var extKey string
			if attr := extractAttr(htmlStr, "data-extension"); attr != "" {
				extKey = attr
			}
			if extKey == "" {
				continue
			}
			// Extract extension label from key (e.g. "joleuger/photobooth" → "Photobooth").
			parts := strings.SplitN(extKey, "/", 2)
			label := parts[len(parts)-1]
			if len(parts) == 2 {
				label = parts[1]
			}

			// Infer field keys from data-field attributes in the HTML.
			var fields []FieldInfo
			// Simple heuristic: find all `data-field="..."` values.
			for {
				startIdx := strings.Index(htmlStr, "data-field=\"")
				if startIdx == -1 {
					break
				}
				startIdx += len(`data-field="`)
				endIdx := strings.Index(htmlStr[startIdx:], "\"")
				if endIdx == -1 {
					break
				}
				key := htmlStr[startIdx : startIdx+endIdx]
				// Determine input type from the HTML.
				fType := "text"
				if strings.Contains(htmlStr[:startIdx+endIdx+startIdx-100], `type="checkbox"`) {
					fType = "checkbox"
				} else if strings.Contains(htmlStr[:startIdx+endIdx+startIdx-100], `type="textarea"`) {
					fType = "textarea"
				}
				fields = append(fields, FieldInfo{Key: key, Type: fType})
				htmlStr = htmlStr[startIdx+endIdx+1:]
			}

			sections = append(sections, Section{
				ID:    extKey,
				Label: label,
				HTML:  template.HTML(htmlStr),
				Fields: fields,
			})
		}
	}

	// Always include a "Core" section.
	bchk := func(b bool) string { if b { return " checked" }; return "" }
	// Build selected backend option.
	var backendOptions string
	for _, b := range h.cfg.BackendNames {
		selected := ""
		if b == ps.BackendRef {
			selected = " selected"
		}
		backendOptions += fmt.Sprintf(`<option value="%s"%s>%s</option>`, b, selected, b)
	}

	// Build tags value.
	var tagsValue string
	for i, t := range ps.Tags {
		if i > 0 {
			tagsValue += ", "
		}
		tagsValue += t
	}

	coreSection := Section{
		ID:    "core",
		Label: "Core",
		HTML: template.HTML(fmt.Sprintf(`
			<div class="form-group">
				<label for="settingsFriendlyName">Friendly Name</label>
				<input type="text" id="settingsFriendlyName" data-section="core" data-field="friendly_name"
					   value="%s" style="width:100%%;background:#0d1b36;border:1px solid #0f3460;color:#e0e0e0;padding:0.5rem 0.75rem;border-radius:6px;font-size:0.9rem;box-sizing:border-box">
			</div>
			<div class="form-group">
				<label for="settingsBackend">sdcpp Backend</label>
				<select id="settingsBackend" data-section="core" data-field="backend_ref" style="width:100%%;background:#0d1b36;border:1px solid #0f3460;color:#e0e0e0;padding:0.5rem 0.75rem;border-radius:6px;font-size:0.9rem;box-sizing:border-box">
					%s
				</select>
			</div>
			<div class="form-group">
				<label class="switch-label">
					<span class="switch">
						<input type="checkbox" id="settingsHidden" data-section="core" data-field="hidden"%s>
						<span class="slider"></span>
					</span>
					<span class="switch-label">Hide from overview</span>
				</label>
			</div>
			<div class="form-group">
				<label for="settingsDescription">Description</label>
				<textarea id="settingsDescription" data-section="core" data-field="description" placeholder="Project description" style="width:100%%;background:#0d1b36;border:1px solid #0f3460;color:#e0e0e0;padding:0.5rem 0.75rem;border-radius:6px;font-size:0.9rem;box-sizing:border-box">%s</textarea>
			</div>
			<div class="form-group">
				<label for="settingsTags">Tags (comma-separated)</label>
				<input type="text" id="settingsTags" data-section="core" data-field="tags"
					   value="%s"
					   placeholder="e.g. art, photos, test" style="width:100%%;background:#0d1b36;border:1px solid #0f3460;color:#e0e0e0;padding:0.5rem 0.75rem;border-radius:6px;font-size:0.9rem;box-sizing:border-box">
			</div>
		`, ps.FriendlyName, backendOptions, bchk(ps.Hidden), ps.Description, tagsValue)),
		Fields: []FieldInfo{
			{Key: "friendly_name", Type: "text"},
			{Key: "backend_ref", Type: "text"},
			{Key: "hidden", Type: "checkbox"},
			{Key: "description", Type: "textarea"},
			{Key: "tags", Type: "text"},
		},
	}

	// Insert Core section at the beginning.
	allSections := append([]Section{coreSection}, sections...)

	// Build section data JSON for the template.
	type sectionData struct {
		ID        string     `json:"id"`
		Label     string     `json:"label"`
		Fields    []FieldInfo `json:"fields"`
		Snapshots map[string]any `json:"snapshots,omitempty"`
	}
	secData := make(map[string]sectionData)
	for _, sec := range allSections {
		snap := make(map[string]any)
		for _, f := range sec.Fields {
			snap[f.Key] = ""
		}
		secData[sec.ID] = sectionData{
			ID:        sec.ID,
			Label:     sec.Label,
			Fields:    sec.Fields,
			Snapshots: snap,
		}
	}

	// Collect section IDs for the template.
	var secIDs []string
	for _, sec := range allSections {
		secIDs = append(secIDs, sec.ID)
	}

	renderData := map[string]any{
		"Title":              h.cfg.Title,
		"Project":            project,
		"Sections":           allSections, // slice — for {{ range .Sections }} template iteration
		"SectionData":        secData,     // map — for const sections = {{ .SectionData }} JS embedding
		"SectionIDs":         secIDs,
		"Backends":           h.cfg.BackendNames,
		"BackendRef":         ps.BackendRef,
		"Hidden":             ps.Hidden,
		"Description":        ps.Description,
		"Tags":               ps.Tags,
		"FriendlyName":       ps.FriendlyName,
	}

	h.render(w, "project_settings", renderData)
}

// extractAttr extracts a data-* attribute value from HTML.
func extractAttr(html, attr string) string {
	key := attr + `="`
	idx := strings.Index(html, key)
	if idx == -1 {
		return ""
	}
	start := idx + len(key)
	end := strings.Index(html[start:], `"`)
	if end == -1 {
		return ""
	}
	return html[start : start+end]
}

// createProject handles POST /{project}/create — creates a new project (form-based).
func (h *handler) createProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	project := r.PathValue("project")
	h.createProjectInternal(w, r, project)
}

// updateProjectSettings handles POST /{project}/settings (updates hidden, backend_ref, or triggers sync)
// Supports scoped saves: when "section" is provided, only that section's fields are applied.
func (h *handler) updateProjectSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	project := r.PathValue("project")

	// Parse JSON body.
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Check for scoped save (section-aware).
	sectionID, _ := body["section"].(string)

	if sectionID == "core" {
		// Scoped core save: only update core fields from the "fields" map.
		if fields, ok := body["fields"].(map[string]any); ok {
			if v, ok := fields["hidden"]; ok {
				hidden, _ := v.(bool)
				if err := h.cfg.ProjectRepo.UpdateProjectHidden(ctx, project, hidden); err != nil {
					slog.Error("update project hidden", "project", project, "error", err)
					http.Error(w, "failed to update hidden", http.StatusInternalServerError)
					return
				}
			}
			if v, ok := fields["backend_ref"]; ok {
				backendRef, _ := v.(string)
				if err := h.cfg.ProjectRepo.UpdateProjectBackend(ctx, project, backendRef); err != nil {
					slog.Error("update project backend", "project", project, "error", err)
					http.Error(w, "failed to update backend", http.StatusInternalServerError)
					return
				}
			}
			if v, ok := fields["friendly_name"]; ok {
				ps, err := h.cfg.ProjectRepo.GetProjectSettings(ctx, project)
				if err != nil {
					slog.Error("get project settings for update", "project", project, "error", err)
					http.Error(w, "failed to get project settings", http.StatusInternalServerError)
					return
				}
				ps.FriendlyName, _ = v.(string)
				if err := h.cfg.ProjectRepo.UpdateProjectSettings(ctx, ps); err != nil {
					slog.Error("update project settings", "project", project, "error", err)
					http.Error(w, "failed to update project settings", http.StatusInternalServerError)
					return
				}
			}
			if v, ok := fields["description"]; ok {
				ps, err := h.cfg.ProjectRepo.GetProjectSettings(ctx, project)
				if err != nil {
					slog.Error("get project settings for update", "project", project, "error", err)
					http.Error(w, "failed to get project settings", http.StatusInternalServerError)
					return
				}
				ps.Description, _ = v.(string)
				if v, ok := fields["tags"]; ok {
					if tagsData, ok := v.([]any); ok {
						var tags []string
						for _, t := range tagsData {
							if tag, ok := t.(string); ok {
								tags = append(tags, tag)
							}
						}
						ps.Tags = tags
					}
				}
				if err := h.cfg.ProjectRepo.UpdateProjectSettings(ctx, ps); err != nil {
					slog.Error("update project settings", "project", project, "error", err)
					http.Error(w, "failed to update project settings", http.StatusInternalServerError)
					return
				}
			}
		}
	} else if sectionID != "" {
		// Scoped extension save: "section" = "owner/extension", "fields" = delta fields.
		// The extension owns its own settings file — the core only dispatches
		// to the extension's registered saver (validation and persistence are
		// the extension's responsibility).
		if fields, ok := body["fields"].(map[string]any); ok {
			parts := strings.SplitN(sectionID, "/", 2)
			if len(parts) != 2 {
				slog.Warn("scoped save: invalid section ID", "section", sectionID)
				http.Error(w, "invalid section", http.StatusBadRequest)
				return
			}
			saver, ok := settingsSaver(h.cfg, sectionID)
			if !ok {
				slog.Warn("scoped save: no settings saver registered", "section", sectionID)
				http.Error(w, "no settings handler for section "+sectionID, http.StatusBadRequest)
				return
			}
			if err := saver(ctx, project, fields); err != nil {
				var ve *ValidationError
				if errors.As(err, &ve) {
					http.Error(w, ve.Message, http.StatusBadRequest)
				} else {
					slog.Error("settings saver failed", "section", sectionID, "error", err)
					http.Error(w, "failed to save section "+sectionID, http.StatusInternalServerError)
				}
				return
			}
		}
	} else {
		// Legacy full-payload save (backward compat).
		// Update hidden flag.
		if v, ok := body["hidden"]; ok {
			hidden, _ := v.(bool)
			if err := h.cfg.ProjectRepo.UpdateProjectHidden(ctx, project, hidden); err != nil {
				slog.Error("update project hidden", "project", project, "error", err)
				http.Error(w, "failed to update hidden", http.StatusInternalServerError)
				return
			}
		}

		// Update backend reference.
		if v, ok := body["backend_ref"]; ok {
			backendRef, _ := v.(string)
			if err := h.cfg.ProjectRepo.UpdateProjectBackend(ctx, project, backendRef); err != nil {
				slog.Error("update project backend", "project", project, "error", err)
				http.Error(w, "failed to update backend", http.StatusInternalServerError)
				return
			}
			// Update JobService backend resolver reference.
			if h.cfg.JobService != nil && h.cfg.JobService.BackendResolver != nil {
				if url, err := h.cfg.JobService.BackendResolver(backendRef); err == nil {
					slog.Info("switched backend", "project", project, "backend", backendRef, "url", url)
				}
			}
		}

		// Update description and tags.
		if v, ok := body["description"]; ok {
			description, _ := v.(string)
			if v, ok := body["tags"]; ok {
				var tags []string
				if tagsData, ok := v.([]any); ok {
					for _, t := range tagsData {
						if tag, ok := t.(string); ok {
							tags = append(tags, tag)
						}
					}
				}
				// Update project settings with new description and tags.
				ps, err := h.cfg.ProjectRepo.GetProjectSettings(ctx, project)
				if err != nil {
					slog.Error("get project settings for update", "project", project, "error", err)
					http.Error(w, "failed to get project settings", http.StatusInternalServerError)
					return
				}
				ps.Description = description
				ps.Tags = tags
				if v, ok := body["friendly_name"]; ok {
					ps.FriendlyName, _ = v.(string)
				}
				if err := h.cfg.ProjectRepo.UpdateProjectSettings(ctx, ps); err != nil {
					slog.Error("update project settings", "project", project, "error", err)
					http.Error(w, "failed to update project settings", http.StatusInternalServerError)
					return
				}
			}
		} else if v, ok := body["friendly_name"]; ok {
			// If description/tags not present but friendly_name is, update it directly.
			ps, err := h.cfg.ProjectRepo.GetProjectSettings(ctx, project)
			if err != nil {
				slog.Error("get project settings for update", "project", project, "error", err)
				http.Error(w, "failed to get project settings", http.StatusInternalServerError)
				return
			}
			ps.FriendlyName, _ = v.(string)
			if err := h.cfg.ProjectRepo.UpdateProjectSettings(ctx, ps); err != nil {
				slog.Error("update project settings", "project", project, "error", err)
				http.Error(w, "failed to update project settings", http.StatusInternalServerError)
				return
			}
		}

		// Handle extension delta settings — dispatch to each extension's
		// registered saver (the extension owns its own settings file).
		// Legacy path keeps best-effort semantics: unknown sections are
		// skipped with a warning.
		if extSettings, ok := body["extension_settings"].(map[string]any); ok {
			for extKey, fields := range extSettings {
				if fieldsMap, ok := fields.(map[string]any); ok {
					parts := strings.SplitN(extKey, "/", 2)
					if len(parts) != 2 {
						continue
					}
					saver, ok := settingsSaver(h.cfg, extKey)
					if !ok {
						slog.Warn("legacy save: no settings saver registered", "extension", extKey)
						continue
					}
					if err := saver(ctx, project, fieldsMap); err != nil {
						slog.Error("settings saver failed", "extension", extKey, "error", err)
						continue
					}
				}
			}
		}
	}

	// Trigger full sync for this project.
	if _, ok := body["resync"]; ok {
		if h.cfg.JobService != nil && h.cfg.ElementRepo != nil {
			if err := h.cfg.ElementRepo.SyncFromStorage(ctx); err != nil {
				slog.Error("sync from storage", "project", project, "error", err)
				http.Error(w, "sync failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
			if err := h.cfg.ProjectRepo.IncrementSyncCount(ctx, project); err != nil {
				slog.Warn("increment sync count", "project", project, "error", err)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":    "ok",
		"saved_fields": body["fields"],
	})
}

// switchBackend handles POST /switch-backend (global backend switch)
func (h *handler) switchBackend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	name := body["backend"]
	if name == "" {
		http.Error(w, "backend name required", http.StatusBadRequest)
		return
	}

	// Validate backend exists.
	found := false
	for _, b := range h.cfg.BackendNames {
		if b == name {
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "backend not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"backend": name})
}

// regenerateInPlace handles POST /{project}/element/{id}/regenerate-in-place —
// recreates the element image in-place with the exact same parameters (same seed, same element ID).
func (h *handler) regenerateInPlace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	project := r.PathValue("project")
	id := r.PathValue("id")

	// Get the original element.
	elem, err := h.cfg.ElementRepo.GetElement(r.Context(), id)
	if err != nil {
		slog.Warn("get element for regenerate-in-place", "id", id, "error", err)
		http.Error(w, "element not found", http.StatusNotFound)
		return
	}

	// Build a fresh element using the exact same values (same ID, same seed, same everything).
	newElem := model.NewImageElement(
		project,
		elem.Generation.Prompt,
		elem.Generation.Width,
		elem.Generation.Height,
		elem.Generation.SampleSteps,
		elem.Generation.TxtCfg,
		elem.Generation.Seed,
		elem.Generation.Model.Architecture,
		elem.Generation.Model.Variant,
		elem.Generation.Model.Params,
		elem.Generation.Model.Quantization,
		elem.Generation.Model.Name,
	)
	newElem.ID = id
	if g := newElem.Generation; g != nil {
		g.NegativePrompt = elem.Generation.NegativePrompt
		g.BackendRef = elem.Generation.BackendRef
	}

	if h.cfg.JobService == nil {
		http.Error(w, "job service not available", http.StatusInternalServerError)
		return
	}

	// Cancel any active job for this element (handles stuck backend case).
	if err := h.cfg.JobService.CancelJob(r.Context(), id); err != nil {
		slog.Warn("regenerate-in-place: cancel active job (may be already done)", "error", err)
	}

	// Start a new job — StartJob reuses the existing element in the DB (no new element is created).
	elemID, err := h.cfg.JobService.StartJob(r.Context(), newElem)
	if err != nil {
		slog.Error("start regenerate-in-place job", "error", err)
		http.Error(w, "failed to start regenerate-in-place job: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"element_id": elemID})
}

// deleteElement handles POST /{project}/element/{id}/delete
func (h *handler) deleteElement(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	project := r.PathValue("project")
	id := r.PathValue("id")

	// Validate project name.
	if !validProjectName.MatchString(project) {
		http.Error(w, "invalid project name", http.StatusBadRequest)
		return
	}

	err := h.cfg.ElementRepo.DeleteElement(r.Context(), id, project)
	if err != nil {
		slog.Error("delete element", "project", project, "id", id, "error", err)
		http.Error(w, "failed to delete element: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// If it's an AJAX request (Accept: application/json), return JSON.
	if r.Header.Get("Accept") == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
		return
	}

	// Otherwise redirect to gallery.
	http.Redirect(w, r, fmt.Sprintf("/basic/%s/gallery", project), http.StatusSeeOther)
}

// uploadExternal handles POST /api/{project}/elements/upload — uploads an external (non-generated) image.
func (h *handler) uploadExternal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	project := r.PathValue("project")

	// Parse multipart form.
	r.ParseMultipartForm(10 << 20) // 10MB max
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Validate file type.
	contentType := header.Header.Get("Content-Type")
	if contentType != "image/png" && contentType != "image/jpeg" {
		http.Error(w, "only PNG and JPEG images are supported", http.StatusBadRequest)
		return
	}

	// Generate element ID.
	elemID := uuid.New().String()

	// Read image data to compute dimensions.
	imgData, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "failed to read image", http.StatusInternalServerError)
		return
	}

	// Decode dimensions.
	cfg, _, imgErr := image.DecodeConfig(bytes.NewReader(imgData))
	if imgErr != nil {
		http.Error(w, "invalid image file", http.StatusBadRequest)
		return
	}

	// Build element with origin=external, no Generation.
	elem := model.Element{
		ID:          elemID,
		Project:     project,
		Kind:        "image",
		Origin:      "external",
		SchemaVersion: 1,
		Version:     1,
		CreatedAt:   time.Now(),
		Image: &model.ImageInfo{
			ProjectLocation: fmt.Sprintf("images/%s.png", elemID),
			Format:          "png",
			Width:           int(cfg.Width),
			Height:          int(cfg.Height),
			SizeBytes:       int64(len(imgData)),
		},
	}

	// Write to S3 (image + JSON) and SQLite.
	imageReader := io.NopCloser(bytes.NewReader(imgData))
	if err := h.cfg.ElementRepo.CreateElement(r.Context(), elem, imageReader, int64(len(imgData))); err != nil {
		slog.Error("create external element", "element", elemID, "error", err)
		http.Error(w, "failed to create element", http.StatusInternalServerError)
		return
	}

	slog.Info("uploaded external element", "element", elemID, "project", project)

	// If it's an AJAX request, return JSON.
	if r.Header.Get("Accept") == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"element_id": elemID})
		return
	}

	// Otherwise redirect to element detail page.
	target := "/basic/" + project + "/element/" + elemID
	if h.cfg.PathPrefix != "" {
		target = h.cfg.PathPrefix + target
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// img2img handles POST /api/{project}/elements/img2img — img2img generation.
func (h *handler) img2img(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	project := r.PathValue("project")

	var req struct {
		ElementID    string   `json:"element_id"`
		Prompt       string   `json:"prompt"`
		NegativePrompt string `json:"negative_prompt"`
		ReferenceIDs []string `json:"reference_ids"`
		Strength     float64  `json:"strength"`
		Width        int      `json:"width"`
		Height       int      `json:"height"`
		Steps        int      `json:"steps"`
		Cfg          float64  `json:"cfg"`
		Seed         int64    `json:"seed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.ElementID == "" || req.Prompt == "" || len(req.ReferenceIDs) == 0 {
		http.Error(w, "element_id, prompt, and reference_ids are required", http.StatusBadRequest)
		return
	}
	if req.Strength <= 0 || req.Strength > 1 {
		req.Strength = 0.75
	}
	if req.Width == 0 {
		req.Width = 512
	}
	if req.Height == 0 {
		req.Height = 512
	}
	if req.Steps == 0 {
		req.Steps = 20
	}
	if req.Cfg == 0 {
		req.Cfg = 7
	}

	// Build ElementRef list from reference element IDs.
	refs := make([]model.ElementRef, 0, len(req.ReferenceIDs))
	for _, refID := range req.ReferenceIDs {
		refs = append(refs, model.ElementRef{ElementID: refID})
	}

	// Create new element with origin="generated", task="img2img".
	newSeed := req.Seed
	if newSeed == 0 {
		newSeed = -1 // random
	}
	elem := model.Element{
		ID:          uuid.New().String(),
		Project:     project,
		Kind:        "image",
		Origin:      "generated",
		SchemaVersion: 1,
		Version:     1,
		CreatedAt:   time.Now().UTC(),
		Generation: &model.Generation{
			Task:             "img2img",
			Prompt:           req.Prompt,
			NegativePrompt:   req.NegativePrompt,
			Width:            req.Width,
			Height:           req.Height,
			Seed:             newSeed,
			SampleSteps:      req.Steps,
			TxtCfg:           req.Cfg,
			Strength:         req.Strength,
			ReferenceImages:  refs,
		},
	}

	// Set the project's selected backend.
	if projMeta, err := h.cfg.ProjectRepo.GetProjectMeta(r.Context(), project); err == nil {
		elem.Generation.BackendRef = projMeta.BackendRef
	}

	// Start job via JobService.
	elemID, err := h.cfg.JobService.StartJob(r.Context(), elem)
	if err != nil {
		slog.Error("img2img: start job", "error", err)
		http.Error(w, "failed to start job: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"element_id": elemID})
}

// externalPage handles GET /basic/{project}/external — shows all external (non-generated) images.
func (h *handler) externalPage(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	// Parse query params
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 { page = 1 }

	opts := data.ListOptions{
		Page:    page,
		PerPage: 50,
		Sort:    "created_at",
		Order:   "desc",
		Origin:  "external",
	}
	opts.Validate()

	elements, total, err := h.cfg.ElementRepo.ListElements(r.Context(), project, opts)
	if err != nil {
		slog.Error("list external elements", "error", err)
		h.renderError(w, r, "failed to load external images")
		return
	}

	// Collect NavBarItems from extensions.
	var navItems template.HTML
	if h.cfg.Hooks != nil {
		for _, fn := range h.cfg.Hooks.NavBarItems {
			items, err := fn(r.Context(), project)
			if err != nil {
				slog.Warn("nav bar items hook", "project", project, "error", err)
				continue
			}
			navItems += items
		}
	}

	// Collect MoreNavItems from extensions.
	var moreNavItems template.HTML
	if h.cfg.Hooks != nil {
		for _, fn := range h.cfg.Hooks.MoreNavItems {
			items, err := fn(r.Context(), project)
			if err != nil {
				slog.Warn("more nav items hook", "project", project, "error", err)
				continue
			}
			moreNavItems += items
		}
	}

	renderData := map[string]any{
		"Title":        h.cfg.Title,
		"Project":      project,
		"Page":         "external",
		"Gallery":      elements,
		"Total":        total,
		"PageNum":      opts.Page,
		"TotalPages":   opts.TotalPages(total),
		"PrevPage":     opts.Page - 1,
		"NextPage":     opts.Page + 1,
		"NavItems":     navItems,
		"MoreNavItems": moreNavItems,
	}

	h.render(w, "external", renderData)
}

// deleteProject handles POST /{project}/delete
func (h *handler) deleteProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	project := r.PathValue("project")

	// Validate project name.
	if !validProjectName.MatchString(project) {
		http.Error(w, "invalid project name", http.StatusBadRequest)
		return
	}

	err := h.cfg.ProjectRepo.DeleteProject(r.Context(), project)
	if err != nil {
		slog.Error("delete project", "project", project, "error", err)
		http.Error(w, "failed to delete project: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Call OnProjectDeleted hooks (extensions clean up their own S3 objects).
	if h.cfg.Hooks != nil {
		for _, fn := range h.cfg.Hooks.OnProjectDeleted {
			if err := fn(r.Context(), project); err != nil {
				slog.Warn("project deleted hook", "project", project, "error", err)
			}
		}
	}

	// Redirect to home page.
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ---- helpers ----

func (h *handler) renderError(w http.ResponseWriter, r *http.Request, msg string) {
	project := r.PathValue("project")
	if project != "" {
		http.Redirect(w, r, "/basic/"+project+"?error="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	http.Error(w, msg, http.StatusBadRequest)
}

// renderDataURLs injects prefix-aware URL helpers into template data.
// Both "prefix" and "urlPath" are replaced so templates can build
// correct URLs regardless of the configured path prefix.
func renderDataURLs(data any, prefix string) any {
	// Only modify map[string]any types.
	d, ok := data.(map[string]any)
	if !ok {
		return data
	}
	d["prefix"] = prefix
	d["urlPath"] = func(path string) string {
		if prefix == "" {
			return path
		}
		return prefix + path
	}
	return d
}

func (h *handler) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	// Set the prefix so the urlPath/prefix FuncMap entries can produce
	// prefix-aware URLs. The FuncMap reads currentPrefix under a mutex.
	prefixMu.Lock()
	currentPrefix = h.cfg.PathPrefix
	prefixMu.Unlock()
	defer func() {
		prefixMu.Lock()
		currentPrefix = ""
		prefixMu.Unlock()
	}()

	data = renderDataURLs(data, h.cfg.PathPrefix)

	// Inject enabled extensions list into template data.
	d, ok := data.(map[string]any)
	if ok {
		extMu.RLock()
		exts := make([]string, len(enabledExtensions))
		copy(exts, enabledExtensions)
		extMu.RUnlock()
		d["EnabledExtensions"] = exts
	}

	if err := h.templates.ExecuteTemplate(w, name+".html", data); err != nil {
		slog.Error("render template", "name", name, "error", err)
	}
}
