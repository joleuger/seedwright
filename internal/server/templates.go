package server

import (
	"embed"
	"html/template"
	"math"
	"strings"
	"sync"

	"seedwright/internal/data/model"
)

//go:embed templates/*.html
var templateFS embed.FS

// currentPrefix is set per-request in render() so that the
// urlPath/prefix FuncMap entries can produce prefix-aware URLs.
var (
	currentPrefix    string
	prefixMu         sync.Mutex
	enabledExtensions []string
	extMu            sync.RWMutex
)

// SetEnabledExtensions sets the list of registered extension keys.
// Called once after ext.RegisterAll. Templates and JS can query this
// at render time (templates) or page load time (JS).
func SetEnabledExtensions(keys []string) {
	extMu.Lock()
	defer extMu.Unlock()
	enabledExtensions = keys
}

// isExtensionEnabled checks whether a given extension key is enabled.
// Thread-safe — reads from a RWMutex-protected slice.
func isExtensionEnabled(key string) bool {
	extMu.RLock()
	defer extMu.RUnlock()
	for _, k := range enabledExtensions {
		if k == key {
			return true
		}
	}
	return false
}

// loadTemplates parses all embedded templates.
// Each file becomes a top-level template named after the file (e.g., "welcome.html").
//
// The "prefix" and "urlPath" template functions read from currentPrefix
// (set per-request in render()).
func loadTemplates() *template.Template {
	return template.Must(template.New("").Funcs(template.FuncMap{
		"prefix": func() string {
			prefixMu.Lock()
			defer prefixMu.Unlock()
			return currentPrefix
		},
		"urlPath": func(path string) string {
			prefixMu.Lock()
			defer prefixMu.Unlock()
			if currentPrefix == "" {
				return path
			}
			return currentPrefix + path
		},
		"hasExtension": func(key string) bool {
			return isExtensionEnabled(key)
		},
		"upper": strings.ToUpper,
		"title": strings.Title,
		"safeHTML": func(v any) template.HTML {
			switch s := v.(type) {
			case string:
				return template.HTML(s)
			case template.HTML:
				return s
			default:
				return template.HTML("")
			}
		},
		"safeJSON": func(s string) template.HTML {
			// Escape for use inside a JSON string literal, returned as template.HTML
			// so the template engine does NOT re-escape it.
			s = strings.ReplaceAll(s, "\\", "\\\\")
			s = strings.ReplaceAll(s, `"`, `\"`)
			s = strings.ReplaceAll(s, "\n", `\n`)
			s = strings.ReplaceAll(s, "\r", `\r`)
			s = strings.ReplaceAll(s, "\t", `\t`)
			return template.HTML(s)
		},
		"unsafeJSON": func(s string) template.HTML {
			// Return raw JSON as template.HTML. The JSON is already safe
			// (no user-controlled HTML). Returned as template.HTML to
			// prevent the template engine from re-escaping it.
			return template.HTML(s)
		},
		"safeJSONArray": func(items []string) template.HTML {
			if len(items) == 0 {
				return template.HTML("[]")
			}
			var b strings.Builder
			b.WriteString("[")
			for i, item := range items {
				if i > 0 {
					b.WriteString(",")
				}
				b.WriteString(`"`)
				escaped := strings.ReplaceAll(item, `\`, `\\`)
				escaped = strings.ReplaceAll(escaped, `"`, `\"`)
				b.WriteString(escaped)
				b.WriteString(`"`)
			}
			b.WriteString("]")
			return template.HTML(b.String())
		},
		"div": func(a, b int) int {
			return int(math.Floor(float64(a) / float64(b)))
		},
		"mod": func(a, b int) int {
			return a % b
		},
		"add": func(a, b int) int {
			return a + b
		},
		"escape": func(s string) template.HTML {
			return template.HTML(strings.TrimSpace(s))
		},
		"trim": strings.TrimSpace,
		"getField": func(elem any, key string) any {
			if e, ok := elem.(*model.Element); ok {
				return e.Field(key)
			}
			return nil
		},
		"getFieldInt": func(elem any, key string) int {
			if e, ok := elem.(*model.Element); ok {
				if v := e.Field(key); v != nil {
					if i, ok := v.(int); ok {
						return i
					}
				}
			}
			return 0
		},
	}).ParseFS(templateFS, "templates/*.html"))
}
