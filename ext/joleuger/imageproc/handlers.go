package imageproc

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
)

// previewRequest is the JSON body for the preview endpoint.
// All fields are required — imageproc has no defaults.
type previewRequest struct {
	ElementID string `json:"element_id"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Fit       string `json:"fit"`
	Rotate    string `json:"rotate"`
}

// previewError is the error response for the preview endpoint.
type previewError struct {
	Error string `json:"error"`
}

// infoResponse is the JSON response for the info endpoint.
type infoResponse struct {
	Engine    string `json:"engine"`
	Available bool   `json:"available"`
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(previewError{Error: msg})
}

// previewHandler handles POST /api/{project}/ext/joleuger/imageproc/preview.
// Runs the caller-supplied processing params against the element's image
// and returns the processed image bytes. Params are request fields —
// validated, never defaulted.
func (e *Extension) previewHandler(w http.ResponseWriter, r *http.Request) {
	var body previewRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	p := Params{Width: body.Width, Height: body.Height, Fit: body.Fit, Rotate: body.Rotate}
	if err := p.validate(); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.ElementID == "" {
		writeJSONError(w, http.StatusBadRequest, "element_id is required")
		return
	}

	project := r.PathValue("project")
	ctx := r.Context()

	elem, err := e.elements.GetElement(ctx, body.ElementID)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "element not found: "+body.ElementID)
		return
	}
	if elem.Project != project {
		writeJSONError(w, http.StatusBadRequest, "element does not belong to project "+project)
		return
	}
	if elem.Image == nil {
		writeJSONError(w, http.StatusBadRequest, "element has no image")
		return
	}

	key := "projects/" + project + "/" + elem.Image.ProjectLocation
	srcPath, srcCleanup, err := e.storage.LocalFile(ctx, key)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "fetch element image: "+err.Error())
		return
	}
	defer func() {
		if srcCleanup != nil {
			srcCleanup()
		}
	}()

	outPath, err := e.processor.Process(ctx, srcPath, p)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "process image: "+err.Error())
		return
	}
	if outPath != srcPath {
		defer os.Remove(outPath)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read processed image: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "image/png")
	if _, err := w.Write(data); err != nil {
		slog.Warn("imageproc: write preview response", "error", err)
	}
}

// infoHandler handles GET /api/{project}/ext/joleuger/imageproc/info.
// Lets the UI know which engine is selected and whether it will
// actually process.
func (e *Extension) infoHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(infoResponse{
		Engine:    e.processor.Name(),
		Available: e.processor.Available(),
	}); err != nil {
		slog.Warn("imageproc: encode info response", "error", err)
	}
}
