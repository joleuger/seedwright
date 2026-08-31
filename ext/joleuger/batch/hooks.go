package batch

import (
	"context"
	"fmt"
	"log/slog"

	"seedwright/internal/data"
	"seedwright/internal/data/model"
	"seedwright/internal/storage"
)

// HandleJobTerminal implements the OnJobTerminal hook.
func (e *Extension) HandleJobTerminal(ctx context.Context, elem *model.Element, job data.JobRecord) error {
	if elem == nil {
		return nil
	}
	item, ok, err := e.findItemByElementID(ctx, elem.ID)
	if err != nil {
		slog.Warn("batch: find item", "element", elem.ID, "error", err)
		return nil
	}
	if !ok {
		return nil
	}

	if err := e.markItem(ctx, item.BatchID, item.Position, job.Status); err != nil {
		slog.Warn("batch: mark item", "batch", item.BatchID, "pos", item.Position, "error", err)
		return nil
	}

	next, done, err := e.nextPendingItem(ctx, item.BatchID)
	if err != nil {
		slog.Warn("batch: next pending", "batch", item.BatchID, "error", err)
		return nil
	}
	if done {
		return e.CompleteBatch(ctx, item.BatchID, "completed")
	}

	nextElem := e.buildElement(ctx, item.BatchID, next)
	created, err := e.jobService.StartJob(ctx, nextElem)
	if err != nil {
		slog.Warn("batch: start next job", "batch", item.BatchID, "pos", next.Position, "error", err)
		return nil
	}
	if err := e.setItemElementID(ctx, item.BatchID, next.Position, created); err != nil {
		slog.Warn("batch: set item element_id", "batch", item.BatchID, "pos", next.Position, "error", err)
		return nil
	}

	return nil
}

// HandleProjectDeleted implements the OnProjectDeleted hook.
func (e *Extension) HandleProjectDeleted(ctx context.Context, project string) error {
	prefix := fmt.Sprintf("projects/%s/ext/joleuger/batch/batches/", project)
	return deletePrefix(ctx, e.storage, prefix)
}

// deletePrefix deletes all objects under the given S3 prefix.
func deletePrefix(ctx context.Context, s storage.StorageBackend, prefix string) error {
	objects, err := s.ListObjects(ctx, prefix)
	if err != nil {
		return err
	}
	for _, obj := range objects {
		if err := s.DeleteObject(ctx, obj.Key); err != nil {
			slog.Warn("batch: delete prefix object", "key", obj.Key, "error", err)
		}
	}
	return nil
}
