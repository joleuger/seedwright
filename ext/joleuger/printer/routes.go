// Package printer implements image printing via the CUPS lp command
// as an extension to seedwright.
package printer

import (
	"log/slog"
	"net/http"

	"seedwright/internal/app"
	"seedwright/internal/authz"
)

// RegisterRoutes registers the printer extension's API endpoints
// on the application's serve mux. All routes require view access —
// printing an image presupposes being able to see it.
func (e *Extension) RegisterRoutes(a *app.App) {
	e.mux.Handle("GET /api/{project}/ext/joleuger/printer/printers",
		authz.RequireAction(authz.ActionView, a.Authz)(http.HandlerFunc(e.printersHandler)))
	e.mux.Handle("POST /api/{project}/ext/joleuger/printer/preview",
		authz.RequireAction(authz.ActionView, a.Authz)(http.HandlerFunc(e.previewHandler)))
	e.mux.Handle("POST /api/{project}/ext/joleuger/printer/print",
		authz.RequireAction(authz.ActionView, a.Authz)(http.HandlerFunc(e.printHandler)))
	slog.Debug("printer: registered routes", "count", 3,
		"routes", []string{
			"GET /api/{project}/ext/joleuger/printer/printers",
			"POST /api/{project}/ext/joleuger/printer/preview",
			"POST /api/{project}/ext/joleuger/printer/print",
		})
}
