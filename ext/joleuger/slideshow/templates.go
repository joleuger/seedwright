package slideshow

import (
	"embed"
	"html/template"
)

//go:embed slideshow.html
var slideshowFS embed.FS

// SlideShowTemplate returns the parsed fullscreen slideshow page template.
func SlideShowTemplate() *template.Template {
	return template.Must(template.New("slideshow.html").ParseFS(slideshowFS, "slideshow.html"))
}
