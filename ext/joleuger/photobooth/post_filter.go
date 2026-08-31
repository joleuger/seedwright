package photobooth

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"strings"

	"seedwright/internal/app"
	"seedwright/internal/data/model"
	"seedwright/internal/storage"
)


// deltaKey returns the S3 key for a project's post_filter delta file.
func deltaKey(project string) string {
	return "projects/" + project + "/ext/joleuger/photobooth/settings.json"
}

// deltaPrefix returns the S3 prefix for all delta files in a project.
func deltaPrefix(project string) string {
	return "projects/" + project + "/ext/joleuger/photobooth/"
}

// GetPostFilter reads the post_filter settings for a project from the
// SQLite column (populated by Sync at startup). Uses pointer scanning
// to handle NULL columns without sql.Scan errors.
func (e *Extension) GetPostFilter(ctx context.Context, project string) (prompt, referenceImage string, err error) {
	var p, ri *string
	err = e.db.QueryRowContext(ctx,
		`SELECT ext_joleuger_photobooth_post_filter_prompt,
		        ext_joleuger_photobooth_post_filter_reference_image
		 FROM projects WHERE name = ?`,
		project,
	).Scan(&p, &ri)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	if p != nil {
		prompt = *p
	}
	if ri != nil {
		referenceImage = *ri
	}
	return prompt, referenceImage, nil
}

// GetTriggerBinding reads the capture trigger key binding (KeyboardEvent.code)
// from the SQLite column (populated by Sync at startup). Returns empty string
// when no binding is set.
func (e *Extension) GetTriggerBinding(ctx context.Context, project string) (code string) {
	var p *string
	err := e.db.QueryRowContext(ctx,
		`SELECT ext_joleuger_photobooth_trigger_binding FROM projects WHERE name = ?`,
		project,
	).Scan(&p)
	if err == sql.ErrNoRows || err != nil {
		return ""
	}
	if p != nil {
		return *p
	}
	return ""
}

// SetTriggerBinding writes the capture trigger key binding (KeyboardEvent.code)
// to the projects table column.  The S3 delta is updated as well so that
// the SettingsSection hook picks up the change via the scoped save flow.
func (e *Extension) SetTriggerBinding(ctx context.Context, project, code string) error {
	// 1. Update the column on the projects table.
	_, err := e.db.ExecContext(ctx,
		`UPDATE projects SET ext_joleuger_photobooth_trigger_binding = ? WHERE name = ?`,
		code, project,
	)
	if err != nil {
		return err
	}

	// 2. Read the existing S3 delta and merge the trigger_binding field.
	key := deltaKey(project)
	var d photoboothSettings
	body, _, err := e.storage.GetObject(ctx, key)
	if err == nil && body != nil {
		data, rErr := io.ReadAll(body)
		body.Close()
		if rErr == nil {
			json.Unmarshal(data, &d)
		}
	}

	// 3. Write updated delta to S3.
	if code != "" {
		d.ID = project
		d.Version++
		if d.Version == 0 {
			d.Version = 1
		}
		d.CaptureTriggerBinding = code
	} else {
		d.CaptureTriggerBinding = ""
	}
	data, _ := json.Marshal(d)
	if err := e.storage.PutObject(ctx, key, strings.NewReader(string(data)), int64(len(data)), "application/json"); err != nil {
		slog.Warn("photobooth: write trigger binding delta to S3", "project", project, "error", err)
	}

	return nil
}

// SetPostFilter sets the post_filter settings for a project.
// Write direction: S3 delta file first, then SQLite column.
func (e *Extension) SetPostFilter(ctx context.Context, project, prompt, referenceImage string) error {
	key := deltaKey(project)

	// 1. Read current delta from S3.
	var d photoboothSettings
	body, _, err := e.storage.GetObject(ctx, key)
	if err == nil && body != nil {
		data, rErr := io.ReadAll(body)
		body.Close()
		if rErr == nil {
			json.Unmarshal(data, &d)
		}
	}

	// 2. Update fields.
	d.ID = project
	d.PostFilterPrompt = prompt
	d.PostFilterReferenceImage = referenceImage
	d.Version++
	if d.Version == 0 {
		d.Version = 1
	}

	// 3. Write delta to S3.
	data, _ := json.Marshal(d)
	if err := e.storage.PutObject(ctx, key, strings.NewReader(string(data)), int64(len(data)), "application/json"); err != nil {
		return err
	}

	// 4. Update the columns on the projects table.
	_, err = e.db.ExecContext(ctx,
		`UPDATE projects SET
			ext_joleuger_photobooth_post_filter_prompt = ?,
			ext_joleuger_photobooth_post_filter_reference_image = ?
		 WHERE name = ?`,
		prompt, referenceImage, project,
	)
	return err
}

// ClearPostFilter removes the post_filter settings for a project.
// Write direction: delete S3 delta file first, then reset SQLite column.
func (e *Extension) ClearPostFilter(ctx context.Context, project string) error {
	key := deltaKey(project)

	// 1. Delete delta file from S3.
	if err := e.storage.DeleteObject(ctx, key); err != nil {
		// If the file doesn't exist, that's fine — it's already default.
		slog.Warn("photobooth: delete delta file", "key", key, "error", err)
	}

	// 2. Reset the columns on the projects table.
	_, err := e.db.ExecContext(ctx,
		`UPDATE projects SET
			ext_joleuger_photobooth_post_filter_prompt = '',
			ext_joleuger_photobooth_post_filter_reference_image = '',
			ext_joleuger_photobooth_trigger_binding = ''
		 WHERE name = ?`,
		project,
	)
	return err
}

// Sync runs after core's SyncFromStorage. It reads the post_filter delta
// file from S3 and populates the projects table columns.
func (e *Extension) Sync(ctx context.Context, a *app.App) error {
	// List all projects from SQLite (populated by core's SyncFromStorage).
	rows, err := a.DB.QueryContext(ctx, `SELECT name FROM projects`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var projects []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		projects = append(projects, name)
	}

	// Sync each project.
	for _, project := range projects {
		if err := syncPostFilterFromS3(ctx, a.Storage, a.DB, project); err != nil {
			slog.Warn("photobooth: sync failed", "project", project, "error", err)
		}
	}

	return nil
}

// syncPostFilterFromS3 loads the delta file for a project from S3 and
// populates the ext_joleuger_photobooth columns on the projects table.
func syncPostFilterFromS3(ctx context.Context, store storage.StorageBackend, db *sql.DB, project string) error {
	// List delta files for the project.
	objects, err := store.ListObjects(ctx, deltaPrefix(project))
	if err != nil {
		return err
	}

	for _, obj := range objects {
		if !strings.HasSuffix(obj.Key, ".json") || !strings.HasSuffix(obj.Key, "settings.json") {
			continue
		}

		body, _, err := store.GetObject(ctx, obj.Key)
		if err != nil {
			slog.Warn("photobooth: read delta file", "key", obj.Key, "error", err)
			continue
		}
		data, rErr := io.ReadAll(body)
		body.Close()
		if rErr != nil {
			slog.Warn("photobooth: read delta file", "key", obj.Key, "error", rErr)
			continue
		}

		var d photoboothSettings
		if err := json.Unmarshal(data, &d); err != nil {
			slog.Warn("photobooth: parse delta file", "key", obj.Key, "error", err)
			continue
		}

		// Check that the project still exists in the projects table.
		var exists int
		err = db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM projects WHERE name = ?`, project,
		).Scan(&exists)
		if err != nil || exists == 0 {
			_ = store.DeleteObject(ctx, obj.Key)
			continue
		}

		// Populate the columns.
		_, err = db.ExecContext(ctx,
			`UPDATE projects SET
				ext_joleuger_photobooth_post_filter_prompt = ?,
				ext_joleuger_photobooth_post_filter_reference_image = ?,
				ext_joleuger_photobooth_trigger_binding = ?
			 WHERE name = ?`,
			d.PostFilterPrompt, d.PostFilterReferenceImage, d.CaptureTriggerBinding, project,
		)
		if err != nil {
			slog.Warn("photobooth: update columns from S3", "project", project, "error", err)
		}
	}

	return nil
}

// settingsDeltaFromDB converts the projects table columns into a
// ProjectSettingsDelta for use by the SettingsSection hook and handleSaveImage.
// Uses pointer scanning to handle NULL columns without sql.Scan errors.
func settingsDeltaFromDB(ctx context.Context, db *sql.DB, project string) model.ProjectSettingsDelta {
	var p, ri, tb *string
	err := db.QueryRowContext(ctx,
		`SELECT ext_joleuger_photobooth_post_filter_prompt,
		        ext_joleuger_photobooth_post_filter_reference_image,
		        ext_joleuger_photobooth_trigger_binding
		 FROM projects WHERE name = ?`,
		project,
	).Scan(&p, &ri, &tb)
	if err != nil {
		return model.ProjectSettingsDelta{}
	}

	delta := model.ProjectSettingsDelta{
		ID: project,
	}
	if p != nil {
		delta.SetField("post_filter_prompt", *p)
	}
	if ri != nil {
		delta.SetField("post_filter_reference_image", *ri)
	}
	if tb != nil {
		delta.SetField("capture_trigger_binding", *tb)
	}
	return delta
}
