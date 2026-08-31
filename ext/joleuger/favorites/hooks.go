package favorites

import (
	"context"
	"html/template"
	"log/slog"
)

// NavBar renders the Favorites navigation link for the project nav bar.
// Registered via the NavBarItems hook.
// Navigates to the gallery with the favorites filter active.
func (e *Extension) NavBar(ctx context.Context, project string) (template.HTML, error) {
	count, err := e.CountFavorites(ctx, project)
	if err != nil {
		slog.Warn("favorites: count", "project", project, "error", err)
		count = 0
	}
	label := "Favorites"
	if count > 0 {
		label = "Favorites (" + itoa(count) + ")"
	}
	return template.HTML(`<a href="/basic/` + project + `/gallery?favorites=true" title="` + label + `">` + label + `</a>`), nil
}

// ElementActions renders the star/favorite toggle button on the
// element detail page. Registered via the ElementActions hook.
func (e *Extension) ElementActions(ctx context.Context, project, elementID string) (template.HTML, error) {
	isFav, err := e.IsFavorite(ctx, project, elementID)
	if err != nil {
		slog.Warn("favorites: is_favorite", "element", elementID, "error", err)
		isFav = false
	}

	icon := "☆"
	if isFav {
		icon = "⭐"
	}

	return template.HTML(`<button class="btn btn-secondary" onclick="toggleFavorite('` + project + `', '` + elementID + `', this)" title="Toggle favorite" style="padding:0.4rem 0.8rem;">` + icon + ` Favorite</button>`), nil
}

// itoa converts int to string without fmt dependency.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
