// Package printer implements image printing via the CUPS lp command
// as an extension to seedwright.
package printer

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"sort"
)

// previewRequest is the JSON body for the preview endpoint.
type previewRequest struct {
	ElementID string `json:"element_id"`
}

// printersHandler handles GET /api/{project}/ext/joleuger/printer/printers.
// Returns a JSON list of available printers: configured printers from
// config, plus auto-discovered local printers from lpstat — unless
// ?configured=true is given, in which case only configured printers are
// returned (the UI's print dialogs work exclusively with configured
// printers, so discovery never reaches them).
func (e *Extension) printersHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var allPrinters []PrinterInfo

	// Add configured printers.
	for _, p := range e.cfg.Printers {
		allPrinters = append(allPrinters, PrinterInfo{
			Name:       p.Name,
			URI:        p.URI,
			Configured: true,
			Status:     "", // we don't poll remote printer status in v1
			Crop:       p.Crop,
			Dimensions: e.canvasDimensions(p),
		})
	}

	// Add auto-discovered local printers (skipped for ?configured=true).
	if r.URL.Query().Get("configured") != "true" {
		localPrinters, err := listLocalPrinters()
		if err != nil {
			slog.Warn("printer: lpstat failed (non-fatal, only configured printers shown)", "error", err)
		} else {
			for _, lp := range localPrinters {
				uri := "cups://localhost:631/printers/" + lp.Name
				allPrinters = append(allPrinters, PrinterInfo{
					Name:       lp.Name,
					URI:        uri,
					Configured: false,
					Status:     lp.Status,
				})
			}
		}
	}

	// Sort: configured first (by name), then local (by name).
	sort.Slice(allPrinters, func(i, j int) bool {
		if allPrinters[i].Configured != allPrinters[j].Configured {
			return allPrinters[i].Configured // true before false
		}
		return allPrinters[i].Name < allPrinters[j].Name
	})

	if allPrinters == nil {
		allPrinters = []PrinterInfo{}
	}

	if err := json.NewEncoder(w).Encode(printersResponse{Printers: allPrinters}); err != nil {
		slog.Warn("printer: encode response", "error", err)
	}
}

// previewHandler handles POST /api/{project}/ext/joleuger/printer/preview.
// Returns the image URL and filename for the element's image.
func (e *Extension) previewHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var body previewRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(printError{Error: "invalid request body"})
		return
	}

	if body.ElementID == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(printError{Error: "element_id is required"})
		return
	}

	project := r.PathValue("project")
	resp := previewResponse{
		ImageURL: e.imagePath(project, body.ElementID),
		Filename: body.ElementID + ".png",
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Warn("printer: encode preview response", "error", err)
	}
}

// printHandler handles POST /api/{project}/ext/joleuger/printer/print.
// Resolves the element (project-strict), fetches the image into a local
// file, processes it onto the print canvas when the selected printer is
// a crop printer, and submits the local file to lp.
func (e *Extension) printHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var body printRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(printError{Error: "invalid request body"})
		return
	}

	if body.ElementID == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(printError{Error: "element_id is required"})
		return
	}
	if body.PrinterURI == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(printError{Error: "printer_uri is required"})
		return
	}
	if body.Copies < 1 {
		body.Copies = 1
	}
	if body.Copies > 99 {
		body.Copies = 99
	}

	project := r.PathValue("project")
	ctx := r.Context()

	// Project-strict: the element must exist and belong to this project.
	elem, err := e.elements.GetElement(ctx, body.ElementID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(printError{Error: "element not found: " + body.ElementID})
		return
	}
	if elem.Project != project {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(printError{Error: "element does not belong to project " + project})
		return
	}
	if elem.Image == nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(printError{Error: "element has no image"})
		return
	}

	// Fetch the element image as a local file (S3 download or file path).
	srcPath, cleanup, err := e.storage.LocalFile(ctx, "projects/"+project+"/"+elem.Image.ProjectLocation)
	if err != nil {
		slog.Warn("printer: fetch element image failed",
			"project", project, "element", body.ElementID, "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(printError{Error: "fetch element image: " + err.Error()})
		return
	}
	if cleanup != nil {
		defer cleanup()
	}

	// Crop printers process the image onto the print canvas first. A
	// printer URI not present in the config (e.g. lpstat-discovered) is
	// always printed raw.
	file := srcPath
	if entry, configured := e.printerEntry(body.PrinterURI); configured {
		if params, ok := e.cropParams(entry); ok {
			processed, perr := e.processor.Process(ctx, srcPath, params)
			if perr != nil {
				slog.Warn("printer: image processing failed",
					"project", project, "element", body.ElementID, "error", perr)
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(printError{Error: "process image: " + perr.Error()})
				return
			}
			if processed != srcPath {
				defer os.Remove(processed)
				file = processed
			}
		}
	}

	jobID, err := e.printJob(ctx, body.ElementID, file, body.PrinterURI, body.Copies)
	if err != nil {
		slog.Warn("printer: print job failed",
			"project", project,
			"element", body.ElementID,
			"printer", body.PrinterURI,
			"copies", body.Copies,
			"error", err,
		)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(printError{Error: err.Error()})
		return
	}

	resp := printResponse{
		JobID:  jobID,
		Status: "queued",
	}
	json.NewEncoder(w).Encode(resp)
}
