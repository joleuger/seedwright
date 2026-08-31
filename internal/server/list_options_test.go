package server

import (
	"net/url"
	"testing"

	"seedwright/internal/data"
)

func parseForTest(rawQuery string, defaultPerPage int) data.ListOptions {
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		panic(err)
	}
	return parseListOptions(q, defaultPerPage)
}

func TestParseListOptions_Defaults(t *testing.T) {
	opts := parseForTest("", 24)

	if opts.Page != 1 {
		t.Errorf("page = %d, want 1", opts.Page)
	}
	if opts.PerPage != 24 {
		t.Errorf("per_page = %d, want default 24", opts.PerPage)
	}
	if opts.Sort != "created_at" || opts.Order != "desc" {
		t.Errorf("sort/order = %q/%q, want created_at/desc", opts.Sort, opts.Order)
	}
	if len(opts.Filters) != 0 {
		t.Errorf("filters = %v, want empty", opts.Filters)
	}
}

func TestParseListOptions_PerPageVariants(t *testing.T) {
	tests := []struct {
		query  string
		def    int
		want   int
	}{
		{"per_page=24", 50, 24},
		{"per_page=50", 24, 50},
		// Regression: the old Validate() clamped 200 back to 50, which
		// silently broke the gallery's "200" option.
		{"per_page=200", 24, 200},
		{"per_page=500", 24, 500}, // arbitrary ints are honored (elements API)
		{"per_page=all", 24, data.MaxPerPage},
		{"per_page=999999", 24, data.MaxPerPage}, // clamped to the cap
		{"per_page=0", 24, 50},                   // invalid → Validate default
		{"per_page=abc", 24, 24},                 // unparseable → caller default
		{"", 50, 50},                             // absent → caller default
	}

	for _, tt := range tests {
		opts := parseForTest(tt.query, tt.def)
		if opts.PerPage != tt.want {
			t.Errorf("parseListOptions(%q, %d).PerPage = %d, want %d", tt.query, tt.def, opts.PerPage, tt.want)
		}
	}
}

func TestParseListOptions_PageSortOrderOrigin(t *testing.T) {
	opts := parseForTest("page=3&sort=seed&order=asc&origin=external", 50)

	if opts.Page != 3 {
		t.Errorf("page = %d, want 3", opts.Page)
	}
	if opts.Sort != "seed" || opts.Order != "asc" {
		t.Errorf("sort/order = %q/%q, want seed/asc", opts.Sort, opts.Order)
	}
	if opts.Origin != "external" {
		t.Errorf("origin = %q, want external", opts.Origin)
	}
}

func TestParseListOptions_InvalidSortOrderReset(t *testing.T) {
	opts := parseForTest("sort=bogus&order=diagonal", 50)

	if opts.Sort != "created_at" {
		t.Errorf("sort = %q, want reset to created_at", opts.Sort)
	}
	if opts.Order != "desc" {
		t.Errorf("order = %q, want reset to desc", opts.Order)
	}
}

func TestParseListOptions_FavoritesFilter(t *testing.T) {
	on := parseForTest("favorites=true", 50)
	if on.Filters["favorites"] != "1" {
		t.Errorf("filters[favorites] = %q, want %q", on.Filters["favorites"], "1")
	}

	off := parseForTest("favorites=false", 50)
	if _, present := off.Filters["favorites"]; present {
		t.Errorf("filters[favorites] present = %q, want absent when not true", off.Filters["favorites"])
	}
}

func TestParseListOptions_GenericFilterPassThrough(t *testing.T) {
	// Unknown params flow into Filters for the query builder registry
	// (registered filters apply, unregistered names are ignored there).
	opts := parseForTest("myext_filter=42&other=xyz", 50)

	if opts.Filters["myext_filter"] != "42" {
		t.Errorf("filters[myext_filter] = %q, want 42", opts.Filters["myext_filter"])
	}
	if opts.Filters["other"] != "xyz" {
		t.Errorf("filters[other] = %q, want xyz", opts.Filters["other"])
	}

	// Known params must NOT leak into Filters.
	for _, key := range []string{"page", "per_page", "sort", "order", "origin", "favorites"} {
		if _, present := opts.Filters[key]; present {
			t.Errorf("filters[%q] present, want absent for known params", key)
		}
	}
}

func TestPerPageLabel(t *testing.T) {
	if got := perPageLabel(data.MaxPerPage); got != "all" {
		t.Errorf("perPageLabel(MaxPerPage) = %q, want all", got)
	}
	if got := perPageLabel(24); got != "24" {
		t.Errorf("perPageLabel(24) = %q, want 24", got)
	}
}
