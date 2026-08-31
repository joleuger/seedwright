package onboarding

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"seedwright/internal/app"
	"seedwright/internal/authz"
	"seedwright/internal/data/model"
	"seedwright/internal/storage"
)

// projectNameRe is the same project-name rule the core enforces.
var projectNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// registerRoutes wires the onboarding page and JSON API.
// The page, the verify probes, and the config PREVIEW are public: a
// first-time user has nothing configured yet, and reaching this page
// is the whole point. Previews never reveal secrets of the current
// config (S3 keys are masked), so public exposure is safe. Writing
// config, applying profiles, and downloading the UNMASKED config
// require ManagePermissions (root by default) — and overwriting an
// existing config additionally needs the running config's
// allow_config_write flag plus an explicit confirm_overwrite.
func (e *Extension) registerRoutes(a *app.App) {
	e.mux.Handle("GET /onboarding", authz.Public()(http.HandlerFunc(e.handlePage)))
	e.mux.Handle("POST /api/onboarding/verify", authz.Public()(http.HandlerFunc(e.handleVerify)))
	e.mux.Handle("POST /api/onboarding/preview", authz.Public()(http.HandlerFunc(e.handlePreview)))
	e.mux.Handle("POST /api/onboarding/complete",
		authz.RequireAction(authz.ActionManagePermissions, a.Authz)(http.HandlerFunc(e.handleComplete)))
	e.mux.Handle("POST /api/onboarding/profile",
		authz.RequireAction(authz.ActionManagePermissions, a.Authz)(http.HandlerFunc(e.handleProfile)))
	e.mux.Handle("POST /api/onboarding/download",
		authz.RequireAction(authz.ActionManagePermissions, a.Authz)(http.HandlerFunc(e.handleDownload)))
}

// handlePage renders the Setup & Customize page.
func (e *Extension) handlePage(w http.ResponseWriter, r *http.Request) {
	tmpl, err := onboardingTemplate()
	if err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	st := e.buildState(r.Context())
	fresh, allowed, _ := e.writeGate()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Errors mid-write are unrecoverable (headers already sent); the
	// user simply sees a truncated page, which is rare.
	_ = tmpl.Execute(w, map[string]any{
		"Title":  "Setup & Customize",
		"prefix": e.pathPrefix,
		"State":  st,
		"OnboardingJS": scriptJSON(scriptPayload{
			Prefix:           e.pathPrefix,
			ConfigExists:     !fresh,
			WriteAllowed:     allowed,
			ConfirmRequired:  !fresh,
			EphemeralWarning: st.EphemeralWarning,
		}),
	})
}

// --- verify ---

// verifyRequest is the payload for POST /api/onboarding/verify.
type verifyRequest struct {
	Target string `json:"target"` // "storage" | "backend"

	// target=backend
	URL string `json:"url"`

	// target=storage
	StorageType    string `json:"storage_type"`
	MemoryCapacity string `json:"memory_capacity"`
	FilePath       string `json:"file_path"`
	Endpoint       string `json:"endpoint"`
	Region         string `json:"region"`
	Bucket         string `json:"bucket"`
	AccessKey      string `json:"access_key"`
	SecretKey      string `json:"secret_key"`
	ForcePathStyle bool   `json:"force_path_style"`
}

func (e *Extension) handleVerify(w http.ResponseWriter, r *http.Request) {
	var req verifyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	switch req.Target {
	case "backend":
		ok, detail := verifyBackend(ctx, req.URL)
		writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "detail": detail})
	case "storage":
		ok, detail := e.verifyStorage(ctx, req)
		writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "detail": detail})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": `target must be "storage" or "backend"`})
	}
}

// verifyStorage builds a throwaway backend from the submitted fields
// and probes it with a read-only ListObjects. Nothing is written.
func (e *Extension) verifyStorage(ctx context.Context, req verifyRequest) (bool, string) {
	node := *storageNode(completeRequest{
		StorageType:    req.StorageType,
		MemoryCapacity: req.MemoryCapacity,
		FilePath:       req.FilePath,
		Endpoint:       req.Endpoint,
		Region:         req.Region,
		Bucket:         req.Bucket,
		AccessKey:      req.AccessKey,
		SecretKey:      req.SecretKey,
		ForcePathStyle: req.ForcePathStyle,
	})
	sb, err := storage.NewStorageBackend(node)
	if err != nil {
		return false, err.Error()
	}
	if _, err := sb.ListObjects(ctx, "projects/"); err != nil {
		return false, "unreachable: " + err.Error()
	}
	switch req.StorageType {
	case "memory":
		return true, "In-memory storage — no external setup needed. Contents are lost on restart."
	case "file":
		return true, "Folder reachable: " + req.FilePath
	case "s3":
		return true, "S3 bucket reachable: " + req.Bucket
	}
	return true, "reachable"
}

// --- complete ---

// validateComplete checks the Finish payload and returns it with
// defaults applied.
func validateComplete(req completeRequest) (*completeRequest, error) {
	switch req.StorageType {
	case "memory":
		if req.MemoryCapacity != "" {
			if _, err := storage.ParseCapacity(req.MemoryCapacity); err != nil {
				return nil, fmt.Errorf("memory capacity: %w", err)
			}
		}
	case "file":
		if req.FilePath == "" {
			return nil, fmt.Errorf("file storage needs a folder path")
		}
	case "s3":
		if req.Endpoint == "" || req.Region == "" || req.Bucket == "" {
			return nil, fmt.Errorf("S3 storage needs endpoint, region, and bucket")
		}
	default:
		return nil, fmt.Errorf("storage type must be memory, file, or s3")
	}

	u, err := url.Parse(req.BackendURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("backend URL must look like http://127.0.0.1:1234")
	}
	if req.Title == "" {
		req.Title = "Seedwright"
	}
	if req.ProjectName != "" && !projectNameRe.MatchString(req.ProjectName) {
		return nil, fmt.Errorf("project name may only use letters, digits, dot, underscore, dash — and must start with a letter or digit")
	}
	return &req, nil
}

// profileDefaults applies a selected profile to the payload: the
// profile's title fills an empty title field. Unknown profile keys are
// an error. Returns the checked payload.
func (e *Extension) checkPayload(req completeRequest) (*completeRequest, error) {
	if req.ProfileKey != "" {
		p := GetProfile(req.ProfileKey)
		if p == nil {
			return nil, fmt.Errorf("unknown profile: %s", req.ProfileKey)
		}
		if req.Title == "" {
			req.Title = p.Title
		}
	}
	return validateComplete(req)
}

func (e *Extension) handleComplete(w http.ResponseWriter, r *http.Request) {
	var req completeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	checked, err := e.checkPayload(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	// Safe-write gate: a fresh file is always written; an EXISTING
	// config needs the running config's allow_config_write flag AND an
	// explicit confirm_overwrite (the manage_permissions authz gate
	// above is the first layer).
	fresh, allowed, reason := e.writeGate()
	if !fresh && !allowed {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": reason})
		return
	}
	if !fresh && !req.ConfirmOverwrite {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": "confirm_overwrite: true is required to overwrite an existing config file",
		})
		return
	}

	if err := e.writeConfigFile(*checked); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	path := e.configPath()
	verb := "written to"
	if !fresh {
		verb = "updated at"
	}
	msg := "Config " + verb + " " + path + " — please restart the app to apply."

	if checked.ProjectName != "" {
		created, cerr := e.ensureProject(r.Context(), checked.ProjectName)
		if cerr != nil {
			// Best effort — the wizard's job is the config file, not
			// project administration. The user can create the project
			// from the welcome page after restart.
			slog.Warn("onboarding: project creation failed", "project", checked.ProjectName, "error", cerr)
		} else if created {
			msg = "Config " + verb + " " + path + " and project " + checked.ProjectName + " created — please restart the app to apply."
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"config_path":      path,
		"restart_required": true,
		"message":          msg,
	})
}

// --- preview / download ---

// previewResponse is what POST /api/onboarding/preview returns. The
// YAML is masked (no secrets of the current config) — this endpoint is
// public.
type previewResponse struct {
	OK                 bool   `json:"ok"`
	Error              string `json:"error,omitempty"`
	YAML               string `json:"yaml,omitempty"`
	ConfigExists       bool   `json:"config_exists"`
	ConfigPath         string `json:"config_path"`
	// WriteAllowed is false only for a blocked overwrite (flag off in
	// the running config). Fresh files are always writable.
	WriteAllowed       bool   `json:"write_allowed"`
	ConfirmRequired    bool   `json:"confirm_required"`
	WriteBlockedReason string `json:"write_blocked_reason,omitempty"`
	EphemeralWarning   string `json:"ephemeral_warning,omitempty"`
}

// handlePreview renders the config the wizard would produce (secrets
// masked) without writing anything. Public — it reveals no secrets.
func (e *Extension) handlePreview(w http.ResponseWriter, r *http.Request) {
	var req completeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, previewResponse{OK: false, Error: err.Error()})
		return
	}
	checked, err := e.checkPayload(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, previewResponse{OK: false, Error: err.Error()})
		return
	}
	yamlText, err := e.renderConfig(*checked, true)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, previewResponse{OK: false, Error: err.Error()})
		return
	}
	fresh, allowed, reason := e.writeGate()
	writeJSON(w, http.StatusOK, previewResponse{
		OK:                 true,
		YAML:               yamlText,
		ConfigExists:       !fresh,
		ConfigPath:         e.configPath(),
		WriteAllowed:       allowed,
		ConfirmRequired:    !fresh,
		WriteBlockedReason: reason,
		EphemeralWarning:   ephemeralStorageWarning(e.configPath()),
	})
}

// handleDownload returns the unmasked config as a file attachment.
// Authorization-gated: the response contains the real S3 keys.
func (e *Extension) handleDownload(w http.ResponseWriter, r *http.Request) {
	var req completeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	checked, err := e.checkPayload(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	yamlText, err := e.renderConfig(*checked, false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="config.yaml"`)
	_, _ = w.Write([]byte(yamlText))
}

// ensureProject creates the project if it does not exist yet and
// reports whether it did.
func (e *Extension) ensureProject(ctx context.Context, name string) (bool, error) {
	if _, err := e.a.Projects.GetProjectSettings(ctx, name); err == nil {
		return false, nil
	}
	return true, e.a.Projects.CreateProject(ctx, model.NewProject(name))
}

// --- profile ---

type profileRequest struct {
	Key string `json:"key"`
}

func (e *Extension) handleProfile(w http.ResponseWriter, r *http.Request) {
	var req profileRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	p := GetProfile(req.Key)
	if p == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "unknown profile: " + req.Key})
		return
	}
	// A profile modifies the config file like any other write: an
	// existing file needs the allow_config_write flag (a fresh file is
	// created from the running effective config when absent).
	fresh, allowed, reason := e.writeGate()
	if !fresh && !allowed {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": reason})
		return
	}
	if err := e.applyProfile(*p); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"restart_required": true,
		"message":          "Profile " + p.Title + " written to " + e.configPath() + " — please restart the app to apply.",
	})
}

// --- helpers ---

func decodeJSON(r *http.Request, out any) error {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
