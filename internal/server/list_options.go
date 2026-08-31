package server

import (
	"net/url"
	"strconv"

	"seedwright/internal/data"
)

// listParamsKnown are the query keys parseListOptions consumes directly.
// Every other key is passed through to ListOptions.Filters; the query
// builder registry decides what to do with it (registered filters apply,
// unregistered names are ignored by ApplyFilters). This keeps the gallery
// and the elements API future-proof: a new extension filter flows through
// without touching either handler.
var listParamsKnown = map[string]bool{
	"page":      true,
	"per_page":  true,
	"sort":      true,
	"order":     true,
	"origin":    true,
	"favorites": true,
}

// parseListOptions converts gallery-style URL query params into
// ListOptions. It is the single source of truth for list filter
// semantics, shared by the HTML gallery and the JSON elements API.
//
// defaultPerPage applies when the per_page param is absent or unparseable
// (the gallery defaults to 24, the API to 50).
func parseListOptions(q url.Values, defaultPerPage int) data.ListOptions {
	opts := data.DefaultListOptions()
	if defaultPerPage > 0 {
		opts.PerPage = defaultPerPage
	}

	if page, err := strconv.Atoi(q.Get("page")); err == nil {
		opts.Page = page
	}
	if pp := q.Get("per_page"); pp != "" {
		if pp == "all" {
			opts.PerPage = data.MaxPerPage
		} else if n, err := strconv.Atoi(pp); err == nil {
			opts.PerPage = n
		}
	}
	if s := q.Get("sort"); s != "" {
		opts.Sort = s
	}
	if o := q.Get("order"); o != "" {
		opts.Order = o
	}
	opts.Origin = q.Get("origin")

	opts.Filters = make(map[string]string)
	if q.Get("favorites") == "true" {
		opts.Filters["favorites"] = "1"
	}
	// Generic pass-through for extension-registered filters.
	for key, values := range q {
		if listParamsKnown[key] || len(values) == 0 {
			continue
		}
		opts.Filters[key] = values[0]
	}

	opts.Validate()
	return opts
}

// perPageLabel renders a parsed PerPage back into the URL token the gallery
// select uses ("24" / "50" / "200" / "all").
func perPageLabel(perPage int) string {
	if perPage == data.MaxPerPage {
		return "all"
	}
	return strconv.Itoa(perPage)
}
