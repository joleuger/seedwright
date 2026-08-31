package photobooth

import (
	"embed"
	"html/template"
)

//go:embed photobooth.html photobooth_index.html
var photoboothFS embed.FS

// PhotoboothTemplate returns the parsed photobooth page template.
func PhotoboothTemplate() *template.Template {
	return template.Must(template.New("photobooth.html").ParseFS(photoboothFS, "photobooth.html"))
}

// PhotoboothIndexTemplate returns the parsed photobooth index (start page) template.
func PhotoboothIndexTemplate() *template.Template {
	return template.Must(template.New("photobooth_index.html").ParseFS(photoboothFS, "photobooth_index.html"))
}
