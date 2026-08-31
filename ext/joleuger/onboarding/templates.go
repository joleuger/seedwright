package onboarding

import (
	"embed"
	"encoding/json"
	"html/template"
	"strings"
)

//go:embed onboarding.html
var onboardingFS embed.FS

// onboardingTemplate returns the parsed Customize page template.
// Re-parsed per request, matching the photobooth convention — the
// page is a handful of clicks away from nobody.
func onboardingTemplate() (*template.Template, error) {
	return template.New("onboarding.html").
		Funcs(template.FuncMap{
			"scriptJSON": scriptJSON,
		}).
		ParseFS(onboardingFS, "onboarding.html")
}

// scriptPayload is what the page's vanilla JS receives as
// window.__ONBOARDING__.
type scriptPayload struct {
	Prefix string `json:"prefix"` // server.path_prefix, for fetch URLs

	// Write-gate state at page render time (the preview endpoint
	// re-reports it per request; this drives the initial UI state).
	ConfigExists     bool   `json:"configExists"`
	WriteAllowed     bool   `json:"writeAllowed"`
	ConfirmRequired  bool   `json:"confirmRequired"`
	EphemeralWarning string `json:"ephemeralWarning"`
}

// scriptJSON renders v as a JSON object literal safe to embed in a
// <script> tag: <, >, & and the line-separator characters are escaped
// to their \uXXXX forms (valid JSON, restored by JSON parsing).
func scriptJSON(v any) template.JS {
	b, err := json.Marshal(v)
	if err != nil {
		return template.JS("{}")
	}
	s := strings.NewReplacer(
		"<", `\u003c`,
		">", `\u003e`,
		"&", `\u0026`,
		"\u2028", `\u2028`,
		"\u2029", `\u2029`,
	).Replace(string(b))
	return template.JS(s)
}
