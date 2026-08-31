package batch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
)

// SyncFromStorage rebuilds the Batch SQLite tables by scanning the
// extension's S3 prefix. Must run after core's SyncFromStorage so
// extension foreign keys resolve against rows that already exist.
func (e *Extension) SyncFromStorage(ctx context.Context) error {
	// List all objects under projects/ to find project prefixes.
	objects, err := e.storage.ListObjects(ctx, "projects/")
	if err != nil {
		return fmt.Errorf("sync list projects: %w", err)
	}

	// Collect unique project prefixes.
	projects := make(map[string]bool)
	for _, obj := range objects {
		// Extract project from key: projects/{project}/...
		rest := obj.Key[len("projects/"):]
		slash := len(rest)
		for i, c := range rest {
			if c == '/' {
				slash = i
				break
			}
		}
		project := rest[:slash]
		if project != "" {
			projects[project] = true
		}
	}

	for project := range projects {
		batchPrefix := fmt.Sprintf("projects/%s/ext/joleuger/batch/batches/", project)
		batchObjs, err := e.storage.ListObjects(ctx, batchPrefix)
		if err != nil {
			slog.Warn("sync: list objects", "prefix", batchPrefix, "error", err)
			continue
		}

		for _, objInfo := range batchObjs {
			rc, _, err := e.storage.GetObject(ctx, objInfo.Key)
			if err != nil {
				slog.Warn("sync: get object", "key", objInfo.Key, "error", err)
				continue
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				slog.Warn("sync: read object", "key", objInfo.Key, "error", err)
				continue
			}

			var batch BatchJSON
			if err := json.Unmarshal(data, &batch); err != nil {
				slog.Warn("sync: unmarshal", "key", objInfo.Key, "error", err)
				continue
			}

			// Upsert batch.
			_, err = e.db.ExecContext(ctx,
				`INSERT INTO ext_joleuger_batch_batches
				 (id, project, status, prompt, negative_prompt, width, height, sample_steps, txt_cfg, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(id) DO UPDATE SET
					project = excluded.project,
					status = excluded.status,
					prompt = excluded.prompt,
					negative_prompt = excluded.negative_prompt,
					width = excluded.width,
					height = excluded.height,
					sample_steps = excluded.sample_steps,
					txt_cfg = excluded.txt_cfg`,
				batch.ID, batch.Project, batch.Status, batch.Prompt, batch.NegativePrompt,
				batch.Width, batch.Height, batch.SampleSteps, batch.TxtCfg, batch.CreatedAt,
			)
			if err != nil {
				slog.Warn("sync: upsert batch", "id", batch.ID, "error", err)
				continue
			}

			// Upsert each item.
			for _, item := range batch.Items {
				var elementID *string
				if item.ElementID != nil {
					tmp := *item.ElementID
					elementID = &tmp
				}
				_, err = e.db.ExecContext(ctx,
					`INSERT INTO ext_joleuger_batch_items
					 (batch_id, position, seed, prompt, element_id, status)
					VALUES (?, ?, ?, ?, ?, ?)
					ON CONFLICT(batch_id, position) DO UPDATE SET
						seed = excluded.seed,
						prompt = excluded.prompt,
						element_id = excluded.element_id,
						status = excluded.status`,
					batch.ID, item.Position, item.Seed, item.Prompt, elementID, item.Status,
				)
				if err != nil {
					slog.Warn("sync: upsert item", "batch", batch.ID, "pos", item.Position, "error", err)
				}
			}
		}
	}

	return nil
}

// ListBatches returns all batches for a project, ordered by creation time descending.
func (e *Extension) ListBatches(ctx context.Context, project string) ([]Batch, error) {
	rows, err := e.db.QueryContext(ctx,
		`SELECT id, project, status, prompt, negative_prompt, width, height, sample_steps, txt_cfg, created_at
		 FROM ext_joleuger_batch_batches
		 WHERE project = ?
		 ORDER BY created_at DESC`,
		project,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var batches []Batch
	for rows.Next() {
		var b Batch
		if err := rows.Scan(&b.ID_, &b.Project_, &b.Status_, &b.Prompt, &b.NegativePrompt,
			&b.Width, &b.Height, &b.SampleSteps, &b.TxtCfg, &b.CreatedAt); err != nil {
			return nil, err
		}
		batches = append(batches, b)
	}
	return batches, rows.Err()
}

// GetBatch returns a single batch by ID.
func (e *Extension) GetBatch(ctx context.Context, id string) (*Batch, error) {
	var b Batch
	err := e.db.QueryRowContext(ctx,
		`SELECT id, project, status, prompt, negative_prompt, width, height, sample_steps, txt_cfg, created_at
		 FROM ext_joleuger_batch_batches WHERE id = ?`,
		id,
	).Scan(&b.ID_, &b.Project_, &b.Status_, &b.Prompt, &b.NegativePrompt,
		&b.Width, &b.Height, &b.SampleSteps, &b.TxtCfg, &b.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// GetBatchItems returns the items for a batch, ordered by position.
func (e *Extension) GetBatchItems(ctx context.Context, batchID string) ([]BatchItem, error) {
	rows, err := e.db.QueryContext(ctx,
		`SELECT batch_id, position, seed, prompt, element_id, status
		 FROM ext_joleuger_batch_items
		 WHERE batch_id = ?
		 ORDER BY position ASC`,
		batchID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []BatchItem
	for rows.Next() {
		var item BatchItem
		if err := rows.Scan(&item.BatchID, &item.Position, &item.Seed, &item.Prompt, &item.ElementID, &item.Status_); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// CompleteBatch marks a batch as completed when all items are done.
func (e *Extension) CompleteBatch(ctx context.Context, batchID, status string) error {
	_, err := e.db.ExecContext(ctx,
		`UPDATE ext_joleuger_batch_batches SET status = ? WHERE id = ?`,
		status, batchID,
	)
	return err
}
