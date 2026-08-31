package data

import (
	"context"
	"database/sql"
	"io"

	"seedwright/internal/authz"
	"seedwright/internal/data/model"
	"seedwright/internal/storage"
)

// ProjectRepository handles CRUD for project metadata.
// Projects are S3-backed via project.json at projects/{name}/project.json.
// The projects SQLite table mirrors this data; source of truth is S3.
type ProjectRepository interface {
	// CreateProject inserts a project row into SQLite and writes project.json to S3.
	CreateProject(ctx context.Context, pm model.ProjectSettings) error

	// ListProjects returns all known project names (excludes hidden by default).
	ListProjects(ctx context.Context, filterHidden bool) ([]string, error)

	// GetProjectMeta returns the project row from SQLite.
	GetProjectMeta(ctx context.Context, name string) (model.ProjectMeta, error)

	// GetProjectSettings returns project settings from SQLite.
	GetProjectSettings(ctx context.Context, name string) (model.ProjectSettings, error)

	// UpdateProjectHidden sets the hidden flag for a project.
	UpdateProjectHidden(ctx context.Context, name string, hidden bool) error

	// UpdateProjectBackend sets the backend reference for a project.
	UpdateProjectBackend(ctx context.Context, name, backendRef string) error

	// UpdateProjectSettings writes updated project settings to S3 project.json and SQLite.
	UpdateProjectSettings(ctx context.Context, ps model.ProjectSettings) error

	// IncrementSyncCount increments the sync count for a project.
	IncrementSyncCount(ctx context.Context, name string) error

	// DeleteProject removes a project from SQLite and all its S3 objects.
	DeleteProject(ctx context.Context, name string) error

	// GetExtensionSettings returns an extension's project settings delta file.
	// Returns model.ProjectSettingsDelta{} with nil Fields if no delta file exists.
	GetExtensionSettings(ctx context.Context, project, owner, extension string) (model.ProjectSettingsDelta, error)

	// UpdateExtensionSettings writes an extension's project settings delta to S3.
	// The S3 key is derived from project, owner, and extension.
	UpdateExtensionSettings(ctx context.Context, project, owner, extension string, delta model.ProjectSettingsDelta) error

	// DeleteExtensionSettings removes an extension's project settings delta from S3.
	DeleteExtensionSettings(ctx context.Context, project, owner, extension string) error

	// UpdateProjectPrimaryOwner sets the primary_owner column on all
	// projects matching the given names. If names is empty, updates all
	// projects. Called by the static enforcer during ownership claim.
	UpdateProjectPrimaryOwner(ctx context.Context, names []string, owner authz.Principal) error
}

// ElementRepository handles CRUD for generation elements.
type ElementRepository interface {
	// CreateElement stores the element JSON and image in S3, then updates SQLite.
	CreateElement(ctx context.Context, elem model.Element, image io.ReadCloser, imageSize int64) error

	// GetElement returns an element by ID.
	GetElement(ctx context.Context, id string) (model.Element, error)

	// ListElements returns a paginated list of elements for a project.
	ListElements(ctx context.Context, project string, opts ListOptions) ([]model.Element, int, error)

	// ListRecentPrompts returns the last N distinct prompts for a project, ordered by most recent.
	ListRecentPrompts(ctx context.Context, project string, limit int) ([]string, error)

	// DeleteElement removes an element (JSON, image, jobs) from S3 and SQLite.
	DeleteElement(ctx context.Context, id, project string) error

	// SyncFromStorage scans S3 and updates SQLite to match.
	SyncFromStorage(ctx context.Context) error

	// ListReferencingElements returns elements that reference the given element
	// in their generation.reference_images (img2img inputs).
	ListReferencingElements(ctx context.Context, refElementID string) ([]model.Element, error)
}

// MaxPerPage is the hard cap on ListOptions.PerPage. The gallery's "all"
// option and unbounded API requests use this value.
const MaxPerPage = 10000

// ListOptions controls pagination and sorting for ListElements.
// Extension filters (e.g. "favorites" for favorites toggle) are passed
// via the Filters map — the handler populates this from URL query params
// and extensions consume them via the QueryBuilder registry.
type ListOptions struct {
	Page    int
	PerPage int
	Sort    string // "created_at", "seed", "model_name"
	Order   string // "asc", "desc"
	Origin  string // core filter: "generated", "uploaded", "ext/..." — empty means all
	Filters map[string]string // extension filters: name → raw value
}

// DefaultListOptions returns safe defaults for ListOptions.
func DefaultListOptions() ListOptions {
	return ListOptions{
		Page:    1,
		PerPage: 50,
		Sort:    "created_at",
		Order:   "desc",
	}
}

// Validate ensures the options are within bounds.
// PerPage accepts any value in [1, MaxPerPage] (the gallery's 24/50/200/all
// all pass through) and is clamped into that range.
func (o *ListOptions) Validate() {
	if o.Page < 1 {
		o.Page = 1
	}
	if o.PerPage < 1 {
		o.PerPage = 50
	} else if o.PerPage > MaxPerPage {
		o.PerPage = MaxPerPage
	}
	if o.Sort != "created_at" && o.Sort != "seed" && o.Sort != "model_name" {
		o.Sort = "created_at"
	}
	if o.Order != "asc" && o.Order != "desc" {
		o.Order = "desc"
	}
}

// TotalPages calculates the number of pages for the given total count.
func (o ListOptions) TotalPages(total int) int {
	if o.PerPage == 0 {
		return 0
	}
	pages := total / o.PerPage
	if total%o.PerPage > 0 {
		pages++
	}
	return pages
}

// Offset returns the SQL offset for this page.
func (o ListOptions) Offset() int {
	return (o.Page - 1) * o.PerPage
}

// StorageBackend wraps both StorageBackend and SQLite for repository implementations.
// Note: named "StorageBackend" (the wrapper) — the interface is also StorageBackend,
// but they live in different packages so there's no collision.
type StorageBackend struct {
	Backend storage.StorageBackend
	SQLite  *sql.DB
}

// NewStorageBackend creates a wrapper from the configured storage client and database.
func NewStorageBackend(backend storage.StorageBackend, db *sql.DB) *StorageBackend {
	return &StorageBackend{
		Backend: backend,
		SQLite:  db,
	}
}
