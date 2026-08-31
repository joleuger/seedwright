package batch

import (
	"embed"
	"html/template"
)

//go:embed batch.html
var batchFS embed.FS

// BatchTemplate returns the parsed batch progress page template.
func BatchTemplate() *template.Template {
	return template.Must(template.New("batch.html").ParseFS(batchFS, "batch.html"))
}
