package data

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"seedwright/internal/data/model"
	"seedwright/internal/data/querybuilder"
	"seedwright/internal/storage"
)

// elementRepo implements ElementRepository using SQLite (reads) and S3 (writes).
type elementRepo struct {
	db      *sql.DB
	storage storage.StorageBackend
	qb      *querybuilder.Builder
}

// NewElementRepository creates a new ElementRepository.
func NewElementRepository(db *sql.DB, store storage.StorageBackend, qb *querybuilder.Builder) ElementRepository {
	return &elementRepo{
		db:      db,
		storage: store,
		qb:      qb,
	}
}

func (r *elementRepo) CreateElement(ctx context.Context, elem model.Element, image io.ReadCloser, imageSize int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Write element JSON to S3.
	elemData, err := elem.ToJSON()
	if err != nil {
		return fmt.Errorf("marshal element: %w", err)
	}
	if err := r.storage.PutObject(ctx, elem.ElementS3Key(), bytes.NewReader(elemData), int64(len(elemData)), "application/json"); err != nil {
		return fmt.Errorf("put element JSON to S3: %w", err)
	}

	// 2. Write image to S3 (if present).
	// The element JSON stores project_location (images/{id}.png) but S3
	// needs the full key. Construct it here.
	imageS3Key := fmt.Sprintf("projects/%s/%s", elem.Project, elem.ImageProjectLocation())
	if image != nil && imageSize > 0 {
		if err := r.storage.PutObject(ctx, imageS3Key, image, imageSize, "image/png"); err != nil {
			image.Close()
			return fmt.Errorf("put image to S3: %w", err)
		}
		image.Close()
	}

	// 3. Update SQLite — extract fields from the nested Generation struct.
	g := elem.Generation
	if g == nil {
		g = &model.Generation{}
	}

	modelName := ""
	if g.Model != nil {
		modelName = g.Model.Name
	}

	// Use the duration set by the job service on Generation (persisted to S3).
	durationSQL := sql.NullFloat64{Valid: g.Duration > 0, Float64: g.Duration}

	_, err = tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO elements (
			id, version, project, created_at, project_location, etag, synced_at,
			origin, model_name, prompt, seed,
			width, height, sample_steps, txt_cfg, duration
		) VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		elem.ID, elem.Version, elem.Project, elem.CreatedAt.Format(time.RFC3339),
		elem.ImageProjectLocation(), `pending`,
		elem.Origin,
		modelName, g.Prompt, g.Seed,
		g.Width, g.Height, g.SampleSteps, g.TxtCfg, durationSQL,
	)
	if err != nil {
		return fmt.Errorf("insert element into SQLite: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit element: %w", err)
	}

	return nil
}

func (r *elementRepo) GetElement(ctx context.Context, id string) (model.Element, error) {
	var elem model.Element
	var projectLoc, originSQL, createdAtSQL, modelSQL, promptSQL string
	var seed sql.NullInt64
	var width, height, sampleSteps sql.NullInt32
	var txtCfg, duration sql.NullFloat64

	err := r.db.QueryRowContext(ctx, `
		SELECT id, version, project, created_at, project_location, origin,
			model_name, prompt, seed,
			width, height, sample_steps, txt_cfg, duration
		FROM elements WHERE id = ?`, id,
	).Scan(
		&elem.ID, &elem.Version, &elem.Project, &createdAtSQL,
		&projectLoc, &originSQL,
		&modelSQL, &promptSQL, &seed,
		&width, &height, &sampleSteps, &txtCfg, &duration,
	)
	if err != nil {
		return model.Element{}, fmt.Errorf("get element %s: %w", id, err)
	}

	// Populate CreatedAt from the scanned SQLite column.
	elem.CreatedAt = parseTime(createdAtSQL)

	// Default origin to "generated" for backward compat.
	if originSQL == "" {
		originSQL = "generated"
	}
	elem.Origin = originSQL

	// Reconstruct ImageInfo.
	elem.Image = &model.ImageInfo{
		ProjectLocation: projectLoc,
		Format:          "png",
	}

	// Build nested Generation from SQLite columns.
	if elem.Origin == "generated" {
		g := model.Generation{
			Task:      "txt2img",
			Model:     &model.ElementModel{Architecture: modelSQL, Name: modelSQL},
			Prompt:    promptSQL,
			Width:     int(width.Int32),
			Height:    int(height.Int32),
			Seed:      seed.Int64,
			SampleSteps: int(sampleSteps.Int32),
			TxtCfg:    txtCfg.Float64,
		}
		if duration.Valid {
			g.Duration = duration.Float64
		}
		elem.Generation = &g
	}

	return elem, nil
}

// extractProjectFromKey derives the project name from an element S3 key.
// Expected format: projects/{project}/elements/{id}.json
func extractProjectFromKey(key string) string {
	// projects/{project}/elements/
	prefix := "projects/"
	if !strings.HasPrefix(key, prefix) {
		return ""
	}
	rest := key[len(prefix):]
	// Find the first "/" after the project name
	slashIdx := strings.Index(rest, "/")
	if slashIdx == -1 {
		return ""
	}
	return rest[:slashIdx]
}

// helper: safely read a Generation field from an Element.
// Returns the zero value when the Generation or the field is nil/invalid.
func genPrompt(e *model.Element) string {
	if e == nil || e.Generation == nil {
		return ""
	}
	return e.Generation.Prompt
}
func genNegativePrompt(e *model.Element) string {
	if e == nil || e.Generation == nil {
		return ""
	}
	return e.Generation.NegativePrompt
}
func genWidth(e *model.Element) int {
	if e == nil || e.Generation == nil {
		return 0
	}
	return e.Generation.Width
}
func genHeight(e *model.Element) int {
	if e == nil || e.Generation == nil {
		return 0
	}
	return e.Generation.Height
}
func genSeed(e *model.Element) int64 {
	if e == nil || e.Generation == nil {
		return 0
	}
	return e.Generation.Seed
}
func genSampleSteps(e *model.Element) int {
	if e == nil || e.Generation == nil {
		return 0
	}
	return e.Generation.SampleSteps
}
func genTxtCfg(e *model.Element) float64 {
	if e == nil || e.Generation == nil {
		return 0
	}
	return e.Generation.TxtCfg
}
func genBackendRef(e *model.Element) string {
	if e == nil || e.Generation == nil {
		return ""
	}
	return e.Generation.BackendRef
}
func genModel(e *model.Element) model.ElementModel {
	if e == nil || e.Generation == nil || e.Generation.Model == nil {
		return model.ElementModel{}
	}
	return *e.Generation.Model
}

func (r *elementRepo) ListElements(ctx context.Context, project string, opts ListOptions) ([]model.Element, int, error) {
	opts.Validate()

	sortedQuery := sortedQuery(opts.Sort, opts.Order)
	order := orderDirection(opts.Order)

	// Build the base query.
	q := &querybuilder.Query{
		From:       `elements e`,
		BaseSelect: `e.id, e.version, e.project, e.created_at, e.project_location, e.etag, e.synced_at,
			e.origin, e.model_name, e.prompt, e.seed,
			e.width, e.height, e.sample_steps, e.txt_cfg, e.duration`,
		OrderBy:        sortedQuery,
		OrderDirection: order,
		Limit:          opts.PerPage,
		Offset:         opts.Offset(),
	}

	// Add the project filter (always required).
	q.AddWhere(`e.project = ?`, project)

	// Add origin filter (core, not query-builder).
	if opts.Origin != "" {
		q.AddWhere(`e.origin = ?`, opts.Origin)
	}

	// Apply extension filters from the query builder registry.
	if r.qb != nil {
		r.qb.ApplyFilters(q, opts.Filters)
		r.qb.ApplyJoins(q)
		r.qb.ApplyColumns(q)
	}

	// Count.
	var countQuery string
	if len(q.Where) > 0 {
		countQuery = fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s`, q.From, strings.Join(q.Where, " AND "))
	} else {
		countQuery = fmt.Sprintf(`SELECT count(*) FROM %s`, q.From)
	}

	var total int
	err := r.db.QueryRowContext(ctx, countQuery, q.WhereArgs...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count elements: %w", err)
	}

	// Full query.
	selectClause := q.BaseSelect
	for _, col := range q.Columns {
		selectClause += fmt.Sprintf(", %s", col)
	}

	fromClause := q.From
	for _, j := range q.Joins {
		fromClause += fmt.Sprintf("\n\t%s", j.SQL)
	}

	whereClause := ""
	if len(q.Where) > 0 {
		whereClause = " AND " + strings.Join(q.Where, " AND ")
	}

	querySQL := fmt.Sprintf(`
		SELECT %s
		FROM %s
		WHERE 1=1%s
		ORDER BY %s %s
		LIMIT ? OFFSET ?`,
		selectClause,
		fromClause,
		whereClause,
		sortedQuery,
		order,
	)

	// Build full args list: WHERE args first, then pagination.
	allArgs := make([]any, len(q.WhereArgs), len(q.WhereArgs)+2)
	copy(allArgs, q.WhereArgs)
	allArgs = append(allArgs, opts.PerPage, opts.Offset())

	rows, err := r.db.QueryContext(ctx, querySQL, allArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("query elements: %w", err)
	}
	defer rows.Close()

	// Scan base columns first, then extension columns into dynamic slots,
	// then populate element fields from the extension column values.
	var elements []model.Element
	for rows.Next() {
		var elem model.Element

		// Base column destinations.
		var (
			modelSQL, promptSQL, createdAtSQL, projectLoc, etag, syncedAt, originSQL string
			seed                                                                      sql.NullInt64
			width, height, sampleSteps                                                sql.NullInt32
			txtCfg, duration                                                          sql.NullFloat64
		)
		// Extension column destinations — stored in a slice for flexible scanning.
		// Each slot holds a pointer that Scan will fill; we retrieve via type assertion.
		extValues := make([]any, len(q.Columns))
		for i := range extValues {
			var v int
			extValues[i] = &v
		}

		scanVals := []any{
			&elem.ID, &elem.Version, &elem.Project, &createdAtSQL, &projectLoc, &etag, &syncedAt,
			&originSQL,
			&modelSQL, &promptSQL, &seed,
			&width, &height, &sampleSteps, &txtCfg, &duration,
		}
		scanVals = append(scanVals, extValues...)

		scanErr := rows.Scan(scanVals...)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("scan element: %w", scanErr)
		}

		elem.CreatedAt = parseTime(createdAtSQL)
		// Default origin to "generated" for backward compat.
		if originSQL == "" {
			originSQL = "generated"
		}
		elem.Origin = originSQL

		// Reconstruct ImageInfo.
		elem.Image = &model.ImageInfo{
			ProjectLocation: projectLoc,
			Format:          "png",
		}

		if elem.Origin == "generated" {
			g := model.Generation{
				Task:        "txt2img",
				Model:       &model.ElementModel{Architecture: modelSQL, Name: modelSQL},
				Prompt:      promptSQL,
				Width:       int(width.Int32),
				Height:      int(height.Int32),
				Seed:        seed.Int64,
				SampleSteps: int(sampleSteps.Int32),
				TxtCfg:      txtCfg.Float64,
			}
			if duration.Valid {
				g.Duration = duration.Float64
			}
			elem.Generation = &g
		}

		// Populate extension fields from scanned extension columns.
		// Core stores values via Element.SetField — a generic mechanism
		// keyed by column name. No core-level knowledge of specific extensions.
		for i, col := range q.Columns {
			if i >= len(extValues) {
				break
			}
			ptr := extValues[i]
			if ptr == nil {
				continue
			}
			if ptrVal, ok := ptr.(*int); ok {
				elem.SetField(col, *ptrVal)
			}
		}

		elements = append(elements, elem)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate elements: %w", err)
	}

	return elements, total, nil
}

// buildWhereSQL builds a WHERE clause string from a list of conditions.
func buildWhereSQL(conds []string) string {
	if len(conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(conds, " AND ")
}

func (r *elementRepo) ListRecentPrompts(ctx context.Context, project string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT e.prompt FROM elements e
		 WHERE e.project = ?
		   AND e.prompt != ''
		   AND NOT EXISTS (
		       SELECT 1 FROM elements e2
		       WHERE e2.project = e.project AND e2.prompt = e.prompt AND e2.prompt != ''
		         AND (e2.created_at > e.created_at
		              OR (e2.created_at = e.created_at AND e2.rowid > e.rowid))
		   )
		 ORDER BY e.created_at DESC, e.rowid DESC
		 LIMIT ?`,
		project, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list recent prompts: %w", err)
	}
	defer rows.Close()

	var prompts []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("scan prompt: %w", err)
		}
		prompts = append(prompts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate prompts: %w", err)
	}
	return prompts, nil
}

func (r *elementRepo) DeleteElement(ctx context.Context, id, project string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Look up the element in SQLite.
	var projectLoc string
	err = tx.QueryRowContext(ctx,
		`SELECT project_location FROM elements WHERE id = ? AND project = ?`, id, project,
	).Scan(&projectLoc)

	// If the element is not in SQLite for this project, check if it exists under
	// a different project — that is a contract violation (should not happen).
	if err == sql.ErrNoRows {
		var otherProject string
		err2 := tx.QueryRowContext(ctx,
			`SELECT project FROM elements WHERE id = ?`, id,
		).Scan(&otherProject)
		if err2 == nil && otherProject != "" {
			slog.Warn("delete element: element exists in different project (contract violation)",
				"id", id, "requested_project", project, "actual_project", otherProject)
		}
		// Not in this project — nothing to do.
		return tx.Commit()
	}
	if err != nil {
		return fmt.Errorf("get element %s: %w", id, err)
	}

	// 2. Delete from S3 (image and element JSON).
	if r.storage != nil {
		if projectLoc != "" {
			// Construct the full S3 key from the project-relative path.
			imageS3Key := fmt.Sprintf("projects/%s/%s", project, projectLoc)
			if err := r.storage.DeleteObject(ctx, imageS3Key); err != nil {
				slog.Warn("delete image from S3", "key", imageS3Key, "error", err)
			}
		}
		elemKey := fmt.Sprintf("projects/%s/elements/%s.json", project, id)
		if err := r.storage.DeleteObject(ctx, elemKey); err != nil {
			slog.Warn("delete element JSON from S3", "key", elemKey, "error", err)
		}
	}

	// 3. Delete associated element_references (FK on element_id has ON DELETE CASCADE).
	tx.ExecContext(ctx, `DELETE FROM jobs WHERE element_id = ?`, id)

	// 4. Delete element from SQLite.
	tx.ExecContext(ctx, `DELETE FROM elements WHERE id = ?`, id)

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete element: %w", err)
	}

	return nil
}

func (r *elementRepo) SyncFromStorage(ctx context.Context) error {
	// List all elements under projects/
	objects, err := r.storage.ListObjects(ctx, "projects/")
	if err != nil {
		return fmt.Errorf("list objects: %w", err)
	}
	slog.Info("SyncFromStorage: scanning S3", "total_objects", len(objects))

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sync transaction: %w", err)
	}
	defer tx.Rollback()

	// Phase 1: Sync project.json from S3 (version-aware upsert).
	// This ensures projects with no elements still have their settings persisted.
	for _, obj := range objects {
		if strings.HasSuffix(obj.Key, "/project.json") {
			reader, _, err := r.storage.GetObject(ctx, obj.Key)
			if err != nil {
				slog.Warn("SyncFromStorage: could not read project.json", "key", obj.Key, "error", err)
				continue
			}

			data, err := io.ReadAll(reader)
			reader.Close()
			if err != nil {
				slog.Warn("SyncFromStorage: could not read project.json data", "key", obj.Key, "error", err)
				continue
			}

			ps, err := model.FromProjectSettingsJSON(data)
			if err != nil {
				slog.Warn("SyncFromStorage: could not parse project.json", "key", obj.Key, "error", err)
				continue
			}

			// Ensure the project row exists (INSERT OR IGNORE).
			_, err = tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO projects (name, schema_version, version, created_at, updated_at, synced_at, friendly_name) VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, ?)`,
				ps.Name, ps.SchemaVersion, ps.Version, ps.CreatedAt.Format(time.RFC3339), ps.UpdatedAt.Format(time.RFC3339), ps.FriendlyName,
			)
			if err != nil {
				slog.Warn("SyncFromStorage: could not insert project", "project", ps.Name, "error", err)
				continue
			}

			// Update only if version differs (modified since last sync).
			tagsBytes, _ := json.Marshal(ps.Tags)
			_, err = tx.ExecContext(ctx,
				`UPDATE projects SET schema_version = ?, version = ?, updated_at = ?, description = ?, tags = ?, friendly_name = ?, synced_at = CURRENT_TIMESTAMP
				WHERE name = ? AND version != ?`,
				ps.SchemaVersion, ps.Version, ps.UpdatedAt.Format(time.RFC3339), ps.Description, string(tagsBytes), ps.FriendlyName, ps.Name, ps.Version,
			)
			if err != nil {
				slog.Warn("SyncFromStorage: could not update project", "project", ps.Name, "error", err)
			}
		}
	}

	// Phase 2: Sync elements and update project metadata.
	for _, obj := range objects {
		// Only match actual element JSONs: projects/{project}/elements/{id}.json
		// Exclude extension delta files and directories.
		if !strings.Contains(obj.Key, "/elements/") ||
			strings.HasSuffix(obj.Key, "/") ||
			strings.Contains(obj.Key, "/ext/") {
			continue
		}

		reader, _, err := r.storage.GetObject(ctx, obj.Key)
		if err != nil {
			return fmt.Errorf("get element %s: %w", obj.Key, err)
		}

		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			return fmt.Errorf("read element %s: %w", obj.Key, err)
		}

		elem, err := model.FromJSON(data)
		if err != nil {
			return fmt.Errorf("parse element %s: %w", obj.Key, err)
		}

		// Derive project from the S3 key path (projects/{project}/elements/{id}.json).
		// The element JSON no longer carries the project field.
		elem.Project = extractProjectFromKey(obj.Key)
		if elem.Project == "" {
			slog.Warn("SyncFromStorage: could not extract project from S3 key",
				"element_id", elem.ID,
				"s3_key", obj.Key,
			)
			continue
		}

		// Ensure the project row exists (works even without meta.json).
		var preCount int
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM projects WHERE name = ?`, elem.Project).Scan(&preCount)
		if err != nil {
			return fmt.Errorf("check project pre-sync: %w", err)
		}

		_, err = tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO projects (name, version, synced_at) VALUES (?, 1, CURRENT_TIMESTAMP)`,
			elem.Project,
		)
		if err != nil {
			return fmt.Errorf("sync project %s: %w", elem.Project, err)
		}

		if preCount == 0 {
			slog.Info("SyncFromStorage: created new project from element",
				"project", elem.Project,
				"element_id", elem.ID,
				"project_location", obj.Key,
			)
		} else {
			slog.Debug("SyncFromStorage: project already existed",
				"project", elem.Project,
				"element_id", elem.ID,
				"project_location", obj.Key,
			)
		}

		// Derive the project-relative image path from the element JSON.
		// For legacy elements without an image field, fall back to
		// deriving it from the element's project and ID.
		imageLoc := elem.ImageProjectLocation()
		if elem.Image != nil && elem.Image.ProjectLocation != "" {
			imageLoc = elem.Image.ProjectLocation
		}

		// Derive extracted fields from the nested Generation struct.
		// For legacy JSON (no generation object), populate from flat fields.
		g := elem.Generation
		if g == nil {
			g = &model.Generation{}
			// Populate from flat fields for backward compatibility with legacy
			// JSON that has prompt/width/height/seed/etc. at top level.
			var flat map[string]any
			if err := json.Unmarshal(data, &flat); err == nil {
				if v, ok := flat["prompt"].(string); ok {
					g.Prompt = v
				}
				if v, ok := flat["negative_prompt"].(string); ok {
					g.NegativePrompt = v
				}
				if v, ok := flat["width"].(float64); ok {
					g.Width = int(v)
				}
				if v, ok := flat["height"].(float64); ok {
					g.Height = int(v)
				}
				if v, ok := flat["seed"].(float64); ok {
					g.Seed = int64(v)
				}
				if v, ok := flat["sample_steps"].(float64); ok {
					g.SampleSteps = int(v)
				}
				if v, ok := flat["txt_cfg"].(float64); ok {
					g.TxtCfg = v
				}
				if modelMap, ok := flat["model"].(map[string]any); ok {
					if arch, ok := modelMap["architecture"].(string); ok {
						g.Model = &model.ElementModel{Architecture: arch}
					}
					if name, ok := modelMap["name"].(string); ok {
						if g.Model == nil {
							g.Model = &model.ElementModel{}
						}
						g.Model.Name = name
					}
				}
			}
		}
		modelName := ""
		if g.Model != nil {
			modelName = g.Model.Name
		}

		// Default origin to "generated" for backward compat.
		origin := elem.Origin
		if origin == "" {
			origin = "generated"
		}

		// Default version for backward compat.
		if elem.Version == 0 {
			elem.Version = 1
		}

		// Sync duration from Generation (persisted to S3).
		durationSQL := sql.NullFloat64{Valid: g.Duration > 0, Float64: g.Duration}

		// Version-aware sync: use INSERT OR IGNORE for new elements,
		// then conditional UPDATE for existing ones where the version
		// differs. This avoids INSERT OR REPLACE (which = DELETE + INSERT
		// in SQLite and fires ON DELETE CASCADE on extension tables).
		_, err = tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO elements (
				id, version, project, created_at, project_location, etag, synced_at,
				origin, model_name, prompt, seed,
				width, height, sample_steps, txt_cfg, duration
			) VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			elem.ID, elem.Version, elem.Project, elem.CreatedAt.Format(time.RFC3339),
			imageLoc, obj.ETag,
			origin, modelName, g.Prompt, g.Seed,
			g.Width, g.Height, g.SampleSteps, g.TxtCfg, durationSQL,
		)
		if err != nil {
			return fmt.Errorf("insert element %s: %w", elem.ID, err)
		}

		// Update only if the element version differs (modified since last sync).
		_, err = tx.ExecContext(ctx,
			`UPDATE elements SET
				version = ?, created_at = ?, origin = ?, model_name = ?, prompt = ?,
				seed = ?, width = ?, height = ?, sample_steps = ?, txt_cfg = ?,
				duration = ?, synced_at = CURRENT_TIMESTAMP, project_location = ?, etag = ?
			WHERE id = ? AND version != ?`,
			elem.Version, elem.CreatedAt.Format(time.RFC3339),
			origin, modelName, g.Prompt, g.Seed,
			g.Width, g.Height, g.SampleSteps, g.TxtCfg,
			durationSQL,
			imageLoc, obj.ETag,
			elem.ID, elem.Version,
		)
		if err != nil {
			return fmt.Errorf("sync element %s: %w", elem.ID, err)
		}

		// Element references sync: projected from generation.reference_images.
		if g != nil && len(g.ReferenceImages) > 0 {
			// Delete existing references for this element (scoped to this one
			// element, not a whole-table delete — does not touch the elements
			// row itself, so it does not risk the CASCADE hazard).
			_, _ = tx.ExecContext(ctx,
				`DELETE FROM element_references WHERE element_id = ?`, elem.ID,
			)
			for pos, ref := range g.ReferenceImages {
				_, err = tx.ExecContext(ctx,
					`INSERT INTO element_references (element_id, position, ref_element_id)
					 VALUES (?, ?, ?)`,
					elem.ID, pos, ref.ElementID,
				)
				if err != nil {
					return fmt.Errorf("insert element_ref %s pos %d: %w", elem.ID, pos, err)
				}
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sync: %w", err)
	}

	return nil
}

func (r *elementRepo) ListReferencingElements(ctx context.Context, refElementID string) ([]model.Element, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT e.*
		FROM elements e
		JOIN element_references er ON er.element_id = e.id
		WHERE er.ref_element_id = ?`, refElementID)
	if err != nil {
		return nil, fmt.Errorf("list referencing elements: %w", err)
	}
	defer rows.Close()

	var elements []model.Element
	for rows.Next() {
		var elem model.Element
		var createdAtSQL, projectLoc, originSQL string
		var seed sql.NullInt64
		var width, height, sampleSteps sql.NullInt32
		var txtCfg, duration sql.NullFloat64
		var modelSQL, promptSQL string

		err := rows.Scan(
			&elem.ID, &elem.Version, &elem.Project, &createdAtSQL,
			&projectLoc, &originSQL, &modelSQL, &promptSQL, &seed,
			&width, &height, &sampleSteps, &txtCfg, &duration,
		)
		if err != nil {
			return nil, fmt.Errorf("scan referencing element: %w", err)
		}

		elem.CreatedAt = parseTime(createdAtSQL)
		if originSQL == "" {
			originSQL = "generated"
		}
		elem.Origin = originSQL
		elem.Image = &model.ImageInfo{ProjectLocation: projectLoc, Format: "png"}

		if elem.Origin == "generated" {
			g := model.Generation{
				Task:        "txt2img",
				Prompt:      promptSQL,
				Width:       int(width.Int32),
				Height:      int(height.Int32),
				Seed:        seed.Int64,
				SampleSteps: int(sampleSteps.Int32),
				TxtCfg:      txtCfg.Float64,
			}
			if duration.Valid {
				g.Duration = duration.Float64
			}
			elem.Generation = &g
		}

		elements = append(elements, elem)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate referencing elements: %w", err)
	}

	return elements, nil
}
