package data

import (
	"time"
)

// parseTime parses a time string in RFC3339 format, returning zero time on failure.
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// sortedQuery maps the Sort field to a safe SQL column name.
func sortedQuery(sort, order string) string {
	switch sort {
	case "created_at", "seed", "model_name":
		return sort
	default:
		return "created_at"
	}
}

// orderDirection ensures the sort direction is safe.
func orderDirection(order string) string {
	if order == "asc" {
		return "ASC"
	}
	return "DESC"
}
