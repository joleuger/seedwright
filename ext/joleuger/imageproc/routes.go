package imageproc

import (
	"log/slog"
	"net/http"

	"seedwright/internal/app"
	"seedwright/internal/authz"
)

// RegisterRoutes registers the imageproc extension's API endpoints
// on the application's serve mux. All routes require view access —
// processing an image presupposes being able to see it.
func (e *Extension) RegisterRoutes(a *app.App) {
	e.mux.Handle("POST /api/{project}/ext/joleuger/imageproc/preview",
		authz.RequireAction(authz.ActionView, a.Authz)(http.HandlerFunc(e.previewHandler)))
	e.mux.Handle("GET /api/{project}/ext/joleuger/imageproc/info",
		authz.RequireAction(authz.ActionView, a.Authz)(http.HandlerFunc(e.infoHandler)))
	slog.Debug("imageproc: registered routes", "count", 2,
		"routes", []string{
			"POST /api/{project}/ext/joleuger/imageproc/preview",
			"GET /api/{project}/ext/joleuger/imageproc/info",
		})
}
