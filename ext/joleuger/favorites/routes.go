package favorites

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"seedwright/internal/app"
)

// RegisterRoutes registers Favorites' HTTP routes on the server mux.
// Favorites toggle is a JSON endpoint under /api/.
func (e *Extension) RegisterRoutes(a *app.App) {
	e.mux.HandleFunc("POST /api/{project}/favorites/toggle", e.handleToggleFavorite)
	slog.Debug("favorites: registered routes", "count", 1,
		"routes", []string{"POST /api/{project}/favorites/toggle"})
}

// handleToggleFavorite handles POST /{project}/favorites/toggle.
// JSON API: toggles favorite status, returns {"favorite": true/false}.
func (e *Extension) handleToggleFavorite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	project := r.PathValue("project")
	// Read element_id from form or JSON body.
	var elementID string
	if r.Header.Get("Content-Type") == "application/json" {
		var body struct {
			ElementID string `json:"element_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		elementID = body.ElementID
	} else {
		elementID = r.FormValue("element_id")
	}
	if elementID == "" {
		http.Error(w, "element_id required", http.StatusBadRequest)
		return
	}

	isFav, err := e.ToggleFavorite(r.Context(), elementID, project)
	if err != nil {
		slog.Error("favorites: toggle", "element", elementID, "error", err)
		http.Error(w, "failed to toggle favorite", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"favorite": isFav,
		"icon":     map[bool]string{true: "⭐", false: "☆"}[isFav],
	})
}

