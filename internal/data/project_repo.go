package data

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"time"

	"seedwright/internal/authz"
	"seedwright/internal/data/model"
	"seedwright/internal/storage"
)

// projectRepo implements ProjectRepository using SQLite and S3.
type projectRepo struct {
	db      *sql.DB
	storage storage.StorageBackend
}

// NewProjectRepository creates a new ProjectRepository.
func NewProjectRepository(db *sql.DB, store storage.StorageBackend) ProjectRepository {
	return &projectRepo{db: db, storage: store}
}

func (r *projectRepo) CreateProject(ctx context.Context, pm model.ProjectSettings) error {
	if r.storage == nil {
		return fmt.Errorf("storage not available")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Write project.json to S3 first.
	psData, err := json.Marshal(pm)
	if err != nil {
		return fmt.Errorf("marshal project settings: %w", err)
	}
	if err := r.storage.PutObject(ctx, pm.ProjectSettingsS3Key(), bytes.NewReader(psData), int64(len(psData)), "application/json"); err != nil {
		return fmt.Errorf("write project settings to S3: %w", err)
	}

	// Insert into SQLite.
	_, err = tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO projects (name, schema_version, version, created_at, updated_at, synced_at, hidden, backend_ref, description, tags, friendly_name, primary_owner) VALUES (?, 1, 1, ?, ?, CURRENT_TIMESTAMP, 0, '', '', '[]', ?, '')`,
		pm.Name, pm.CreatedAt.Format(time.RFC3339), pm.UpdatedAt.Format(time.RFC3339), pm.FriendlyName, pm.PrimaryOwner,
	)
	if err != nil {
		return fmt.Errorf("insert project: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit project: %w", err)
	}

	return nil
}

func (r *projectRepo) GetProjectSettings(ctx context.Context, name string) (model.ProjectSettings, error) {
	var schemaVersion, version int
	var createdAt, updatedAt, backendRef, description, tags, friendlyName, primaryOwner string
	var hidden int

	err := r.db.QueryRowContext(ctx,
		`SELECT name, schema_version, version, created_at, updated_at, hidden, backend_ref, description, tags, friendly_name, primary_owner FROM projects WHERE name = ?`, name,
	).Scan(&name, &schemaVersion, &version, &createdAt, &updatedAt, &hidden, &backendRef, &description, &tags, &friendlyName, &primaryOwner)
	if err != nil {
		return model.ProjectSettings{}, fmt.Errorf("get project settings %s: %w", name, err)
	}

	var tagsSlice []string
	if tags != "" {
		_ = json.Unmarshal([]byte(tags), &tagsSlice)
	}

	return model.ProjectSettings{
		Name:          name,
		SchemaVersion: schemaVersion,
		Version:       version,
		CreatedAt:     parseTime(createdAt),
		UpdatedAt:     parseTime(updatedAt),
		Hidden:        hidden != 0,
		BackendRef:    backendRef,
		Description:   description,
		Tags:          tagsSlice,
		FriendlyName:  friendlyName,
		PrimaryOwner:  primaryOwner,
	}, nil
}

func (r *projectRepo) UpdateProjectSettings(ctx context.Context, ps model.ProjectSettings) error {
	if r.storage == nil {
		return fmt.Errorf("storage not available")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Update version and updated_at.
	ps.Version++
	ps.UpdatedAt = time.Now().UTC()

	// Write project.json to S3 first.
	psData, err := json.Marshal(ps)
	if err != nil {
		return fmt.Errorf("marshal project settings: %w", err)
	}
	if err := r.storage.PutObject(ctx, ps.ProjectSettingsS3Key(), bytes.NewReader(psData), int64(len(psData)), "application/json"); err != nil {
		return fmt.Errorf("write project settings to S3: %w", err)
	}

	// Update SQLite.
	tagsBytes, _ := json.Marshal(ps.Tags)
	_, err = tx.ExecContext(ctx,
		`UPDATE projects SET schema_version = ?, version = ?, updated_at = ?, hidden = ?, backend_ref = ?, description = ?, tags = ?, friendly_name = ?, primary_owner = ? WHERE name = ?`,
		ps.SchemaVersion, ps.Version, ps.UpdatedAt.Format(time.RFC3339),
		func() int { if ps.Hidden { return 1 }; return 0 }(),
		ps.BackendRef, ps.Description, string(tagsBytes), ps.FriendlyName, ps.PrimaryOwner, ps.Name,
	)
	if err != nil {
		return fmt.Errorf("update project settings in SQLite: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit project settings: %w", err)
	}

	return nil
}

func (r *projectRepo) ListProjects(ctx context.Context, filterHidden bool) ([]string, error) {
	var query string
	if filterHidden {
		query = `SELECT name FROM projects WHERE hidden = 0 ORDER BY name`
	} else {
		query = `SELECT name FROM projects ORDER BY name`
	}

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query projects: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		names = append(names, name)
	}
	return names, nil
}

func (r *projectRepo) GetProjectMeta(ctx context.Context, name string) (model.ProjectMeta, error) {
	var id, createdAt, backendRef, primaryOwner string
	var hidden int
	err := r.db.QueryRowContext(ctx,
		`SELECT name, created_at, hidden, backend_ref, primary_owner FROM projects WHERE name = ?`, name,
	).Scan(&name, &createdAt, &hidden, &backendRef, &primaryOwner)
	if err != nil {
		return model.ProjectMeta{}, fmt.Errorf("get project %s: %w", name, err)
	}

	return model.ProjectMeta{
		Name:         name,
		ID:           id,
		CreatedAt:    parseTime(createdAt),
		Hidden:       hidden != 0,
		BackendRef:   backendRef,
		PrimaryOwner: primaryOwner,
	}, nil
}

func (r *projectRepo) UpdateProjectHidden(ctx context.Context, name string, hidden bool) error {
	val := 0
	if hidden {
		val = 1
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE projects SET hidden = ? WHERE name = ?`, val, name,
	)
	if err != nil {
		return fmt.Errorf("update project hidden: %w", err)
	}
	return nil
}

func (r *projectRepo) UpdateProjectBackend(ctx context.Context, name, backendRef string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE projects SET backend_ref = ? WHERE name = ?`, backendRef, name,
	)
	if err != nil {
		return fmt.Errorf("update project backend: %w", err)
	}
	return nil
}

func (r *projectRepo) UpdateProjectMetaJSON(ctx context.Context, name string, data []byte) error {
	if r.storage == nil {
		return fmt.Errorf("storage not available")
	}

	pm, err := model.FromProjectJSON(data)
	if err != nil {
		return fmt.Errorf("parse project meta: %w", err)
	}

	if err := r.storage.PutObject(ctx, pm.ProjectS3Key(), bytes.NewReader(data), int64(len(data)), "application/json"); err != nil {
		return fmt.Errorf("write project meta to S3: %w", err)
	}

	// Update SQLite hidden/backend_ref from the new meta.
	_, err = r.db.ExecContext(ctx,
		`UPDATE projects SET hidden = ?, backend_ref = ? WHERE name = ?`,
		func() int { if pm.Hidden { return 1 }; return 0 }(),
		pm.BackendRef, name,
	)
	if err != nil {
		return fmt.Errorf("update project meta in SQLite: %w", err)
	}

	return nil
}

// UpdateProjectPrimaryOwner sets the primary_owner column on all projects.
// If names is nil or empty, sets primary_owner on all projects.
//
// Implements authz.ProjectOwnerUpdater so the static enforcer can call
// it directly via ownership claim.
func (r *projectRepo) UpdateProjectPrimaryOwner(ctx context.Context, names []string, owner authz.Principal) error {
	if len(names) == 0 {
		_, err := r.db.ExecContext(context.Background(),
			`UPDATE projects SET primary_owner = ? WHERE 1=1`, string(owner))
		return err
	}
	tx, err := r.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(context.Background(),
		`UPDATE projects SET primary_owner = ? WHERE name = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, name := range names {
		_, err := stmt.ExecContext(context.Background(), string(owner), name)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *projectRepo) IncrementSyncCount(ctx context.Context, name string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE projects SET synced_at = CURRENT_TIMESTAMP WHERE name = ?`, name,
	)
	if err != nil {
		return fmt.Errorf("increment sync count: %w", err)
	}
	return nil
}

func (r *projectRepo) DeleteProject(ctx context.Context, name string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Delete all S3 objects under the project prefix.
	if r.storage != nil {
		objects, err := r.storage.ListObjects(ctx, fmt.Sprintf("projects/%s/", name))
		if err != nil {
			slog.Warn("list project objects for delete", "project", name, "error", err)
		} else {
			for _, obj := range objects {
				if err := r.storage.DeleteObject(ctx, obj.Key); err != nil {
					slog.Warn("delete S3 object", "key", obj.Key, "error", err)
				}
			}
		}
	}

	// 2. Delete all jobs for elements in this project from SQLite.
	_, err = tx.ExecContext(ctx,
		`DELETE FROM jobs WHERE project = ?`, name,
	)
	if err != nil {
		return fmt.Errorf("delete jobs for project %s: %w", name, err)
	}

	// 3. Delete all elements for this project from SQLite.
	_, err = tx.ExecContext(ctx,
		`DELETE FROM elements WHERE project = ?`, name,
	)
	if err != nil {
		return fmt.Errorf("delete elements for project %s: %w", name, err)
	}

	// 4. Delete the project from SQLite.
	_, err = tx.ExecContext(ctx,
		`DELETE FROM projects WHERE name = ?`, name,
	)
	if err != nil {
		return fmt.Errorf("delete project %s: %w", name, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete project: %w", err)
	}

	return nil
}

func (r *projectRepo) GetExtensionSettings(ctx context.Context, project, owner, extension string) (model.ProjectSettingsDelta, error) {
	if r.storage == nil {
		return model.ProjectSettingsDelta{}, nil
	}

	s3Key := model.ProjectSettingsDeltaS3Key(project, owner, extension)
	obj, _, err := r.storage.GetObject(ctx, s3Key)
	if err != nil {
		// No delta file exists — return empty delta (default state)
		return model.ProjectSettingsDelta{}, nil
	}
	defer obj.Close()

	data, err := io.ReadAll(obj)
	if err != nil {
		return model.ProjectSettingsDelta{}, fmt.Errorf("read extension settings %s: %w", s3Key, err)
	}

	return model.ProjectSettingsDeltaFromJSON(data)
}

func (r *projectRepo) UpdateExtensionSettings(ctx context.Context, project, owner, extension string, delta model.ProjectSettingsDelta) error {
	if r.storage == nil {
		return fmt.Errorf("storage not available")
	}

	// Build S3 key from project, owner, and extension.
	s3Key := model.ProjectSettingsDeltaS3Key(project, owner, extension)

	// Increment version.
	delta.Version++

	// Write delta to S3.
	deltaData, err := json.Marshal(delta)
	if err != nil {
		return fmt.Errorf("marshal extension settings delta: %w", err)
	}

	if err := r.storage.PutObject(ctx, s3Key, bytes.NewReader(deltaData), int64(len(deltaData)), "application/json"); err != nil {
		return fmt.Errorf("write extension settings delta to S3: %w", err)
	}

	return nil
}

func (r *projectRepo) DeleteExtensionSettings(ctx context.Context, project, owner, extension string) error {
	if r.storage == nil {
		return nil
	}

	s3Key := model.ProjectSettingsDeltaS3Key(project, owner, extension)
	return r.storage.DeleteObject(ctx, s3Key)
}
