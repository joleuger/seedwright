// Package querybuilder provides an extensible query construction system.
//
// The core repository builds a base query (FROM, SELECT core columns, WHERE,
// ORDER, LIMIT, OFFSET). Extensions register with the Registry to contribute:
//
//   - Filters: additional WHERE conditions (e.g. ext_joleuger_favorites_is_favorite = ?)
//   - Joins: JOIN clauses with alias deduplication
//   - Columns: additional SELECT columns (e.g. ext_joleuger_favorites_is_favorite)
//
// This keeps the core repository free of extension-specific SQL while allowing
// a single ListElements endpoint to combine arbitrary filters, joins, and
// projections from multiple extensions independently.
//
// The pattern is the Specification/Query Object pattern applied to SQL generation.
package querybuilder

import (
	"fmt"
	"strings"
	"sync"
)

// Query holds the state of a single SQL query being constructed.
// Core sets the FROM clause and base columns; extensions contribute
// WHERE fragments, JOIN clauses, and additional SELECT columns.
type Query struct {
	// Base fields (set by core).
	From   string
	BaseSelect string // e.g. "e.id, e.version, e.project, ..."
	OrderBy string
	OrderDirection string
	Limit  int
	Offset int

	// Extension contributions (set by registry).
	Where   []string
	WhereArgs []any
	Joins   []Join
	Columns []string // additional columns appended to SELECT
}

// AddSelect adds additional columns to the SELECT clause.
// The first call sets the clause; subsequent calls append.
func (q *Query) AddSelect(col string) {
	if col == "" {
		return
	}
	if q.Columns == nil {
		q.Columns = []string{col}
	} else {
		q.Columns = append(q.Columns, col)
	}
}

// AddWhere appends a WHERE condition and its arguments.
func (q *Query) AddWhere(cond string, args ...any) {
	if cond == "" {
		return
	}
	q.Where = append(q.Where, cond)
	q.WhereArgs = append(q.WhereArgs, args...)
}

// Join holds a single JOIN clause with an alias for deduplication.
type Join struct {
	// SQL is the full JOIN clause, e.g. "JOIN favorites f ON f.element_id = e.id".
	SQL string
	// Alias is the table alias for deduplication, e.g. "favorites".
	Alias string
}

// IsDuplicate checks if a join with this alias has already been registered.
func (j Join) IsDuplicate(existing []Join) bool {
	for _, e := range existing {
		if e.Alias == j.Alias {
			return true
		}
	}
	return false
}

// Filter represents a single filter contribution from an extension.
// It applies a WHERE condition to the query.
type Filter struct {
	// Name identifies the filter (e.g. "favorites").
	Name string
	// Apply is called when the filter is active. It receives the query
	// and the raw filter value as an interface{}. The implementation is
	// responsible for converting the value to the appropriate SQL.
	Apply func(q *Query, value any)
}

// Builder provides the Registry API that extensions register with.
// It collects filters, joins, and columns from all extensions.
type Builder struct {
	filters map[string]Filter
	joins   []Join
	mu      sync.Mutex
}

// NewBuilder creates an empty query builder.
func NewBuilder() *Builder {
	return &Builder{
		filters: make(map[string]Filter),
	}
}

// AddFilter registers a filter contribution.
// Call from each extension's initialization (e.g. favorites.Register(registry)).
// Filters are deduplicated by name — the last registration wins.
func (b *Builder) AddFilter(f Filter) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.filters[f.Name] = f
}

// AddJoin registers a JOIN clause if one with the same alias is not
// already registered. Returns true if the join was added, false if skipped.
func (b *Builder) AddJoin(j Join) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if j.IsDuplicate(b.joins) {
		return false
	}
	b.joins = append(b.joins, j)
	return true
}

// AddColumn registers an additional SELECT column.
func (b *Builder) AddColumn(col string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.AddColumnUnlocked(col)
}

// AddColumnUnlocked adds a column without locking (caller must hold the lock).
func (b *Builder) AddColumnUnlocked(col string) {
	if col == "" {
		return
	}
	b.joins = append(b.joins, Join{
		SQL:   fmt.Sprintf(", %s", col),
		Alias: "_column", // sentinel for column deduplication
	})
}

// ApplyFilters applies all registered filters whose keys are present in params.
// params maps filter names to their raw values (usually from URL query parameters).
func (b *Builder) ApplyFilters(q *Query, params map[string]string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for name, value := range params {
		if f, ok := b.filters[name]; ok && value != "" {
			f.Apply(q, value)
		}
	}
}

// ApplyJoins applies all registered joins, deduplicating by alias.
// Skips "_column" sentinels added by AddColumn — those are handled by ApplyColumns.
func (b *Builder) ApplyJoins(q *Query) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, j := range b.joins {
		if j.Alias == "_column" {
			continue
		}
		if !j.IsDuplicate(q.Joins) {
			q.Joins = append(q.Joins, j)
		}
	}
}

// ApplyColumns applies all registered columns.
func (b *Builder) ApplyColumns(q *Query) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, col := range b.joins {
		if col.Alias == "_column" {
			// Strip the ", " prefix since core will handle the full SELECT.
			if strings.HasPrefix(col.SQL, ", ") {
				q.Columns = append(q.Columns, strings.TrimPrefix(col.SQL, ", "))
			}
		}
	}
}

// BuildSQLAssembles the complete query SQL from the query state and builder contributions.
// The returned SQL and args are ready to execute.
//
// The caller (core repository) sets the FROM, base SELECT, ORDER BY, LIMIT, and OFFSET.
// The builder contributes WHERE conditions, JOIN clauses, and additional columns.
func BuildSQLAssembled(q *Query) (string, []any) {
	// Build the full SELECT clause.
	selectClause := q.BaseSelect
	for _, col := range q.Columns {
		selectClause += fmt.Sprintf(", %s", col)
	}

	// Build the FROM clause with joins.
	fromClause := q.From
	for _, j := range q.Joins {
		fromClause += fmt.Sprintf("\n\t%s", j.SQL)
	}

	// Build the WHERE clause.
	var whereClause string
	var whereArgs []any
	if len(q.Where) > 0 {
		whereClause = " WHERE " + strings.Join(q.Where, " AND ")
		whereArgs = q.WhereArgs
	}

	sql := fmt.Sprintf(`
		SELECT %s
		FROM %s
		%s
		%s %s
		LIMIT %d OFFSET %d`,
		selectClause,
		fromClause,
		whereClause,
		q.OrderBy,
		q.OrderDirection,
		q.Limit,
		q.Offset,
	)

	return sql, whereArgs
}
