package batch

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"seedwright/internal/app"
)

// RegisterRoutes registers Batch's HTTP routes on the server mux.
//
// JSON endpoints under /api/, HTML progress page under /basic/ (part of
// the basic dashboard experience).
func (e *Extension) RegisterRoutes(a *app.App) {
	e.mux.HandleFunc("POST /api/{project}/ext/joleuger/batch/generate", e.handleBatchGenerate)
	e.mux.HandleFunc("POST /api/{project}/ext/joleuger/batch/preview", e.handlePreview)
	e.mux.HandleFunc("GET /basic/{project}/batch/{id}", e.handleBatchPage)
	e.mux.HandleFunc("GET /api/{project}/batch/{id}/api", e.handleBatchAPI)
	slog.Debug("batch: registered routes", "count", 4,
		"routes", []string{
			"POST /api/{project}/ext/joleuger/batch/generate",
			"POST /api/{project}/ext/joleuger/batch/preview",
			"GET /basic/{project}/batch/{id}",
			"GET /api/{project}/batch/{id}/api",
		})
}

// handleBatchGenerate handles POST /{project}/ext/joleuger/batch/generate.
// This is the batch entry point — always expands and always creates a batch.
// The UI calls this directly when combination syntax or multiple seeds are
// detected; single-job submissions go to the core POST /{project}/generate.
func (e *Extension) handleBatchGenerate(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	// ParseMultipartForm handles both application/x-www-form-urlencoded
	// and multipart/form-data (which Chrome's FormData API sends).
	// For multipart POST requests, ParseForm() alone can consume the body
	// without populating r.PostForm, so FormValue() returns "".
	// ParseMultipartForm is the reliable way to read both content types.
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		if err == http.ErrNotMultipart {
			// Not multipart — fall back to standard ParseForm.
			r.ParseForm()
		} else {
			slog.Error("batch: parse form", "error", err, "content_type", r.Header.Get("Content-Type"))
			http.Error(w, "failed to parse form", http.StatusBadRequest)
			return
		}
	}
	slog.Debug("batch: form data", "post_form", r.PostForm, "form", r.Form, "content_type", r.Header.Get("Content-Type"))

	// Helper: read a value from a parsed form, checking both maps
	// (FormValue handles this but we need to be explicit for MultipartForm
	// which may not populate PostForm for multipart requests).
	formValue := func(key string) string {
		if v := r.FormValue(key); v != "" {
			return v
		}
		if r.MultipartForm != nil {
			if vals := r.MultipartForm.Value[key]; len(vals) > 0 {
				return vals[0]
			}
		}
		return ""
	}

	prompt := formValue("prompt")
	negativePrompt := formValue("negative_prompt")

	width, _ := strconv.Atoi(formValue("width"))
	height, _ := strconv.Atoi(formValue("height"))
	steps, _ := strconv.Atoi(formValue("steps"))
	cfg, _ := strconv.ParseFloat(formValue("cfg"), 64)
	seedStr := formValue("seeds")

	if width == 0 { width = 512 }
	if height == 0 { height = 512 }
	if steps == 0 { steps = 20 }
	if cfg == 0 { cfg = 7.0 }

	if prompt == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "prompt is required"})
		return
	}

	// Expand.
	variants, err := e.Expand(prompt, seedStr)
	if err != nil {
		slog.Error("batch: expand", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to expand prompt: " + err.Error()})
		return
	}

	if len(variants) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "no variants produced"})
		return
	}

	// Create batch from all variants.
	batchID, err := e.CreateBatchFromVariants(r.Context(), project, prompt, negativePrompt, width, height, steps, cfg, variants)
	if err != nil {
		slog.Error("batch: create", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to create batch: " + err.Error()})
		return
	}

	http.Redirect(w, r, "/basic/"+project+"/batch/"+batchID, http.StatusSeeOther)
}

// handlePreview handles POST /{project}/ext/joleuger/batch/preview.
// Expands the prompt without creating a batch; returns count + expanded list as JSON.
func (e *Extension) handlePreview(w http.ResponseWriter, r *http.Request) {
	slog.Info("batch: handlePreview called", "method", r.Method, "path", r.URL.Path)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Prompt string `json:"prompt"`
		Seeds  string `json:"seeds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	variants, err := e.Expand(body.Prompt, body.Seeds)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	type variantJSON struct {
		Prompt string `json:"prompt"`
		Seed   int64  `json:"seed"`
	}
	jsonVariants := make([]variantJSON, len(variants))
	for i, v := range variants {
		jsonVariants[i] = variantJSON{Prompt: v.Prompt, Seed: v.Seed}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"count":    len(variants),
		"variants": jsonVariants,
	})
}

// handleBatchPage handles GET /{project}/batch/{id}.
func (e *Extension) handleBatchPage(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	batchID := r.PathValue("id")

	batch, err := e.GetBatch(r.Context(), batchID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if batch.Project_ != project {
		http.NotFound(w, r)
		return
	}

	items, err := e.GetBatchItems(r.Context(), batchID)
	if err != nil {
		http.Error(w, "failed to load batch items", http.StatusInternalServerError)
		return
	}
	// Populate Batch.Items so CardSteps() works via the cardstep trait.
	batch.Items = items

	tmpl := BatchTemplate()
	if err := tmpl.Execute(w, map[string]any{
		"Title":   "Batch",
		"Project": project,
		"Batch":   batch,
		"Items":   items,
	}); err != nil {
		slog.Error("batch: render template", "error", err)
	}
}

// handleBatchAPI handles GET /{project}/batch/{id}/api.
func (e *Extension) handleBatchAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	batchID := r.PathValue("id")
	project := r.PathValue("project")

	batch, err := e.GetBatch(r.Context(), batchID)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if batch.Project_ != project {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	items, err := e.GetBatchItems(r.Context(), batchID)
	if err != nil {
		http.Error(w, "failed to load items", http.StatusInternalServerError)
		return
	}

	type itemJSON struct {
		Position  int     `json:"position"`
		Seed      int64   `json:"seed"`
		Prompt    string  `json:"prompt"`
		ElementID *string `json:"element_id,omitempty"`
		Status    string  `json:"status"`
	}
	jsonItems := make([]itemJSON, len(items))
	for i, item := range items {
		jsonItems[i] = itemJSON{
			Position:  item.Position,
			Seed:      item.Seed,
			Prompt:    item.Prompt,
			ElementID: item.ElementID,
			Status:    item.Status_,
		}
	}

	resp := map[string]any{
		"batch_status": batch.Status_,
		"items":        jsonItems,
	}

	if batch.Status_ == "completed" || batch.Status_ == "cancelled" || batch.Status_ == "failed" {
		resp["batch_done"] = true
	}

	json.NewEncoder(w).Encode(resp)
}
