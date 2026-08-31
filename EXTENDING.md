# EXTENDING.md — Extension Contract

## Scope

This document describes the extension contract: how bundled extensions integrate with seedwright.

**Single binary.** Extensions are hardwired into `cmd/app/main.go`. No separate build target, no `cmd/app-ext`.

## Where extensions live

`ext/{owner}/{extension}/` — each extension is its own Go package. Examples:
- `ext/joleuger/batch/` — sequential multi-seed generation
- `ext/joleuger/favorites/` — element bookmarking
- `ext/joleuger/imageproc/` — stateless image-processing engine (leaf dependency)

---

## Part 1 — Storage Convention

### Namespace

All extension data lives under `projects/{project}/ext/{owner}/{extension}/`. Each extension owns its own subtree.

### Recommended Layout

Extensions have two choices for storage. The naming convention below ensures clear separation and avoids collisions.

#### Pattern A: New table (extension owns its own entity type)

Use when the extension introduces a concept that doesn't directly extend an existing core entity.

**S3:**
```
projects/{project}/ext/{owner}/{extension}/{table_name}/{table_element_id}.json
```

**SQLite:**
```
ext_{owner}_{extension}_{table_name}
```

**Example — Batch (owns batch entities):**
```
S3: projects/{project}/ext/joleuger/batch/batches/{batch_id}.json

SQLite: ext_joleuger_batch_batches
        ext_joleuger_batch_items
```

#### Pattern B: Extension columns (append to a base table)

Use when the extension adds state to an existing core entity. The delta file in S3 is the **source of truth**; the extension's `Migrate` method may add one or more columns to the base table so queries are fast without joining a separate table. The DB is a cache — it can be silently dropped and rebuilt from S3 at any time.

**S3 (delta files — the truth):**
```
projects/{project}/ext/{owner}/{extension}/{table_name}/{table_element_id}.json
```

The delta file contains an `id` (foreign key to the base entity), a `version` (incremented on each modification), and the extension-specific fields. Files are created lazily (default = no file = default state).

The `extensions_metadata` table also serves as the core schema version table. Once the first public release ships, `MigrateSchema` (currently paused in `internal/data/sqlite.go`) will use this table's `core` entry to version-gate schema migrations — only running when the actual schema has evolved.

**SQLite (columns on the base table):** columns appended to the base table, prefixed `ext_{owner}_{extension}_`. The extension's `Migrate` method adds these columns **only on the first run**, guarded by the `extensions_metadata` table (created by core in `CreateSchema`).

**Migration guard — `extensions_metadata`:**
```sql
CREATE TABLE IF NOT EXISTS extensions_metadata (
    extension_key TEXT PRIMARY KEY,
    version       INTEGER NOT NULL
);
```
The extension's `Migrate` queries this table first. If no row exists for its key, it runs `ALTER TABLE` then inserts (`extension_key`, `version = 1`). On subsequent startups the row is found and `ALTER TABLE` is skipped.

**Example — Favorites (extends elements):**

The favorites extension adds a single boolean column `ext_joleuger_favorites_is_favorite` to the `elements` table. The S3 delta file remains the authoritative source; on startup `Sync` reads delta files and populates the column.

```
S3: projects/{project}/ext/joleuger/favorites/elements/{element_id}.json

SQLite (base elements table — core):
    CREATE TABLE IF NOT EXISTS elements (
        id TEXT PRIMARY KEY,
        -- …core columns…
    );

SQLite (extensions_metadata — created by core):
    CREATE TABLE IF NOT EXISTS extensions_metadata (
        extension_key TEXT PRIMARY KEY,
        version       INTEGER NOT NULL
    );

SQLite (favorites Migrate adds, once only):
    ALTER TABLE elements ADD COLUMN ext_joleuger_favorites_is_favorite INTEGER DEFAULT 0;
    INSERT OR IGNORE INTO extensions_metadata (extension_key, version)
        VALUES ('ext_joleuger_favorites', 1);
```

Delta file:
```json
{
  "id": "{element_id}",
  "version": 1,
  "is_favorite": true
}
```

**Rules common to both patterns:**
- **`id`**: foreign key to the base entity
- **`version`**: incremented on each modification (starts at 1)
- **Lazy creation**: no file = default state
- **Toggle back to default**: delete the file
- **Sync**: scan S3 prefix → parse JSON → populate SQLite on startup
- **Orphan cleanup**: sync compares delta files against the base entity table

### Rebuild

Core's `SyncFromStorage` doesn't know extension tables exist. Each extension provides its own `Sync()` function that reads from S3, run after core's sync at startup — otherwise a deleted `cache.db` silently loses extension state.

---

## Part 1.5 — Project Settings Delta Pattern

Extensions can store project-specific settings using a delta file pattern similar to Pattern B, but scoped to the project itself rather than to individual elements.

### S3 Layout

```
projects/{project}/ext/{owner}/{extension}/settings.json
```

### Delta File Structure

```json
{
  "id": "{project_name}",
  "version": 1,
  "field1": "value1",
  "field2": 42,
  "field3": true
}
```

The delta file does **not** include `owner` or `extension` fields — those are derived from the S3 path and not persisted in the delta itself.

### How It Works

1. **Lazy creation**: No delta file = default state (no S3 object)
2. **Write (extension-owned)**: The extension persists its own settings. It registers a `SettingsSavers` hook keyed `"owner/extension"`; the core settings endpoint dispatches the submitted fields to that saver, which validates them and writes its own settings file (typically its own typed struct, read-modify-write to S3). **Core never read-modify-writes an extension's delta** — it dispatches and reads only. `ProjectRepository.UpdateExtensionSettings` remains as a convenience for the generic bag-based idiom.
3. **Read**: Extension calls `ProjectRepository.GetExtensionSettings(project, owner, extension)` which returns the delta from S3 (or empty delta if not exists)
4. **Delete**: Extension calls `ProjectRepository.DeleteExtensionSettings(project, owner, extension)` which removes the delta file from S3
5. **Sync**: On startup, `SyncFromStorage` reads project.json from S3 (version-aware upsert), but extension delta settings are NOT synced — they're read on-demand when the settings API is called

### Repository Methods

```go
// GetExtensionSettings returns an extension's project settings delta file.
// Returns model.ProjectSettingsDelta{} with nil extFields if no delta file exists.
GetExtensionSettings(ctx context.Context, project, owner, extension string) (model.ProjectSettingsDelta, error)

// UpdateExtensionSettings writes an extension's project settings delta to S3.
// The S3 key is derived from project, owner, and extension.
UpdateExtensionSettings(ctx context.Context, project, owner, extension string, delta model.ProjectSettingsDelta) error

// DeleteExtensionSettings removes an extension's project settings delta from S3.
DeleteExtensionSettings(ctx context.Context, project, owner, extension string) error
```

### ProjectSettingsDelta Model

```go
type ProjectSettingsDelta struct {
    ID        string         `json:"id"`        // project name
    Version   int            `json:"version"`
    extFields map[string]any  // extension-specific fields (unexported)
}

func (d ProjectSettingsDelta) Field(key string) any {
    // Returns value by key, or nil if not set
}

func (d *ProjectSettingsDelta) SetField(key string, value any) {
    // Stores a field value by key (generic, no core knowledge of extensions)
}

func (d ProjectSettingsDelta) Fields() map[string]any {
    // Returns the full fields map (nil if none set)
}
```

`extFields` follows the same pattern as `Element.extFields`: unexported map with `Field(key)` for reads, `SetField(key, value)` for writes, and `Fields()` to serialize the full map.

Because a plain `json.Marshal` cannot see the unexported map, `ProjectSettingsDelta` implements custom `MarshalJSON`/`UnmarshalJSON`: it serializes as one flat object — `id` and `version` plus every entry of `extFields` — and parses unknown keys back into `extFields`. This keeps every bag-based **read** lossless — the settings page's two-pass render and extensions reading values via `delta.Field()` — and keeps the `UpdateExtensionSettings` convenience **write** path lossless (read a delta, `SetField`, write back, all fields preserved). With the `SettingsSavers` dispatch, core no longer performs that write: an extension that registers a section owns its write and typically maps the submitted fields into its own typed struct instead. Extension field names `id` and `version` are reserved and must not be used.

### Example: Extension Storing Project Settings

```go
// In extension code
func (e *Extension) SaveProjectSettings(ctx context.Context, project string, config Config) error {
    delta := model.ProjectSettingsDelta{
        ID:      project,
        Version: 1,  // Will be incremented by UpdateExtensionSettings
    }
    delta.SetField("max_concurrent", config.MaxConcurrent)
    delta.SetField("poll_interval", config.PollInterval.String())
    return e.cfg.ProjectRepo.UpdateExtensionSettings(ctx, project, e.owner, e.extension, delta)
}

func (e *Extension) LoadProjectSettings(ctx context.Context, project string) (Config, error) {
    delta, err := e.cfg.ProjectRepo.GetExtensionSettings(ctx, project, e.owner, e.extension)
    if err != nil {
        return Config{}, err
    }

    config := Config{} // defaults
    if v := delta.Field("max_concurrent"); v != nil {
        config.MaxConcurrent = v.(int)
    }
    if v := delta.Field("poll_interval"); v != nil {
        config.PollInterval, _ = time.ParseDuration(v.(string))
    }
    return config, nil
}
```

### Concrete Example: Photobooth (Project-level Pattern B)

The photobooth extension stores project-level settings as a delta file. On startup, its `Sync()` method reads the S3 delta and populates SQLite columns so reads are fast without joining tables.

| Aspect | Implementation |
|---|---|
| S3 delta | `projects/{project}/ext/joleuger/photobooth/settings.json` |
| SQLite columns | `ext_joleuger_photobooth_post_filter_prompt`, `ext_joleuger_photobooth_post_filter_reference_image` on `projects` table |
| Read | `GetPostFilter(ctx, project)` — reads from SQLite columns |
| Write | `SetPostFilter(ctx, project, prompt, refImage)` — S3 first, then SQLite |
| Clear | `ClearPostFilter(ctx, project)` — delete S3 delta, reset SQLite |
| Sync | `Sync(ctx, a)` — reads S3 delta, populates SQLite columns |

This pattern is identical to Pattern B for elements (Part 1), just scoped to the project instead.

### Why SQLite Cache for Extensions

S3 is the source of truth, but reading S3 on every request adds latency. The SQLite cache gives:

- **Fast reads** — `SELECT ext_joleuger_myext_field FROM projects WHERE id = ?` (indexed, fast)
- **Source of truth** — S3 delta file (survives cache drops, visible to other services)
- **Sync on startup** — `Sync()` repopulates cache from S3

### Pattern Template for Future Extensions

When adding a new extension, use this structure:

```go
// 1. Delta struct — mirrors the S3 JSON structure.
type myExtensionDelta struct {
    ID   string `json:"id"`
    Ver  int    `json:"version"`
    Foo  string `json:"foo"`
    Bar  int    `json:"bar"`
}

// 2. Read from SQLite cache.
func (e *Extension) GetFoo(ctx context.Context, scope string) (string, error) {
    var foo string
    err := e.db.QueryRowContext(ctx,
        `SELECT ext_joleuger_myext_foo FROM <table> WHERE id = ?`, scope,
    ).Scan(&foo)
    return foo, err
}

// 3. Write: S3 first, then SQLite.
func (e *Extension) SetFoo(ctx context.Context, scope, foo string) error {
    key := deltaKey(scope)

    // Read current delta from S3.
    var d myExtensionDelta
    body, _, err := e.storage.GetObject(ctx, key)
    if err == nil && body != nil {
        data, _ := io.ReadAll(body)
        body.Close()
        json.Unmarshal(data, &d)
    }

    // Update fields.
    d.ID = scope
    d.Foo = foo
    d.Ver++

    // Write to S3.
    data, _ := json.Marshal(d)
    if err := e.storage.PutObject(ctx, key, strings.NewReader(string(data)), int64(len(data)), "application/json"); err != nil {
        return err
    }

    // Update SQLite.
    _, err = e.db.ExecContext(ctx,
        `UPDATE <table> SET ext_joleuger_myext_foo = ? WHERE id = ?`, foo, scope,
    )
    return err
}

// 4. Sync — populate SQLite from S3.
func (e *Extension) Sync(ctx context.Context, a *app.App) error {
    // List all scopes from SQLite (populated by core's SyncFromStorage).
    rows, err := a.DB.QueryContext(ctx, `SELECT id FROM <table>`)
    if err != nil {
        return err
    }
    defer rows.Close()

    var scopes []string
    for rows.Next() {
        var id string
        rows.Scan(&id)
        scopes = append(scopes, id)
    }

    for _, scope := range scopes {
        syncFromS3(ctx, a.Storage, a.DB, scope)
    }
    return nil
}
```

### Potential Improvements (Deferred)

**Unit of Work for Extensions:** A helper that writes S3 + SQLite in a single call would eliminate the "S3 first, then SQLite" boilerplate:

```go
func UpdateExtensionDelta(ctx context.Context, repo data.ProjectRepository,
    project, owner, extension string, delta model.ProjectSettingsDelta) error {
    // 1. Write to S3 (increment version).
    // 2. Map delta fields → SQLite columns.
    // 3. UPDATE SQLite.
}
```

This requires knowing which delta fields map to which SQLite columns — extension-specific. **Verdict:** defer until the pattern repeats 3+ times.

---

## Part 0.5 — Extension Lifecycle Gate (`enabled` flag)

Each bundled extension has an `enabled` config flag under `extensions.owner/name.enabled` in `config.yaml` (default `true`). The `ext.RegisterAll` function checks this flag for every extension — if disabled, it skips migration, route registration, hooks, and sync. The gate lives in `ext/extensions.go` and is the single point of truth for extension lifecycle.

**Example:**
```yaml
extensions:
  joleuger/batch:
    enabled: false
  joleuger/photobooth:
    enabled: true
```

When disabled, the extension creates no tables, registers no routes, fires no hooks, and its UI elements are hidden via the `hasExtension` template function.

---

## Part 2 — Go Hooks

A hook is a callback registered in `ext/extensions.go::RegisterAll()`, called from exactly one place in core. Empty by default — core behaves identically with zero hooks.

**Call sites:**
- **`OnJobTerminal`** — called when a job reaches a terminal status (`completed`, `failed`, `cancelled`), *before* the job row is pruned from the `jobs` table.
- **`OnProjectDeleted`** — called after a project is deleted (core's S3 objects + SQLite rows removed)

**Rules:**
- **Hook errors are logged, never fatal, never block other hooks or other jobs.** Core iterates the slice, calls each registered function, logs any error, and continues.
- **Order is registration order** — in `ext/extensions.go`, where extensions are constructed and appended one by one.
- **`OnJobTerminal` fires *before* the job row is pruned.** The `jobs` table is runtime-only: terminal rows are deleted immediately after `OnJobTerminal` fires. Extensions must not rely on job rows persisting for post-hoc queries — use their own tables (Pattern A) or S3 delta files (Pattern B) for durable job tracking.

**Adding a new hook (future):** add the field to the `Hooks` struct in `internal/server/app.go` and one call site. No feature logic, no new dependencies, no behavior change when the slice is empty.

---

## Part 3 — UI Slots

Extensions can inject HTML fragments into core pages. All slots are `template.HTML` fields — extensions append to `a.Hooks.{SlotName}` in `ext/extensions.go`.

> **Entry points on core pages are NOT hooks.** The *entry point* an extension gets on a core page (a button, a nav link) is hard-coded in the core template behind `{{ if hasExtension "owner/name" }}` — the same pattern as the favorites filter button in `gallery.html` or the slideshow button. New generic HTML-injection hooks are not added for this: UIs are too specific for hooks, and injection slots tend to disturb the UX. If your extension needs an entry point on a core page, add a small guarded slot to the core template instead. The hook slots below are for supplementary content (cards, actions, settings sections), not entry points. One documented exception: `WelcomeExtras` — the welcome page has no project context, so a `hasExtension` guard around a core template slot would hard-code a single provider; the slot exists precisely for the onboarding provider's banner.

### DashboardExtras

Injected into the project dashboard (below the generate form, above the elements grid). Rendered only on the project dashboard page.

### NavBarItems

Injected into the project navigation bar. Rendered on every project-scoped page (dashboard, gallery, element detail).

### ElementActions

Injected into the element detail page, below the primary action buttons ("Reuse Settings", "Retry", "Download"). Rendered only on the element detail page.

### MoreNavItems

Injected into the "More" (⋯) dropdown menu in the project navigation bar. Rendered on every project-scoped page (dashboard, gallery, element detail, batch progress). Each item is a plain `<a>` tag. Photobooth uses this slot — the link renders as `📷 Photobooth`.

### WelcomeExtras

`[]func(ctx context.Context) (template.HTML, error)` — no project argument (the welcome page has none). Injected into the welcome (project selection) page after the project grid. The onboarding extension uses it for its "Setup & Customize →" banner.

### Inline JS

Extensions that register UI hooks should use inline `<script>` in their own pages or rely on existing inline handlers in core templates (e.g., `toggleGalleryFav` in `gallery.html`). Keep extension UI scripts self-contained.

### SettingsField (deprecated)

> **Deprecated.** Use `SettingsSection` instead (see below). Core still adapts `SettingsField` into sidebar entries via `projectSettingsPage` handler for backward compat, but new extensions should use `SettingsSection`.

Injected into the project settings modal, rendered after the built-in fields (backend selector, hidden toggle, description, tags). Called on the settings modal load (GET /api/{project}/settings) — each field is rendered once and the browser populates it from the API response.

**Rules:**
- **Return `template.HTML`** — the HTML fragment to inject
- **Add `data-extension` attribute** — format: `data-extension="owner/extension"` so the browser can identify and save extension fields
- **Store fields for save** — use `data-field="my_field"` so the browser can collect values on save

**Example:**
```go
func (e *Extension) registerSettingsField(a *app.App) {
    a.Hooks.SettingsField = append(a.Hooks.SettingsField,
        func(ctx context.Context, project string) (template.HTML, error) {
            return template.HTML(fmt.Sprintf(`
                <div class="form-group">
                    <label for="settingsMyField">My Setting</label>
                    <input type="text" id="settingsMyField"
                           data-extension="joleuger/myext"
                           data-field="my_field"
                           value="%s">
                </div>`, defaultValue)), nil
        },
    )
}
```

### SettingsSection (new — preferred)

Replaces `SettingsField` with a scoped section approach. Each extension registers a `SettingsSection` that returns a `*server.Section` with its own sidebar nav item, form fields, and field metadata for client-side change tracking.

**Rules:**
- **Return `*server.Section`** — the section has `ID`, `Label`, `HTML`, and `Fields`
- **Section ID** — use the format `"owner/extension"` (e.g. `"joleuger/photobooth"`). `"core"` is reserved for core project settings.
- **Label** — display name shown in the sidebar nav
- **HTML** — form content for this section. Use `data-section="owner/extension"` and `data-field="my_field"` attributes on inputs so the browser can snapshot values and detect changes.
- **Fields** — list of `server.FieldInfo{Key, Type}` describing each form field. Enables client-side change tracking without DOM introspection.
- **Read from delta** — the hook receives `model.ProjectSettingsDelta` parameter. Read field values from `delta.Field("my_field")` (reads S3 delta), not from SQLite. The SQLite column is only used by the post-filter job path.

**Example:**
```go
func (e *Extension) SettingsSection(ctx context.Context, project string, delta model.ProjectSettingsDelta) (*server.Section, error) {
    // Read values from S3 delta (authoritative source for settings).
    val := delta.Field("my_setting").(string)

    return &server.Section{
        ID:    "joleuger/myext",
        Label: "My Extension",
        HTML: template.HTML(fmt.Sprintf(`
            <div class="form-group">
                <label for="settingsMyField">My Setting</label>
                <input type="text" id="settingsMyField"
                       data-section="joleuger/myext" data-field="my_setting"
                       value="%s">
            </div>`, val)),
        Fields: []server.FieldInfo{
            {Key: "my_setting", Type: "text"},
        },
    }, nil
}
```

**Registration:**
```go
func (e *Extension) RegisterHooks(a *app.App) {
    if a.Hooks != nil {
        a.Hooks.SettingsSection = append(a.Hooks.SettingsSection, e.SettingsSection)
    }
}
```

**Browser integration:** The settings page JavaScript reads the section's `Fields` metadata to snapshot values on load and compare on input. On save, it reads actual DOM values (not cached API response) and POSTs `{section: "joleuger/myext", fields: {my_setting: "value"}}`. The handler routes on `section` to update only that extension's delta.

**Migration from SettingsField:** Extensions currently using `SettingsField` can migrate to `SettingsSection` by:
1. Replacing the `SettingsField` hook registration with `SettingsSection`
2. Reading values from `delta.Field()` instead of from SQLite or other sources
3. Adding `data-section="owner/extension"` and `data-field="my_field"` attributes
4. Returning a `*server.Section` with `Fields` metadata

Core's `projectSettingsPage` handler still adapts `SettingsField` hooks into sidebar entries via HTML attribute parsing, so old extensions continue to work without changes. However, new extensions should use `SettingsSection` directly.

### SettingsSavers (write path — required with SettingsSection)

`SettingsSection` is the **read/render** side; `SettingsSavers` is the **write** side. Every extension that registers a `SettingsSection` must register a saver for the same section ID. On a scoped save (`POST /api/{project}/settings` with `{section, fields}`), core looks up the saver and dispatches the raw submitted fields to it. The extension is solely responsible for validating the fields and persisting its own settings — core never reads, modifies, or writes an extension's settings file on save (it only reads deltas to render the settings page).

**Contract:**

- **Key**: `"owner/extension"` — same string as the section ID.
- **Type**: `server.SettingsSaver` = `func(ctx context.Context, project string, fields map[string]any) error`. `fields` is the raw submitted map — checkbox values arrive as JSON booleans, text/number inputs as JSON strings (or numbers), so validate types, don't assume.
- **Validate first, write second**: reject unknown keys and wrong types before touching storage; a failed validation must not write anything.
- **Error contract**: return `&server.ValidationError{Message: ...}` for invalid input → the endpoint answers **400** with that message. Any other error → **500**. No saver registered for the section → **400** ("no settings handler for section …"). The legacy full-payload path (`extension_settings` map) dispatches per extension the same way but is best-effort: unknown sections are skipped with a warning and saver errors are logged, not returned.
- **Persistence**: map the validated fields into your own settings struct and read-modify-write your own S3 file (increment `version`). If you mirror hot-path fields into the `projects` table, update those columns in the same saver.

**Registration (photobooth):**

```go
func (e *Extension) RegisterHooks(a *app.App) {
    if a.Hooks != nil {
        a.Hooks.SettingsSection = append(a.Hooks.SettingsSection, e.SettingsSection)
        if a.Hooks.SettingsSavers == nil {
            a.Hooks.SettingsSavers = map[string]server.SettingsSaver{}
        }
        a.Hooks.SettingsSavers["joleuger/photobooth"] = e.saveProjectSettings
    }
}
```

The photobooth saver validates each field against its field map (unknown keys rejected, strict string/bool types, `max_photos` clamped to 1–10), then read-modify-writes its own `photoboothSettings` struct to the delta file and mirrors the hot-path columns (`post_filter_prompt`, `post_filter_reference_image`, `trigger_binding`) into the `projects` table.

---

## Part 3.5 — Extension-Defined Routes

Extensions can register their own HTTP routes on the core serve mux under a dedicated prefix `/{project}/ext/{owner}/{extension}/`. This gives extensions their own endpoints that never collide with core routes, and keeps core code free of extension-specific business logic.

### Why This Pattern

When an extension needs to:
- Provide a custom form (e.g., batch creation from the generate form)
- Serve a dedicated page (e.g., batch progress page)
- Return JSON data for AJAX polling (e.g., batch status API)

Instead of intercepting core routes (which couples to the internal mux), register a dedicated route. The browser's JavaScript calls the extension endpoint directly, and the UI routes to it based on context (e.g., single job vs. batch).

### How It Works

```go
// In ext/joleuger/batch/routes.go
func (e *Extension) RegisterRoutes(a *app.App) {
    e.mux.HandleFunc("POST /api/{project}/ext/joleuger/batch/preview", e.handlePreview)
    e.mux.HandleFunc("POST /api/{project}/ext/joleuger/batch/generate", e.handleBatchGenerate)
    e.mux.HandleFunc("GET /basic/{project}/batch/{id}", e.handleBatchPage)
    e.mux.HandleFunc("GET /api/{project}/batch/{id}/api", e.handleBatchAPI)
}
```

The `{project}` path param is resolved via Go 1.26 `r.PathValue("project")`. Routes are registered on `a.GetServeMux()` — the same mux core uses — so they serve alongside core handlers. The `stripPrefix` middleware (applied once at the top level) ensures that when `server.path_prefix` is configured, extension routes are automatically reachable under the prefix too.

**HTML pages from extensions** that generate links must use the same `{{ urlPath "..." }}` / `url("...")` helpers as core templates to remain prefix-aware. The `prefix` and `urlPath` template functions are injected into every render.

### UI Integration

The browser's JavaScript in `project.html` decides which endpoint to call based on the Preview button's expansion result:

```javascript
// On Preview click
const resp = await fetch(`/${project}/ext/joleuger/batch/preview`, {
    method: 'POST',
    body: new URLSearchParams({prompt, seeds})
});
const data = await resp.json();

if (data.count === 1) {
    // Single variant → core generate handler
    document.getElementById('generateForm').action = `/${project}/generate`;
    form.submit();
} else {
    // Multiple variants → batch extension handler
    document.getElementById('generateForm').action = `/${project}/ext/joleuger/batch/generate`;
    form.submit();
}
```

### Route Ordering

Batch's extension routes are registered **before** core routes in `ext/extensions.go::RegisterAll()` so they take precedence. However, the `/{project}/ext/joleuger/batch/` prefix is so specific that collisions with core routes are impossible — the prefix is unique.

### Best Practices

- **Use the `/{project}/ext/{owner}/{extension}/` prefix** — makes extension ownership and purpose clear from the URL
- **Register routes in `RegisterRoutes(a)`** — same as hook-based extensions; called during `RegisterAll`
- **Return 303 See Other for form submissions** — POST → redirect pattern avoids double-submissions on page reload
- **Return JSON for AJAX endpoints** — use `w.Header().Set("Content-Type", "application/json")`
- **Read `project` from `r.PathValue("project")`** — Go 1.26 path parameter extraction

### Client-Side Element Lists: use `GET /api/{project}/elements`

When an extension page needs an element list client-side (a slideshow queue, a picker, a poller), fetch the core elements API — **never parse gallery HTML**:

```
GET /api/{project}/elements?page=1&per_page=500&sort=created_at&order=desc&favorites=true
```

- **Envelope:** `{elements: [...], total, page, per_page, total_pages}` — `elements` is an array (never `null`).
- **Params:** `page`, `per_page` (any int, or `all` for everything), `sort` (`created_at` | `seed` | `model_name`), `order`, `origin`, `favorites` — plus **any filter your extension registered with the query builder** (Part 7) can be passed as a plain URL param; unregistered names are silently ignored. This is what makes the API safe to reuse: an extension's registered filters become URL-shareable without any handler change.
- **Auth:** `ActionView` — same standing as the gallery page.

Reference implementations: the slideshow queue (`ext/joleuger/slideshow/slideshow.html`) and the img2img reference picker (`loadRefImages` in `internal/server/templates/project.html`).

---

## Part 4 — Composition Root (`ext/extensions.go`) and the Extension Contract

Bundled extensions are wired directly into the one binary. The composition root is in `ext/extensions.go`, and the contract it manages is the `ext.Extension` interface (`ext/ext_interface.go`):

```go
type Extension interface {
    Key() string                              // "owner/name" — matches config key + S3 ext/ namespace
    Enabled(cfg *config.Config) (bool, error) // reads the extension's config block
    Dependencies() []extdep.Dependency        // nil = no dependencies
    Migrate(ctx context.Context, db *sql.DB) error
    Initialize(ctx context.Context, a *app.App) error // construct state, register hooks/routes
    Sync(ctx context.Context, a *app.App) error       // S3 → SQLite after core sync
}
```

Each bundled extension package provides `var Descriptor = descriptor{}` (see `ext/joleuger/*/descriptor.go`); the composition root lists them in `var Bundled = []ext.Extension{...}`. Extension packages **never import `ext`** — the descriptors implement the interface structurally at the `Bundled` assignment. This avoids an import cycle (`ext` imports `app` for the `*app.App` parameter, and extension packages import `app` to read it).

**Lifecycle ordering** (in `cmd/app/main.go`):

```
RegisterAll()   → resolve enabled extensions → build + validate dependency graph
                  → ordered Migrate → set app.ExtDeps → ordered Initialize
                  → hook/route wiring (before sync)
SyncAndCleanup() → cancel stuck jobs + core sync from S3
SyncAll()       → extension sync from S3 (reverse of init order)
Serve()         → ready
```

**Why `RegisterAll` before `SyncAndCleanup`?** Hook/route registration is pure wiring (no I/O) and must happen *before* `SyncAndCleanup` (which runs `CancelStuckJobs`). Hook firings from startup cleanup must reach registered listeners.

**Migration before querybuilder registration:** `RegisterAll` runs migrations **first**, then constructs extensions (which register querybuilder contributions). This ensures the schema exists before the query builder adds extension columns to queries — otherwise the very first dashboard request could hit "no such column" errors because the query builder has registered `ext_joleuger_favorites_is_favorite` but the column hasn't been added yet.

**Why `SyncAll` after `SyncAndCleanup`?** Extension S3 sync must happen *after* core's `SyncFromStorage`, so extension foreign keys resolve against rows that already exist.

**Dependency ordering** is computed, not manual: `RegisterAll` initializes extensions in dependency order (see Part 4.5), so required dependencies are always initialized before their dependents.

---

## Part 4.5 — Extension Dependencies (`internal/extdep`)

Extensions may depend on other extensions. The dependency graph **must stay acyclic**, and every dependency must be documented in the depending extension's `EXTENSION.md` (Dependencies section) — the graph is a contract, not an implementation detail.

**Kinds:**

| Kind | Meaning | Enforcement |
|---|---|---|
| `extdep.CompileRequired` | The extension's Go code directly imports the other extension's package | The compiler already enforces it; declare it for graph completeness |
| `extdep.RuntimeRequired` | A disabled dependency would break the extension at runtime | `RegisterAll` fails startup when the dependency is disabled |
| `extdep.RuntimeOptional` | The extension degrades gracefully without the dependency | No startup failure; the extension checks availability at runtime |

`RuntimeOptional` is the kind for **UI-only reuse** — one extension's page calls the other's HTTP API from JavaScript, with no Go-level import.

**Declaring** dependencies (on the extension's descriptor):

```go
func (descriptor) Dependencies() []extdep.Dependency {
    return []extdep.Dependency{
        {Key: "joleuger/printer", Kind: extdep.RuntimeOptional},
    }
}
```

Bundled examples: photobooth declares `joleuger/printer` as `RuntimeOptional`
(UI-only reuse via the printer's HTTP API — degrades to Retake/Keep without it),
and printer declares `joleuger/imageproc` as `CompileRequired` (its Go code
imports imageproc's `Processor` for the crop pipeline — disabling imageproc
fails printer startup).

**Checking at runtime** (in the depending extension's code):

```go
// a.ExtDeps is the shared *extdep.Graph. RegisterAll sets it before the
// first Initialize. Both checks are nil-safe: a nil graph (e.g. an
// extension built via its New() in a unit test) reports everything
// disabled and not initialized.
if a.ExtDeps.IsEnabled("joleuger/printer") {
    // render the print UI / wire the feature
}
if a.ExtDeps.IsInitialized("joleuger/printer") {
    // safe to call into the other extension's Go code
}
```

**Validation** happens in `RegisterAll` over *all* declared edges (required and optional):

- Unknown dependency key → startup error
- Cycle (optional edges count) → startup error naming the cycle path
- Required dependency disabled → startup error
- Optional dependency disabled → fine; the dependent starts and must degrade

**Initialization order** uses Kahn's algorithm over required edges only (ties preserve the `Bundled` listing order), so a required dependency's `Initialize` — and its `MarkInitialized` — always precede the dependent's.

---

## Part 5 — Configuration

`Config.Extensions` / `ExtensionConfig` is already in `internal/config/config.go`. Extensions declare their settings as a typed struct + `LoadConfig` function:

```yaml
extensions:
  joleuger/batch:
    max_concurrent: 1
    poll_interval: 2s
```

```go
type Config struct {
    MaxConcurrent int           `yaml:"max_concurrent"`
    PollInterval  time.Duration `yaml:"poll_interval"`
}

func LoadConfig(cfg *config.Config) (Config, error) {
    c := Config{MaxConcurrent: 1, PollInterval: 2 * time.Second} // defaults first
    if err := cfg.ExtensionConfig("joleuger/batch", &c); err != nil {
        return c, err
    }
    return c, nil
}
```

If no configurable values are needed, skip this entirely.

**Gotcha:** `Config.Extensions` is `map[string]yaml.Node` (values), not `map[string]*yaml.Node` (pointers). `yaml.Unmarshal` doesn't populate pointer values inside a map.

---

## Part 7 — Query Builder (Filters, Columns, Joins)

Extensions can contribute SQL fragments to the core `ListElements` query via the **Query Builder** registry in `internal/data/querybuilder/`. The core repository builds a base query (FROM, base SELECT, WHERE, ORDER, LIMIT, OFFSET). Extensions independently register filters, columns, and joins — the core repository has no extension-specific SQL branches.

This is the **Specification/Query Object pattern** applied to SQL generation. Neither extension knows the other exists.

### How It Works

```
GET /basic/:project/gallery?favorites=true&sort=created_at
                │
                ▼
 Handler ────► opts.Filters["favorites"] = "1"
                │
                ▼
 Repository  ──► q := Query{FROM, BaseSelect, WHERE, ORDER, LIMIT}
                │  ┌──────────────────────────────────┐
                └─►│ ApplyFilters(q, opts.Filters)    │
                   │ ApplyJoins(q)                     │
                   │ ApplyColumns(q)                   │
                   └──────────────────────────────────┘
                │
                ▼
 SELECT e.id, e.ext_joleuger_favorites_is_favorite, ...
 FROM elements e
 WHERE e.project = ? AND e.ext_joleuger_favorites_is_favorite = 1
 ORDER BY created_at DESC LIMIT 24 OFFSET 0
```

### Extension API

Each extension implements a `Register(b *querybuilder.Builder)` method called during `RegisterAll`:

```go
func (e *Extension) Register(b *querybuilder.Builder) {
    // 1. Filter: "favorites" query param → WHERE e.ext_joleuger_favorites_is_favorite = ?
    b.AddFilter(querybuilder.Filter{
        Name: "favorites",
        Apply: func(q *querybuilder.Query, value any) {
            q.AddWhere("e.ext_joleuger_favorites_is_favorite = ?", 1)
        },
    })

    // 2. Column: always select the extension column so ListElements
    //    scans it; the core stores it generically via SetField(col, value).
    b.AddColumn("e.ext_joleuger_favorites_is_favorite")
}
```

### Registering Filters

```go
b.AddFilter(querybuilder.Filter{
    Name: "myfilter",  // matches URL param: ?myfilter=123
    Apply: func(q *querybuilder.Query, value any) {
        // value is always a string (from URL params)
        // Add WHERE conditions with any number of args
        q.AddWhere("ext_my_table.my_field >= ?", 42)
    },
})
```

Filters are deduplicated by `Name` — the last registration wins. If the URL param is absent (`""`), `ApplyFilters` skips it.

### Registering Columns

```go
b.AddColumn("e.ext_my_column")
```

Columns are appended to the core's base `SELECT` clause. The core's `ListElements` scans all columns (base + extension) in order. Extension column values are stored generically via `elem.SetField(col, *ptrVal)` — keyed by column name into an internal map. Templates access them via `getField` / `getFieldInt` template functions.

**No manual switch case required.** The core's populate callback is fully generic: it iterates all registered columns and calls `SetField(col, value)` for each scanned extension column. Extensions do not need to modify core code when adding a new column.

### Registering Joins

For extensions that own separate tables (Pattern A from Part 1), join the table:

```go
b.AddJoin(querybuilder.Join{
    SQL:   "JOIN ext_joleuger_batch_items i ON i.batch_id = e.id",
    Alias: "ext_joleuger_batch_items",
})
```

Joins are deduplicated by `Alias`. If two extensions register the same alias, the first wins.

### Handler Side

The handler populates `opts.Filters` from URL query params and passes it to the repository:

```go
opts := data.ListOptions{Page: page, PerPage: perPage}
opts.Filters = make(map[string]string)
if r.URL.Query().Get("favorites") == "true" {
    opts.Filters["favorites"] = "1"
}
```

### Rules

- **Core has no extension-specific SQL branches.** `element_repo.go` knows nothing about any extension. It builds a base query and delegates to the registry.
- **Extensions are independent.** Neither the favorites extension nor any future extension knows about the other. They register with the same `Builder` and the core combines their contributions.
- **Nil builder is safe.** If `r.qb` is `nil` (tests, minimal setups), `ListElements` skips `ApplyFilters`/`ApplyJoins`/`ApplyColumns` — the base query works without any extensions.
- **Extension columns must be registered.** If an extension adds a column via `Migrate`, it must also register that column via `b.AddColumn()`, otherwise the column won't be in the `SELECT` and `ListElements` won't scan it.
- **Column scan is generic.** Extension columns are stored via `elem.SetField(col, value)` into an internal map. Templates access them via `getField` / `getFieldInt` — no core-level knowledge of specific extensions is needed.

---

## Part 6 — Documentation Convention

Each extension keeps one combined doc: `ext/{owner}/{extension}/EXTENSION.md` — purpose, S3 layout, SQLite schema, hooks used, UI slots used, routes, minimum Base version, known limitations. All in one file next to the code.

**Rule:** a change to an extension updates that extension's own `EXTENSION.md`, never the root docs — unless the change adds/removes a hook, a slot, a route, or the extension itself, in which case AGENTS.md and README.md also need updating.

This is a new leaf on AGENTS.md's existing "Documentation Maintenance" decision tree:

```
Did the change only affect a bundled extension (ext/{owner}/{name}/)?
└── Yes → Update that extension's own EXTENSION.md, not the root docs.
          Only touch root AGENTS.md/README.md if the extension's presence,
          a hook, a slot, or a route changes.
```
