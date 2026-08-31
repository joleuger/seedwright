// Package batch implements sequential multi-seed generation as an
// extension to seedwright.
//
// See EXTENDING.md for the extension contract.
// See this package's EXTENSION.md for Batch-specific docs.
package batch

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"seedwright/internal/app"
	"seedwright/internal/config"
	"seedwright/internal/contracts"
	"seedwright/internal/data"
	"seedwright/internal/data/model"
	"seedwright/internal/storage"
)

// Config holds Batch's tunable settings.
type Config struct {
	Enabled       bool          `yaml:"enabled"`
	MaxConcurrent int           `yaml:"max_concurrent"`
	PollInterval  time.Duration `yaml:"poll_interval"`
}

// Variant is one expanded (prompt, seed) pair.
type Variant struct {
	Prompt string
	Seed   int64
}

// LoadConfig returns Batch's config from the global app config.
// Sets defaults before reading, so callers get sensible values even
// when the extension section is absent.
func LoadConfig(cfg *config.Config) (Config, error) {
	c := Config{Enabled: true, MaxConcurrent: 1, PollInterval: 2 * time.Second}
	if err := cfg.ExtensionConfig("joleuger/batch", &c); err != nil {
		return c, fmt.Errorf("batch: config: %w", err)
	}
	return c, nil
}

// Extension holds the Batch extension's state and dependencies.
type Extension struct {
	db          *sql.DB
	storage     storage.StorageBackend
	jobService  *data.JobService
	mux         *http.ServeMux
	cfg         Config
	projectRepo data.ProjectRepository
}

// New constructs a new Batch extension.
func New(db *sql.DB, storage storage.StorageBackend, jobService *data.JobService, mux *http.ServeMux, cfg Config) *Extension {
	return &Extension{
		db:         db,
		storage:    storage,
		jobService: jobService,
		mux:        mux,
		cfg:        cfg,
	}
}

// --- Core types ---

// BatchJSON mirrors the S3 JSON format for a batch.
type BatchJSON struct {
	ID            string    `json:"id"`
	Project       string    `json:"project"`
	CreatedAt     time.Time `json:"created_at"`
	Status        string    `json:"status"`
	Prompt        string    `json:"prompt"`
	NegativePrompt string   `json:"negative_prompt"`
	Width         int       `json:"width"`
	Height        int       `json:"height"`
	SampleSteps   int       `json:"sample_steps"`
	TxtCfg        float64   `json:"txt_cfg"`
	Items         []ItemJSON `json:"items"`
}

// ItemJSON mirrors one item in a batch.
type ItemJSON struct {
	Position   int     `json:"position"`
	Seed       int64   `json:"seed"`
	Prompt     string  `json:"prompt"`
	ElementID  *string `json:"element_id"`
	Status     string  `json:"status"`
}

// --- Batch helpers ---

// generateBatchID creates a new batch ID.
func generateBatchID() string {
	return fmt.Sprintf("batch_%x", time.Now().UnixNano())
}

// writeBatchToS3 persists a batch JSON document to S3.
func (e *Extension) writeBatchToS3(ctx context.Context, b BatchJSON) error {
	key := fmt.Sprintf("projects/%s/ext/joleuger/batch/batches/%s.json", b.Project, b.ID)
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	return e.storage.PutObject(ctx, key, strings.NewReader(string(data)), int64(len(data)), "application/json")
}

// findItemByElementID returns the batch item for a given element ID.
// Returns ok=false if the element is not part of any batch.
func (e *Extension) findItemByElementID(ctx context.Context, elementID string) (BatchItem, bool, error) {
	var item BatchItem
	err := e.db.QueryRowContext(ctx,
		`SELECT batch_id, position, seed, element_id, status
		 FROM ext_joleuger_batch_items
		 WHERE element_id = ?`,
		elementID,
	).Scan(&item.BatchID, &item.Position, &item.Seed, &item.ElementID, &item.Status_)
	if err == sql.ErrNoRows {
		return BatchItem{}, false, nil
	}
	if err != nil {
		return BatchItem{}, false, err
	}
	return item, true, nil
}

// markItem updates a batch item's status.
func (e *Extension) markItem(ctx context.Context, batchID string, position int, status string) error {
	_, err := e.db.ExecContext(ctx,
		`UPDATE ext_joleuger_batch_items SET status = ? WHERE batch_id = ? AND position = ?`,
		status, batchID, position,
	)
	return err
}

// setItemElementID records the element ID for a batch item.
func (e *Extension) setItemElementID(ctx context.Context, batchID string, position int, elementID string) error {
	_, err := e.db.ExecContext(ctx,
		`UPDATE ext_joleuger_batch_items SET element_id = ? WHERE batch_id = ? AND position = ?`,
		elementID, batchID, position,
	)
	return err
}

// nextPendingItem returns the next pending item in a batch, or (nil, true) if
// the batch is complete.
func (e *Extension) nextPendingItem(ctx context.Context, batchID string) (*BatchItem, bool, error) {
	var item BatchItem
	err := e.db.QueryRowContext(ctx,
		`SELECT batch_id, position, seed, prompt, element_id, status
		 FROM ext_joleuger_batch_items
		 WHERE batch_id = ? AND status = 'pending'
		 ORDER BY position ASC
		 LIMIT 1`,
		batchID,
	).Scan(&item.BatchID, &item.Position, &item.Seed, &item.Prompt, &item.ElementID, &item.Status_)
	if err == sql.ErrNoRows {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &item, false, nil
}

// buildElement creates an Element with the same parameters as the batch,
// using the given seed.
func (e *Extension) buildElement(ctx context.Context, batchID string, item *BatchItem) model.Element {
	// Fetch batch params from DB.
	var batch Batch
	err := e.db.QueryRowContext(ctx,
		`SELECT id, project, status, prompt, negative_prompt, width, height, sample_steps, txt_cfg
		 FROM ext_joleuger_batch_batches WHERE id = ?`,
		batchID,
	).Scan(&batch.ID_, &batch.Project_, &batch.Status_, &batch.Prompt, &batch.NegativePrompt,
		&batch.Width, &batch.Height, &batch.SampleSteps, &batch.TxtCfg)
	if err != nil {
		slog.Error("batch buildElement: scan batch", "batch_id", batchID, "error", err)
		return model.Element{}
	}

	// Use the item's expanded prompt if available (from CreateBatchFromVariants),
	// otherwise fall back to the batch prompt (backward compat for seed-only batches).
	prompt := item.Prompt
	if prompt == "" {
		prompt = batch.Prompt
	}

	return model.Element{
		ID:            fmt.Sprintf("%s_%d", batchID, item.Position),
		Project:       batch.Project_,
		Kind:          "image",
		CreatedAt:     time.Now(),
		Generation: &model.Generation{
			Task:           "txt2img",
			Prompt:         prompt,
			NegativePrompt: batch.NegativePrompt,
			Width:          batch.Width,
			Height:         batch.Height,
			SampleSteps:    batch.SampleSteps,
			TxtCfg:         batch.TxtCfg,
			Seed:           item.Seed,
		},
	}
}

// countRunning returns the number of running batches for a project.
func (e *Extension) countRunning(ctx context.Context, project string) (int, error) {
	var n int
	err := e.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM ext_joleuger_batch_batches
		 WHERE project = ? AND status = 'running'`,
		project,
	).Scan(&n)
	return n, err
}

// --- Core types ---

// Batch is the SQLite batch row.
type Batch struct {
	ID_             string
	Project_        string
	Status_         string
	Prompt          string
	NegativePrompt  string
	Width           int
	Height          int
	SampleSteps     int
	TxtCfg          float64
	CreatedAt       time.Time
	// Items is populated by GetBatchItems and used by CardSteps.
	Items []BatchItem
}

// BatchItem is the SQLite item row.
type BatchItem struct {
	BatchID   string
	Position  int
	Seed      int64
	Prompt    string
	ElementID *string
	Status_   string
}

// --- Batch satisfies the contracts Step/Card via thin accessor methods.
// Batch keeps its own storage and orchestration exactly as they are; nothing
// about adopting the contract changes internals.
//
// One BatchItem = one Step, resolving the "batch produces N elements" question
// directly: N items, N Steps, each with its own singular output — never a
// single Step with a plural one.

// Type implements contracts.Step.
func (item BatchItem) Type() string { return "batch.item" }

// Status implements contracts.Step.
func (item BatchItem) Status() string { return item.Status_ }

// CanonicalStatus implements contracts.Step.
// Maps batch statuses into five well-known states: running, success,
// failed, cancelled, waiting. Sub-reasons (e.g. "timeout", "format error")
// stay hidden behind the exact Status() value.
func (item BatchItem) CanonicalStatus() string {
	switch item.Status_ {
	case "pending":
		return "waiting"
	case "generating":
		return "running"
	case "completed":
		return "success"
	case "failed":
		return "failed"
	case "cancelled":
		return "cancelled"
	default:
		return item.Status_
	}
}

// Output implements contracts.Step.
func (item BatchItem) Output() *contracts.ElementRef {
	if item.ElementID == nil || *item.ElementID == "" {
		return nil
	}
	return &contracts.ElementRef{ElementID: *item.ElementID}
}

// --- Batch satisfies contracts.Card. ---

// ID implements contracts.Card.
func (b Batch) ID() string { return b.ID_ }

// Project implements contracts.Card.
func (b Batch) Project() string { return b.Project_ }

// Steps implements contracts.Card.
func (b Batch) Steps() []contracts.Step {
	out := make([]contracts.Step, len(b.Items))
	for i, item := range b.Items {
		out[i] = item
	}
	return out
}

// CreateBatch creates a batch from expanded variants, inserts records,
// and starts the first job. Returns the batch ID for redirect.
func (e *Extension) CreateBatch(ctx context.Context, project, prompt, negativePrompt string, width, height, steps int, cfg float64, seedStr string) (string, error) {
	seeds := parseSeeds(seedStr)
	if len(seeds) == 0 {
		return "", fmt.Errorf("no valid seeds")
	}

	batchID := generateBatchID()

	// Ensure project row exists.
	if e.projectRepo != nil {
		_ = e.projectRepo.CreateProject(ctx, model.NewProject(project))
	}

	now := time.Now().UTC()

	_, err := e.db.Exec(`INSERT INTO ext_joleuger_batch_batches
		(id, project, status, prompt, negative_prompt, width, height, sample_steps, txt_cfg, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		batchID, project, "running", prompt, negativePrompt,
		width, height, steps, cfg, now,
	)
	if err != nil {
		return "", fmt.Errorf("batch: insert batch: %w", err)
	}

	// Persist batch JSON to S3.
	batchJSON := BatchJSON{
		ID:             batchID,
		Project:        project,
		CreatedAt:      now,
		Status:         "running",
		Prompt:         prompt,
		NegativePrompt: negativePrompt,
		Width:          width,
		Height:         height,
		SampleSteps:    steps,
		TxtCfg:         cfg,
		Items:          make([]ItemJSON, len(seeds)),
	}
	for i, seed := range seeds {
		batchJSON.Items[i] = ItemJSON{
			Position: i,
			Seed:     seed,
			Prompt:   prompt,
			Status:   "pending",
		}
	}
	if err := e.writeBatchToS3(ctx, batchJSON); err != nil {
		slog.Warn("batch: S3 write (non-fatal)", "batch", batchID, "error", err)
	}

	// Insert item rows and start first job.
	for pos, seed := range seeds {
		_, err := e.db.Exec(`INSERT INTO ext_joleuger_batch_items
			(batch_id, position, seed, prompt, element_id, status) VALUES (?, ?, ?, ?, NULL, 'pending')`,
			batchID, pos, seed, prompt,
		)
		if err != nil {
			return "", fmt.Errorf("batch: insert item %d: %w", pos, err)
		}

		elem := model.NewImageElement(project, prompt, width, height, steps, cfg, seed, "unknown", "", "", "", "unknown")
		if g := elem.Generation; g != nil {
			g.NegativePrompt = negativePrompt
		}

		if pos == 0 {
			created, err := e.jobService.StartJob(ctx, elem)
			if err != nil {
				return "", fmt.Errorf("batch: start first job: %w", err)
			}
			e.setItemElementID(ctx, batchID, pos, created)
		}
	}

	return batchID, nil
}

// CreateBatchFromVariants creates a batch from a list of already-expanded
// variants, inserts records, and starts the first job. Returns the batch ID.
func (e *Extension) CreateBatchFromVariants(ctx context.Context, project, prompt, negativePrompt string, width, height, steps int, cfg float64, variants []Variant) (string, error) {
	batchID := generateBatchID()

	// Ensure project row exists.
	if e.projectRepo != nil {
		_ = e.projectRepo.CreateProject(ctx, model.NewProject(project))
	}

	now := time.Now().UTC()

	_, err := e.db.Exec(`INSERT INTO ext_joleuger_batch_batches
		(id, project, status, prompt, negative_prompt, width, height, sample_steps, txt_cfg, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		batchID, project, "running", prompt, negativePrompt,
		width, height, steps, cfg, now,
	)
	if err != nil {
		return "", fmt.Errorf("batch: insert batch: %w", err)
	}

	// Persist batch JSON to S3.
	batchJSON := BatchJSON{
		ID:             batchID,
		Project:        project,
		CreatedAt:      now,
		Status:         "running",
		Prompt:         prompt,
		NegativePrompt: negativePrompt,
		Width:          width,
		Height:         height,
		SampleSteps:    steps,
		TxtCfg:         cfg,
		Items:          make([]ItemJSON, len(variants)),
	}
	for i, v := range variants {
		batchJSON.Items[i] = ItemJSON{
			Position: i,
			Seed:     v.Seed,
			Prompt:   v.Prompt,
			Status:   "pending",
		}
	}
	if err := e.writeBatchToS3(ctx, batchJSON); err != nil {
		slog.Warn("batch: S3 write (non-fatal)", "batch", batchID, "error", err)
	}

	// Insert item rows and start first job.
	for i, v := range variants {
		_, err := e.db.Exec(`INSERT INTO ext_joleuger_batch_items
			(batch_id, position, seed, prompt, element_id, status) VALUES (?, ?, ?, ?, NULL, 'pending')`,
			batchID, i, v.Seed, v.Prompt,
		)
		if err != nil {
			return "", fmt.Errorf("batch: insert item %d: %w", i, err)
		}

		if i == 0 {
			// Create element and start first job.
			elem := model.NewImageElement(project, v.Prompt, width, height, steps, cfg, v.Seed, "unknown", "", "", "", "unknown")
			if g := elem.Generation; g != nil {
				g.NegativePrompt = negativePrompt
			}

			elemID, err := e.jobService.StartJob(ctx, elem)
			if err != nil {
				return "", fmt.Errorf("batch: start first job: %w", err)
			}
			e.setItemElementID(ctx, batchID, i, elemID)
		}
	}

	return batchID, nil
}

// NewExtension constructs a Batch extension from an App instance.
// This is the entrypoint called from ext.RegisterAll.
func NewExtension(ctx context.Context, a *app.App) (*Extension, error) {
	cfg, err := LoadConfig(a.Config)
	if err != nil {
		return nil, err
	}
	b := New(a.DB, a.Storage, a.JobService, a.GetServeMux(), cfg)
	b.projectRepo = a.Projects
	b.RegisterRoutes(a)
	return b, nil
}

// Sync rebuilds the Batch SQLite tables from S3.
// This is the entrypoint called from ext.SyncAll.
func Sync(ctx context.Context, a *app.App) error {
	b := New(a.DB, a.Storage, a.JobService, a.GetServeMux(), Config{})
	b.projectRepo = a.Projects
	return b.SyncFromStorage(ctx)
}
