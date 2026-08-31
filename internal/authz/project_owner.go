package authz

import "context"

// ProjectOwnerUpdater is a minimal interface implemented by ProjectRepository
// so that the static_enforcer can set primary_owner on projects without
// importing the internal/data package (which would create a circular import).
//
// The data-plane enforcer doesn't need full project CRUD — just the ability
// to record "this principal owns the project" when a control-plane claim
// succeeds.
type ProjectOwnerUpdater interface {
	// UpdateProjectPrimaryOwner sets the primary_owner column for all
	// projects matching the given project names. If names is empty, sets
	// primary_owner on all projects in the database.
	UpdateProjectPrimaryOwner(ctx context.Context, names []string, owner Principal) error
}
