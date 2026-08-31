# Batch Extension — EXTENSION.md

## Overview

Sequential multi-seed image generation. A user submits a prompt + a list/range of seeds, and the extension creates batches that generate images one seed at a time.

**New front door (2026-07-26):** Batch creation is now folded into the project dashboard's generate form. The user fills in the same params, optionally adds combination syntax (`{cat,dog} in space`) and/or a seed list (`1..10` or `42,43,44`), clicks **Preview**, reviews the expanded variants, and submits. This eliminates a separate "Start Batch" card — the generate form is the single entry point.

## User Flow

1. On the project dashboard, the user fills in the generate form with prompt, params, and optionally combination syntax in the prompt (e.g. `a {cat,dog} in space`) and/or a seed list in the Seeds field (e.g. `1..10`)
2. Clicks **Preview** → JS calls `POST /{project}/ext/joleuger/batch/preview` to expand and show variant count + list
3. Confirms in the modal → JS routes to the appropriate endpoint:
   - **Single variant** → `POST /{project}/generate` (core handler, creates one image)
   - **Multiple variants** → `POST /{project}/ext/joleuger/batch/generate` (batch handler)
4. Batch creates → redirects to `/{project}/batch/{batchID}`
5. Dashboard page polls `GET /{project}/batch/{batchID}/api` every 2s
6. Shows progress: pending / generating / completed / failed / cancelled per item
7. When complete, shows grid of all generated images

## Combination Syntax

The extension supports `{a,b,c}` grouping in the prompt, expanded via cartesian product with the seed list.

| Input | Expansion |
|---|---|
| Prompt: `"a {cat,dog} in space"` | `"a cat in space"`, `"a dog in space"` |
| Seeds: `42,43` | 2 variants per prompt |
| Both | 4 variants (2 prompts × 2 seeds) |
| Seeds: `1..10` | 20 variants (2 prompts × 10 seeds) |

## Data Model

### S3 Layout (Pattern A — new table)

```
projects/{project}/ext/joleuger/batch/batches/{batch_id}.json
```

### Batch JSON (S3)

```json
{
  "id": "batch_abc123",
  "project": "my-project",
  "created_at": "2025-07-21T12:00:00Z",
  "status": "running",
  "prompt": "a cat in space",
  "negative_prompt": "",
  "width": 512,
  "height": 512,
  "sample_steps": 20,
  "txt_cfg": 7.0,
  "items": [
    { "position": 0, "seed": 42, "prompt": "a cat in space", "element_id": "elem-uuid-1", "status": "completed" },
    { "position": 1, "seed": 43, "prompt": "a dog in space", "element_id": "elem-uuid-2", "status": "generating" },
    { "position": 2, "seed": 44, "prompt": "a cow in space", "element_id": null, "status": "pending" }
  ]
}
```

### SQLite Tables

```sql
CREATE TABLE ext_joleuger_batch_batches (
    id              TEXT PRIMARY KEY,
    project         TEXT NOT NULL,
    status          TEXT DEFAULT 'queued',
    prompt          TEXT,
    negative_prompt TEXT,
    width           INTEGER,
    height          INTEGER,
    sample_steps    INTEGER,
    txt_cfg         REAL,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (project) REFERENCES projects(name) ON DELETE CASCADE
);

CREATE TABLE ext_joleuger_batch_items (
    batch_id   TEXT NOT NULL,
    position   INTEGER NOT NULL,
    seed       INTEGER NOT NULL,
    prompt     TEXT,
    element_id TEXT,
    status     TEXT DEFAULT 'pending',
    PRIMARY KEY (batch_id, position),
    FOREIGN KEY (batch_id)   REFERENCES ext_joleuger_batch_batches(id) ON DELETE CASCADE,
    FOREIGN KEY (element_id) REFERENCES elements(id) ON DELETE SET NULL
);
```

> **Note:** The `prompt` column on `ext_joleuger_batch_items` stores the **expanded variant prompt** for each item (e.g., `"a cat in space"`), not the raw template (e.g., `"a {cat,dog} in space"`). This ensures that when a batch advances to the next item, the generated image uses the correct per-variant prompt. Seed-only batches (no combination syntax) store the original prompt in both the batch and item rows.

### Job Status Values

| Status | Meaning |
|---|---|
| `"pending"` | Not yet started |
| `"generating"` | Job submitted to sdcpp, waiting for completion |
| `"completed"` | Image generated and uploaded |
| `"failed"` | Job failed (error recorded in element) |
| `"cancelled"` | Job was cancelled |

### Batch Status Values

| Status | Meaning |
|---|---|
| `"queued"` | All items pending |
| `"running"` | At least one item is generating |
| `"completed"` | All items completed |
| `"failed"` | At least one item failed |
| `"cancelled"` | Batch was manually cancelled |

## HTTP Endpoints

| Endpoint | Method | Description |
|---|---|---|
| `POST /{project}/ext/joleuger/batch/preview` | POST | Expand prompt + seeds, return variant count + list (JSON) |
| `POST /{project}/ext/joleuger/batch/generate` | POST | Expand + create batch, start first job |
| `/{project}/batch/{id}` | GET | Render batch progress page |
| `/{project}/batch/{id}/api` | GET | JSON polling response |

### POST `/{project}/ext/joleuger/batch/preview`

Returns JSON with the number of variants and their (prompt, seed) pairs, without creating a batch.

Request body (JSON):
```json
{"prompt": "a {cat,dog} in space", "seeds": "1..5"}
```

Response (JSON):
```json
{
  "count": 10,
  "variants": [
    { "prompt": "a cat in space", "seed": 1 },
    { "prompt": "a dog in space", "seed": 1 },
    ...
  ]
}
```

### POST `/{project}/ext/joleuger/batch/generate`

Form fields: `prompt`, `negative_prompt`, `width`, `height`, `steps`, `cfg`, `seeds` (comma-separated, range `1..10`, or combination syntax `{a,b,c}`).

Always expands and always creates a batch (redirects to `/{project}/batch/{batchID}`, 303 See Other). The expanded variant prompts are stored per-item so that subsequent batch items use the correct prompt, not the raw template.

### GET `/{project}/batch/{id}/api`

JSON response:
```json
{
  "batch_status": "running",
  "batch_done": false,
  "items": [
    { "position": 0, "seed": 42, "prompt": "a cat in space", "element_id": "elem-uuid-1", "status": "completed" },
    { "position": 1, "seed": 43, "prompt": "a dog in space", "element_id": null, "status": "pending" }
  ]
}
```

When `batch_done` is `true`, the polling script stops and shows the final state.

## UI Integration

### Project Dashboard — Folded into Generate Form

The batch extension does **not** inject a "Start Batch" card. Instead, the project dashboard's generate form serves as the single entry point for both single-image and batch creation. The JavaScript in `project.html`:

1. On Preview button click, calls `POST /{project}/ext/joleuger/batch/preview`
2. If variant count is 1, calls the core `POST /{project}/generate` (single image)
3. If variant count > 1, calls `POST /{project}/ext/joleuger/batch/generate` (batch)

This routing keeps the core Go code clean — it knows nothing about batches. The extension owns the preview endpoint, the expand logic, and the batch creation.

### Batch Progress Page

The batch progress page (`batch.html`) shows:
- Batch metadata (prompt, params, creation time)
- Per-item progress table (position, seed, status, link to element)
- A **Cancel** button that sets batch status to `"cancelled"`

### Polling

The batch progress page polls `/api` every 2 seconds. When `batch_done` is `true`, polling stops.

## Configuration

Add to `config.yaml`:

```yaml
extensions:
  joleuger/batch:
    max_concurrent: 1  # currently always 1 (sequential)
```

## Extension Contract

The batch extension follows the extension contract defined in `EXTENDING.md`:

- **Bootstrap**: `NewExtension(ctx, a)` constructs from `*app.App`, wiring in the project repo and serve mux
- **Hooks**: Appends `HandleJobTerminal` and `HandleProjectDeleted` to `a.Hooks`
- **Schema**: `Migrate(ctx, db)` creates tables idempotently
- **Routes**: `RegisterRoutes(a)` registers on `a.GetServeMux()` at `/{project}/ext/joleuger/batch/{route}` prefix — no collision with core routes
- **Sync**: `Sync(ctx, a)` rebuilds tables from S3 via `SyncFromStorage`
- **UI Slots**: `NavBar` via `NavBarItems` hook (adds "Batch" link). Note: `DashboardCard` method exists in `dashboard.go` but is not wired to `DashboardExtras` — users navigate via the NavBar "Batch" link instead.

### Key Methods

| Method | Signature | Purpose |
|---|---|---|
| `NeedsExpansion` | `(prompt, seeds string) bool` | Fast check: is there a combo group or multi-seed? |
| `Expand` | `(prompt, seeds string) ([]Variant, error)` | Full expansion: cartesian product of groups × seeds |
| `CreateBatch` | `(ctx, project, prompt, ..., seedStr string) (string, error)` | Create batch from seed string |
| `CreateBatchFromVariants` | `(ctx, project, prompt, ..., variants []Variant) (string, error)` | Create batch from pre-expanded variants |

## Adding New Extensions

To add a new extension, follow these steps:

1. Create `ext/{owner}/{name}/` with:
   - `ext.go` — `Extension` struct, `New()`, `NewExtension(ctx, a)` convenience
   - `migrate.go` — `Migrate(ctx, db)` for DDL
   - `hooks.go` — hook implementations (optional)
   - `routes.go` — `RegisterRoutes(a)` (optional)
   - `sync.go` — `SyncFromStorage(ctx)` (optional)
   - `dashboard.go` — `DashboardCard(ctx, project)` (optional)
   - `templates.go` — embedded HTML templates (optional)
2. Register in `ext/extensions.go`:
   ```go
   b, err := name.NewExtension(ctx, a)
   a.Hooks.OnJobTerminal = append(a.Hooks.OnJobTerminal, b.HandleJobTerminal)
   b.RegisterRoutes(a)
   batch.Migrate(ctx, a.DB)
   ```
3. Sync in `ext/extensions.go`:
   ```go
   if err := name.Sync(ctx, a); err != nil { ... }
   ```

## Lifecycle

### Create Batch
1. POST to `/{project}/ext/joleuger/batch/generate` (or via the folded
   Preview flow on the dashboard generate form)
2. Creates batch row + item rows in SQLite
3. Creates batch JSON in S3
4. Starts job for first item via `JobService.StartJob`
5. Records element ID in item row

### Auto-Advance
1. Job completes → `HandleJobTerminal` fires
2. Marks item as completed
3. Checks for next pending item
4. If found: creates element, starts job, records element ID
5. If none: calls `CompleteBatch`

### Cancel
1. User clicks Cancel → POST to `/{project}/batch/{id}/cancel`
2. Sets batch status to `"cancelled"`
3. Cancels any running job on sdcpp side
4. Marks all remaining items as `"cancelled"`

### Delete Project
1. `HandleProjectDeleted` fires when project is deleted
2. Deletes all S3 objects under `projects/{project}/ext/joleuger/batch/`

### Recover from S3 (Sync)
1. `SyncFromStorage` scans S3 for batch JSON files
2. Upserts batch and item rows in SQLite
3. Recovers state after database loss

---

## User Cases

### UC-B01 — Preview Batch Variants

**Goal:** Expand prompt + seeds and see all generated variants before committing.

| Step | Action | Expected Result |
|---|---|---|
| 1 | `GET /test-01` (dashboard with generate form) | Form with "Preview" button |
| 2 | Fill prompt="a {cat,dog} in space", seeds="1..3" | — |
| 3 | `POST /test-01/ext/joleuger/batch/preview` (JSON: `{"prompt":"a {cat,dog} in space","seeds":"1..3"}`) | 200 OK JSON: `count=6`, variants list with 2 prompts × 3 seeds |
| 4 | Verify variants | `[{prompt:"a cat in space",seed:1}, {prompt:"a cat in space",seed:2}, {prompt:"a cat in space",seed:3}, {prompt:"a dog in space",seed:1}, {prompt:"a dog in space",seed:2}, {prompt:"a dog in space",seed:3}]` |

**Single variant → core generate:** If prompt has no groups and seeds is a single value, preview returns count=1. Submitting goes to `POST /{project}/generate` (core handler), not the batch extension.

### UC-B02 — Create a Batch with Multiple Seeds

**Goal:** Submit a batch, verify batch creation and auto-advancement.

| Step | Action | Expected Result |
|---|---|---|
| 1 | Fill form: prompt="a cat", seeds="42,43,44" | — |
| 2 | Submit (Preview count > 1 → `POST /test-01/ext/joleuger/batch/generate`) | 303 redirect to `GET /test-01/batch/{batchID}` |
| 3 | `GET /test-01/batch/{batchID}/api` | 200 OK: `batch_status: "running"`, item 0=`generating`, items 1-2=`pending` |
| 4 | Poll every 2s until done | Item 0→completed, item 1→generating |
| 5 | Poll until `batch_done: true` | 200 OK: `batch_status: "completed"`, all 3 items done |
| 6 | `GET /test-01/gallery` | 3 new elements appear with seeds 42, 43, 44 |

**S3 verification:** `projects/test-01/ext/joleuger/batch/batches/{batchID}.json` exists with all items and correct statuses.

### UC-B03 — Monitor Batch Progress

**Goal:** Real-time monitoring of batch items as they complete one-by-one.

| Step | Action | Expected Result |
|---|---|---|
| 1 | Create a batch with 5 seeds (UC-B02) | Redirects to `GET /test-01/batch/{batchID}` |
| 2 | Batch progress page renders | Shows per-item table with position, seed, prompt, status, link to element |
| 3 | Page polls `GET /test-01/batch/{batchID}/api` every 2s | `#activeJobsList` DOM element updated (partial DOM update, no page reload) |
| 4 | Each item transitions | `pending` → `generating` → `completed` in sequence |
| 5 | All items complete | `batch_done: true`; polling stops; final state shown |

### UC-B04 — Cancel a Running Batch

**Goal:** Stop a batch mid-execution and verify cleanup.

| Step | Action | Expected Result |
|---|---|---|
| 1 | Create a batch with 10 seeds (UC-B02) | Batch running, first few items completing |
| 2 | `POST /test-01/batch/{batchID}/cancel` | Batch status set to `"cancelled"` |
| 3 | Running item | sdcpp job cancelled; element created with status reflecting cancellation |
| 4 | Pending items | All remaining items marked `"cancelled"` |
| 5 | S3 verification | `batches/{batchID}.json` updated to `"cancelled"` status |

### UC-B05 — Batch with Combination Syntax

**Goal:** Generate images from prompt groups × seeds via cartesian product.

| Step | Action | Expected Result |
|---|---|---|
| 1 | Fill form: prompt="a {cat,dog} wearing {hat,scarf}", seeds="1..2" | — |
| 2 | `POST /test-01/ext/joleuger/batch/preview` | 200 OK JSON: `count=8` (4 prompts × 2 seeds) |
| 3 | Verify variant prompts | `["a cat wearing hat","a cat wearing scarf","a dog wearing hat","a dog wearing scarf"]` × seeds 1,2 |
| 4 | Submit (count > 1 → batch) | 303 redirect to batch progress page |
| 5 | `GET /test-01/batch/{batchID}/api` | 8 items; each with correct per-variant prompt |
| 6 | Wait for completion | `batch_done: true`; gallery shows 8 images |

**Deterministic order:** Preview shows items sorted by seed (outer loop) then prompt variant (inner loop, sorted alphabetically). Batch items are created in the same order.

### UC-B06 — Batch Auto-Advance (Job Failure)

**Goal:** Verify batch continues past a failed item.

| Step | Action | Expected Result |
|---|---|---|
| 1 | Create a batch where one item will fail (e.g., trigger sdcpp error) | — |
| 2 | Batch runs; one item fails | Item status → `"failed"`; element created with error |
| 3 | Auto-advance fires | Next pending item starts generating |
| 4 | All remaining items complete | Batch status: `"failed"` (at least one item failed); other items `"completed"` |

### UC-B07 — Batch Sync from S3

**Goal:** Rebuild batch state from S3 after database loss.

| Step | Action | Expected Result |
|---|---|---|
| 1 | Create a batch that completed (UC-B02) | Batch JSON + items in S3; batch + items in SQLite |
| 2 | Delete `cache.db` | — |
| 3 | Restart application | `SyncFromStorage` scans S3 for batch JSON files |
| 4 | `GET /test-01/batch/{batchID}` | Batch page renders; all items show correct status from S3 |
