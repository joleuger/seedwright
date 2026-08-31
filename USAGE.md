# Usage Guide

## Scope

**What this is:** The end-user guide — how to install, configure, run, and use the application. Target audience: operators and end users.

**What's in here:** Quick start, configuration, running the server, UI usage, API reference (browser routes), storage layout overview, testing commands, troubleshooting.

**What's NOT here:** Implementation details, architecture diagrams, data model specifics, test strategies, sdcpp API internals. For those, see ARCHITECTURE.md, DATA-MODEL.md, AGENTS.md, TESTING.md, SDCPP-API.md respectively.

---

## Table of Contents

- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [Running](#running)
- [Using the UI](#using-the-ui)
- [API Reference](#api-reference)
- [Storage Layout](#storage-layout)
- [Testing](#testing)
- [Troubleshooting](#troubleshooting)

---

## Using the UI

### Project Selection (Home Page)

The landing page (`/`) shows all known projects as cards. Clicking a project navigates to its dashboard. [📸 Screenshot: Home page with project cards]

Projects are **not auto-created** on visit. Project names are validated against `^[a-zA-Z0-9][a-zA-Z0-9._-]*$`:
- Invalid names (e.g., `favicon.ico`, paths with spaces) are rejected immediately
- Valid but non-existent names show an error with a **"Create Project"** button
- Clicking the button creates the project and navigates to its dashboard

Under the grid, a **✨ "Setup & Customize"** banner links to the wizard (provided by the onboarding extension; hidden when `application.onboarding: "none"`).

### Setup & Customize (`/onboarding`)

The setup wizard doubles as a permanent customize page:

1. **Current setup** — live status of the config file (including whether the wizard may write it), storage, and sdcpp backend (the backend probe reports the model name when reachable). If the config file sits on ephemeral storage (tmpfs / container overlayfs on Linux), a warning tells you a written config would be lost — **Download** is the alternative.
2. **What do you actually want? (profiles)** — one-click presets (try-it / photobooth / family-archive / minimal) that toggle the app title and extension on/off flags — never your storage. Picking one prefills the rest of the form.
3. **Step 1: Storage** — memory (ephemeral, capacity limit), local folder, or S3. **Verify** probes your choice before you commit; S3 is probed with the credentials you typed.
4. **Step 2: Backend** — the sdcpp base URL (conventional default `http://127.0.0.1:1234`). **Verify** fetches capabilities and shows the model. Links to the sdcpp install docs (Linux / Windows) and model selection (`MODEL_SELECTION.md`).
5. **Step 3: Name it** — app title + first project.
6. **Config preview** — the exact `config.yaml` the wizard would produce, updated live as you change fields. Two buttons:
   - **Write config file** — writes it to the path the app was started with. For an *existing* file the page shows an explicit confirmation checkbox, and the write is only possible if the *running* config allows it (`extensions.joleuger/onboarding.allow_config_write: true`); otherwise the button is disabled with an explanation and the page degrades to preview + download. A *missing* file (first run) is always writable.
   - **Download config file** — saves the same config (with real values, not masked) as `config.yaml` to your machine. Use it when writing is not allowed, or when the storage is ephemeral.
7. **CUSTOMIZE.md link** — a small box in the "What do you actually want?" section pointing at [CUSTOMIZE.md](https://github.com/joleuger/seedwright/blob/main/CUSTOMIZE.md) in the repo: the full scenario catalog (which scenarios need no code at all vs. which want a coding agent to build one small extension in a **fork**), copy-paste agent prompts, and how to fork the app down to the 5% you need.

Everything the wizard writes requires a **restart** to take effect — the app tells you so; there is no self-restart.

### Project Dashboard

The dashboard (`/basic/:project`) is the main workspace: [📸 Screenshot: Project dashboard with all sections visible]

1. **Generate Form** — Enter a prompt and adjust parameters:

   | Field | Default | Description |
   |---|---|---|
   | Prompt | *(required)* | Text description of the image you want |
   | Negative Prompt | `""` | Things to exclude from the image |
   | Width | `512` | Image width (multiple of 64, range 64–2048) |
   | Height | `512` | Image height (multiple of 64, range 64–2048) |
   | Steps | `20` | Sampling iterations (20–40 for quality) |
   | CFG Scale | `7.0` | How closely to follow the prompt (higher = stricter) |
   | Seed | `-1` | Random (-1) or a fixed seed for reproducibility |

2. **Active Jobs** — When a job is running, this card shows:
   - Animated spinner with status label (queued / generating)
   - Link to the element detail page
   - Cancel button to abort the job
   - **"Cancel All"** button — aborts all active jobs at once
   - Polls every 3 seconds with **partial-DOM update** (no page reload)
   - Shows "All jobs completed" when all jobs finish; polling stops automatically

3. **Recent** — Last 8 generated images in a grid. Click to view details.

4. **Help** — Quick-start prompts and usage tips.

#### img2img Generation (Denoise)

img2img generates a new image from one or more reference images. Access it from the project dashboard — a **"Reference Images"** button and a **"Denoise Strength"** slider appear below the Negative Prompt field.

1. Click **"+ Add"** to open the image picker and select reference images from External or Gallery
2. Adjust the **Denoise Strength** slider (0.0 = keep original, 1.0 = full noise; default 0.75)
3. Enter a prompt describing the desired edit
4. Click **Generate** — a new element with `origin: "generated"` and `task: "img2img"` is created

img2img-specific parameters:

| Field | Default | Description |
|---|---|---|
| Strength | `0.75` | Denoise strength (0.0–1.0); lower = more like the reference image |
| Reference Images | *(required)* | One or more external or gallery images to use as input |

#### The "More" Menu (⋯)

To the right of the Dashboard and Gallery links in the navigation bar is a "⋯" button. Clicking it opens a dropdown menu where extensions register their links. Currently:

- **📷 Photobooth** — opens the photobooth project selection index (`/photobooth/`)

#### Form Persistence

Your form inputs are saved to `localStorage` (key: `sdcpp_form_{project}`). When you revisit the dashboard, your last entered values are restored.

#### Randomize Seed (🎲)

Next to the Seed field is a 🎲 button. Clicking it generates a pseudo-random number in [0, 2147483647] using a glibc LCG seeded with `Date.now()`. The generated value fills the Seed field immediately, ready to submit.

#### Reuse Settings

On the Element Detail page, click the **"⟳ Reuse Settings"** button to copy that element's parameters (prompt, negative prompt, width, height, steps, CFG scale, seed) **and** its reference images back to the generate form on the dashboard. If the element was generated from reference images (img2img), those element IDs are stored and the dashboard populates reference image thumbnails on load. Clicking the button stores the params in `localStorage` and redirects you to the project dashboard, where the form auto-fills. Reuse settings takes priority over normal form persistence — if both exist, reuse wins and is then cleared from storage.

#### Add as Reference

On the Element Detail page, click the **"+ Add as Reference"** button to add that element's image as a reference for img2img. The button stores the element ID in `localStorage` and redirects you to the project dashboard, where a small thumbnail of the referenced image appears below the strength slider. Click a thumbnail to remove it from the reference list. This is a lightweight way to build a set of reference images without using the picker modal — useful when you already have the element open.

### Gallery

The gallery (`/basic/:project/gallery`) shows all generated images in a paginated grid:

- **Images per page:** select 24, 50, 200, or All via the dropdown
- **Sort by:** created_at (default), seed, or model_name — select from the dropdown
- **Order:** descending (newest first) by default
- Elements with metadata but no image file show a placeholder: *"Image missing — regenerate below"*

### Slideshow

The **▶ Slideshow** button on the gallery (visible when the slideshow extension is enabled) plays the gallery's current selection fullscreen: the sort, order, origin, and favorites filters you have applied are carried over, pagination is ignored, and the first up to 500 matching elements become the queue. Each slide shows for 4 seconds (progress bar at the bottom) with a caption of prompt, seed, model, and date; the next slide is preloaded so advancing is seamless. Missing images are skipped automatically.

Controls:

- **Space** (or the ⏸/▶ button) — play / pause
- **← / →** — previous / next slide
- **Click the image** — open the element detail page
- **Esc** (or the ✕ button) — back to the gallery with the same filters

Disable it with `extensions.joleuger/slideshow.enabled: false` in the config.

### External Images

The external images page (`/basic/:project/external`) shows all non-generated images in a project — images that were uploaded or captured (e.g., from Photobooth) rather than generated by sdcpp. These elements have `origin: "external"` and no generation metadata (no prompt, seed, or parameters).

There are three ways to upload images:

1. **Click** the drop zone to select a file from your computer (PNG or JPEG).
2. **Drag & drop** one or more images onto the drop zone.
3. **Paste** an image from the clipboard (Ctrl+V / Cmd+V) anywhere on the page.

The image is immediately saved to S3 and an element is created. You are redirected to the element detail page.

External images appear in the gallery filterable via the URL `?origin=external`. They can be used as reference images for img2img.

### Element Detail

The element page (`/basic/:project/element/:id`) shows: [📸 Screenshot: Element detail page with image and metadata]

- The full-size generated image
- All metadata: prompt, negative prompt, seed, dimensions, steps, CFG scale, model
- **Regenerate button** — always visible when element metadata exists
- **Retry button** — appears when job status is "failed" or "cancelled"
- **External elements** (`origin: "external"`) have no generation metadata — only image dimensions and creation date are shown

#### Regenerating an Image

Use Regenerate to recreate the image **in-place** — same element ID, same seed, same parameters. This is useful when:

- The sdcpp backend was interrupted or the image was deleted accidentally
- You want to regenerate a whole image library from the cheap metadata stored in S3

1. Navigate to the element detail page (`/basic/:project/element/:id`)
2. Click the **"Regenerate"** button
3. If a job is already running for this element, it is cancelled first
4. A new job is submitted with the **exact same parameters** (prompt, negative prompt, dimensions, steps, CFG, seed) and the same element ID
5. When the job completes, the new image replaces the old one
6. You're redirected to the same element detail page with the updated image

#### Retrying Failed or Cancelled Jobs

If a generation job has failed (e.g., sdcpp returned an error) or was cancelled, you can retry it from the element detail page:

1. Navigate to the element detail page (`/basic/:project/element/:id`)
2. Click the **"Retry"** button (visible when job status is "failed" or "cancelled")
3. A new job is created with the **same parameters** (prompt, negative prompt, dimensions, steps, CFG)
4. If the original seed was `-1` (random), a new random seed is generated
5. If the original seed was fixed, the same seed is used
6. The new job starts immediately and you're redirected to the new element's detail page

This is useful when:
- The sdcpp instance had a transient error
- You want to regenerate with a different random seed
- A previous job was cancelled and you want to try again

#### Delete an Image

To permanently remove a generated image and all its associated data:

1. Navigate to the element detail page (`/basic/:project/element/:id`)
2. Click the **"Delete"** button (red, below the metadata)
3. Confirm the deletion in the dialog
4. The image, element JSON, and all job records are removed from S3 and SQLite
5. You're redirected to the gallery

**Warning:** This action is irreversible. Deletion removes the PNG file from S3, the element JSON document, and all job records from SQLite.

**Project-strict:** Delete only removes the element if it belongs to the specified project. If the element ID exists under a different project, nothing is deleted and a warning is logged (this indicates a bug). If the element does not exist in SQLite at all, the operation succeeds silently.

#### Cancel All Stuck Jobs

When jobs appear stuck (e.g., after a restart), use the **"Cancel All"** button in the Active Jobs card on the dashboard:

1. Click **"Cancel All"** in the Active Jobs section
2. All jobs with status "queued" or "generating" are marked as "cancelled"
3. Jobs with a valid sdcpp job ID are also cancelled on the sdcpp side
4. The UI refreshes to show no active jobs

This is useful when:
- The process crashed and goroutines died but SQLite still has active job records
- You want to clean up stuck jobs from the UI
- After a restart, you want to clear stale jobs

#### Automatic Startup Cleanup

On every application startup, the system automatically cancels all stuck jobs (queued/generating with no active poller). This ensures that after a deployment or crash recovery, you never see "orphaned" jobs with spinning indicators.

### Batch Generate

Batch mode (implemented as an extension) lets you generate multiple images with the same prompt but different seeds, enqueued one at a time.

1. Enter seeds in one of these formats:
   - Comma-separated list: `42, 137, 999`
   - Range with step: `0-100` (every 10th seed: 0, 10, 20, ..., 100)
   - Mixed: `42, 137, 0-100, 10` (deduplicated, sorted)
2. Click **Generate** — the first seed starts immediately, subsequent seeds enqueue after each completes
3. Navigate to `/basic/:project/batch/:id` to watch progress (see [Batch Progress](#batch-progress) below)

#### Batch Progress

Navigate to `/basic/:project/batch/:id` to watch progress:
- Current seed, progress bar, completed/total count
- Generated images appear as they complete
- Auto-refreshes every 2 seconds
- Cancel button aborts the batch

#### Seed Parsing Formats

| Input | Parsed Seeds |
|---|---|
| `42` | `[42]` |
| `42, 137, 999` | `[42, 137, 999]` |
| `0-100` | `[0, 10, 20, 30, 40, 50, 60, 70, 80, 90, 100]` (step 10) |
| `0-100, 5` | `[0, 5, 10, ..., 100]` (explicit step 5) |
| `42, 137, 0-100, 10` | `[0, 10, 20, ..., 100, 42, 137]` (deduplicated, sorted) |
| `42, 42, 42` | `[42]` (deduplicated) |
| `abc` | Error: invalid seed format |

### Photobooth

The Photobooth extension provides a fullscreen camera page for quick image capture. Access it from the "More" (⋯) dropdown in the navigation bar.

**Getting started:**

1. Navigate to `/photobooth/` — the index page shows all known projects as tappable cards
2. Tap a project card to open the camera view (or tap the "＋" card to create a new project)
3. The fullscreen camera view opens with a live preview

**What it does:**
1. Opens a full-screen camera view with a live preview
2. Tap the large circular shutter button to capture a frame
3. Review the captured image in a confirmation overlay
4. Choose **Retake** (go back to camera) or **Keep** (save to project)
5. When kept, the image is saved directly to S3 and an element record is created

**Print directly from the preview (photobooth 2.0):**
When the printer extension is enabled and *Print in capture preview* is on (project setting, default on), the capture overlay replaces Keep with a print workflow in the proven layout of the old photobooth system:

- **Copy-count buttons** on the left — each button draws its number as overlapping photo icons (so the count is readable even for children who cannot read). Tap one to choose how many copies; the first button (1) is selected by default.
- **Print button** (🖨 icon) on the right — saves the photo to the project and submits a CUPS print job for the chosen copy count. A toast confirms the job.
- **✕ (Done)** — closes the preview (it is the only exit in print mode). Saves the photo if *Keep photo when closing the preview* is on (default on); discards it otherwise.

The printer is **not selected in the overlay** — in photobooth mode the person in front of the camera should never be asked a question. The operator configures it once per project (see *Printer* below in the project settings). The print button is enabled only when a printer is configured *and* currently available; otherwise it is disabled with a hint. If the save succeeds but the print job fails, the photo remains in the gallery and can be printed from its element page.

Without the printer extension (or with printing disabled in the project settings), the overlay shows the classic **Retake / Keep** buttons.

**Features:**
- Chromeless, fullscreen design — no nav bar, no scrollbars, optimized for iPad/tablet use
- Front/rear camera toggle button (📷 icon, top-right)
- Front camera preview is mirrored (like a real mirror/phone camera)
- Retina/HD resolution capture (auto-selects best available)
- Works on mobile and desktop (desktop: uses webcam)
- No sdcpp processing — captures are saved as-is

**Camera requirements:**
- Browser must support the MediaDevices API (`getUserMedia`)
- Camera permission must be granted
- HTTPS required on remote hosts (localhost is exempt)
- If accessed over plain HTTP on a remote address, a friendly error message appears instead of the camera view

**Supported browsers:** Chrome, Firefox, Safari, Edge (latest versions). On iOS, Safari is the default and fully supported.

**Captured images** are stored as `projects/{project}/elements/photobooth_{timestamp}.png` with origin `"ext/joleuger/photobooth"`. They appear in the gallery like any other element and can be regenerated, retried (not applicable — no sdcpp job), deleted, or favorited.

**Photobooth project settings** (project settings page → *Photobooth* section):

- **Post-filter (optional txt2img pass)** + **reference image element ID** — an optional second generation pass after capture (see EXTENDING.md, Part 1.5)
- **Capture trigger key** — bind a Bluetooth-remote key to the shutter
- **Print in capture preview** — toggle the print workflow in the capture overlay (default on; also requires the printer extension to be enabled)
- **Printer** — the printer the capture preview prints to. The list is loaded from the printer extension (configured printers only); the saved choice survives even if the printer extension is later disabled
- **Max copies** — upper bound of the copy-count buttons, 1–10 (default 5)
- **Keep photo when closing the preview** — save the photo when the overlay is closed with ✕ (default on)

### Printer

The printer extension prints element images via [CUPS](https://www.cups.org/) (`lp`). On the element detail page it offers a print dialog with a preview, printer selection, and copy count — including a *Preview crop* toggle that shows exactly what a crop printer will print. It also provides the print API used by the photobooth capture preview (see above).

**Configuration** (`config.yaml`):

```yaml
extensions:
  joleuger/printer:
    enabled: true
    rotate: "auto"                        # optional: "auto" (default) | "never" — crop printers only
    printers:
      - name: "Office"
        uri: "cups://printserver.local/printers/office"      # raw: prints the image as-is
      - name: "Dye-sub"
        uri: "cups://printserver.local/printers/dyesub"
        crop: true
        dimensions: "1800x1200"          # optional print canvas, gm-style "WxH"
```

- **Printers** — configured printers (any CUPS URI). The print dialog and the photobooth *Printer* setting list **configured printers only**; local printers discovered via `lpstat -p` are still returned by the raw `GET …/printers` API but do not reach those UIs.
- **`crop`** (per printer) — when `true`, the image is processed onto the printer's canvas (center-crop, ratio never distorted) by the `joleuger/imageproc` extension before printing. Printers without `crop` print the image as-is.
- **`dimensions`** (per printer) — the print canvas as gm-style `"WxH"`. With `crop: true` and no `dimensions`, the default canvas **1800×1200** is used.
- **`rotate`** — applied to crop printers: `"auto"` (default) rotates portrait images 90° before the center crop; `"never"` never rotates.
- **Processing engine** — crop processing is done by the `joleuger/imageproc` extension (engine `gm` by default; `gm` must be on the host's `PATH`). The printer extension declares imageproc as a **compile-required** dependency: with imageproc disabled, the printer cannot start.
- The image is always handed to `lp` as a **local file** (downloaded from storage at print time). The CUPS server never needs to reach this UI, so there is no base-URL setting.

### Project Settings

Each project has a settings gear icon (⚙) on its dashboard and project card. [📸 Screenshot: Settings modal with backend dropdown and hidden toggle]

The settings page (`/basic/{project}/settings`) has a sidebar with the **Core** section plus one section per extension that provides settings (e.g. *Photobooth*). Each section saves independently (the Save button is marked while it has unsaved changes). Extension sections are validated by the extension itself — invalid values (unknown fields, wrong types, out-of-range numbers) are rejected with an error message and nothing is saved.

**Core section:**

- **Friendly Name** and **Tags** — display metadata for the project
- **sdcpp Backend** — select which sdcpp backend instance to use (multi-backend support)
- **Hide from overview** — toggle to hide the project from the home page project selection grid
- **Description** — free-text project description

#### Delete a Project

To permanently remove an entire project and all its data:

1. Open the settings page (click the ⚙ gear icon on the dashboard)
2. Click **"Delete project"** at the bottom of the sidebar (red button)
3. Confirm the deletion in the dialog
4. All S3 objects, element records, and job records are removed
5. You're redirected to the home page

**Warning:** This action is irreversible. Deletion removes everything: all element JSON documents, all PNG images, and all SQLite records. There is no undo.

#### Multi-Backend Support

Configure multiple sdcpp backends in `config.yaml`:

```yaml
sdcpp:
  backends:
    - name: "production"
      base_url: "http://prod-server:1235"
    - name: "staging"
      base_url: "http://staging-server:1235"
```

Each project can independently select its backend. The JobService resolves the correct backend URL per-project via the `BackendResolver` callback.

Legacy config (`sdcpp.base_url`) is auto-migrated to `backends[0]` with name `"default"`.

---

## API Reference

---

## Quick Start

### Zero-config (first run)

No `config.yaml` needed. The app boots on first-run defaults (10 MB of
ephemeral in-memory storage + sdcpp at `http://127.0.0.1:1234`) and the
setup wizard is on by default:

```bash
make build
./app
```

1. Open [http://localhost:8080](http://localhost:8080) in your browser.
2. Click **"Setup & Customize"** (the ✨ banner under the project grid) — or go straight to [http://localhost:8080/onboarding](http://localhost:8080/onboarding).
3. Pick a profile ("What do you actually want?"), where your images live (memory / local folder / S3), and point the app at your sdcpp instance. Check the **Config preview**, then **Write config file** — on a first run there is nothing to overwrite, so the write always works — and restart. (If the wizard is not allowed to overwrite your existing config, use **Download** and place the file yourself.)
4. Restart, create a project, enter a prompt, and click **Generate**.

> **Docker:** `docker build -t seedwright . && docker run -d -p 8080:8080 -v seedwright-data:/data seedwright` — same first-run flow; `config.yaml` lands in the `/data` volume. sdcpp runs outside the container (GPU); the wizard points the app at it.

### Classic (edit the config yourself)

1. **Install Go 1.26+** and clone the repository.
2. **Copy `config.example.yaml` to `config.yaml`** and adjust sdcpp + storage.
3. **Build and run:** `make build && ./app`
4. Open [http://localhost:8080](http://localhost:8080), select or create a project, generate.

---

## Configuration

All configuration lives in `config.yaml` (or a file of your choice, passed as a CLI argument).

### Full Reference

```yaml
# ─── Server ───────────────────────────────────────────────
server:
  listen: ":8080"              # HTTP listen address (host:port)
  path_prefix: ""              # Reverse-proxy subpath prefix (default "" = root)
                               # e.g. "/sd" → all routes served at /sd/basic/..., /sd/api/...

# ─── sdcpp Backends ──────────────────────────────────────
sdcpp:
  backends:
    - name: "default"
      base_url: "http://127.0.0.1:1234"  # Base URL of the sdcpp API (conventional default)
      architecture: "flux2"              # Model architecture — e.g. "flux2", "sd3", "sdxl"

# ─── Object Storage ──────────────────────────────────────
# type: "s3" (S3-compatible), "file" (local folder), or "memory"
# (ephemeral, first-run default).
storage:
  type: "s3"

  # S3 settings (required when type: "s3")
  endpoint: "http://localhost:3900"  # S3 endpoint URL
  region: "garage"                   # S3 region (used for signing)
  bucket: "sdcpp-outputs"            # Bucket name (created if absent)
  access_key: ""                     # Access key ID
  secret_key: ""                     # Secret access key
  force_path_style: true             # Use path-style URLs (required for Garage/minio)

  # Local folder (required when type: "file")
  # file_path: "/data/sdcpp-outputs"

  # Memory (type: "memory") — ephemeral, lost on restart. The device is
  # considered FULL at the capacity limit; nothing is evicted.
  # capacity: "10MB"                 # default 10MB; binary units: "512KB", "1GB", "2048B"

# ─── SQLite Cache ────────────────────────────────────────
database:
  sqlite_path: "cache.db"            # Path to the SQLite database file

# ─── Application ─────────────────────────────────────────
application:
  title: "seedwright"             # Title shown in the browser tab
  default_project: "default"         # Name of the first project
  onboarding: "joleuger/onboarding" # Setup-wizard provider; "none" disables it

# ─── Test Flags ──────────────────────────────────────────
e2e:
  e2e_with_sdcpp: false              # Gate E2E tests behind sdcpp
```

### Required Fields (validated at startup)

| Field | Validation |
|---|---|
| `server.listen` | Must not be empty |
| `sdcpp.backends[*].base_url` | Must be a valid URL (http/https) for each backend |
| `storage.endpoint` + `storage.region` + `storage.bucket` | Required when `storage.type: s3` |
| `storage.file_path` | Required when `storage.type: file` |
| `storage.capacity` | Optional when `storage.type: memory` (default 10MB) — binary units or bare bytes |
| `database.sqlite_path` | Must not be empty |

### Defaults (applied when fields are empty)

| Field | Default |
|---|---|
| `server.listen` | `:8080` |
| `sdcpp.backends[0].base_url` | `http://127.0.0.1:1234` |
| `storage.capacity` | `10MB` (memory type only) |
| `database.sqlite_path` | `cache.db` |
| `application.title` | `seedwright` |
| `application.default_project` | `default` |
| `application.onboarding` | `joleuger/onboarding` (the bundled wizard) |
| `extensions.joleuger/onboarding.allow_config_write` | `false` — the wizard may not overwrite an existing `config.yaml` (a missing file is always writable) |

> **No config file at all?** That's fine — the app boots on first-run
> defaults (`memory` storage + `http://127.0.0.1:1234`) and the wizard
> at `/onboarding` writes `config.yaml` for you. A config file that
> *exists* but is invalid still fails at startup.

### Custom Config Path

```bash
# Default: uses config.yaml in the current directory
./app

# Custom path
./app /etc/seedwright/config.yaml
```

---

## Running

### Development

```bash
# Build and run in one command
make run

# Or manually
go build -o app ./cmd/app/
./app
```

### Production

```bash
# Static build (no CGO shared libs)
CGO_ENABLED=1 go build -o seedwright ./cmd/app/

# Deploy the binary + config.yaml anywhere
scp seedwright config.yaml user@server:/opt/seedwright/
ssh user@server 'cd /opt/seedwright && ./seedwright'
```

The binary is self-contained. No additional dependencies, libraries, or services are required beyond:

- The Go runtime (linked statically when built with `CGO_ENABLED=1` and appropriate linker flags)
- A running sdcpp instance
- An S3-compatible storage backend

### Graceful Shutdown

Send `SIGINT` or `SIGTERM` to stop the server gracefully:

```bash
kill -TERM $(pgrep seedwright)
```

---

## API Reference

### Browser Routes

Routes are namespaced: `/basic/` for HTML pages, `/api/` for JSON.
When `server.path_prefix` is set (e.g. `/sd`), the prefix wraps everything: `GET /sd/basic/:project`, `GET /sd/api/:project/jobs/active`.

All HTML routes return pages rendered from Go templates.

| Method | Path | Page |
|---|---|---|
| `GET` | `/` | Project selection |
| `POST` | `/create-project` | Create project from welcome page (JSON, redirects to `/basic/:project`) |
| `POST` | `/switch-backend` | Switch active sdcpp backend (JSON) |
| `POST` | `/api/:project/create` | Create project (JSON, redirects to `/basic/:project`) |
| `GET` | `/basic/:project` | Dashboard (generate form, active jobs, recent elements) |
| `POST` | `/api/:project/generate` | Submit single job (JSON) |
| `GET` | `/basic/:project/gallery` | Gallery grid with sort + favorites filter |
| `GET` | `/basic/:project/element/:id` | Element detail |
| `GET` | `/basic/:project/element/:id/image` | Raw PNG image bytes (proxy from S3) |
| `POST` | `/api/:project/element/:id/generate-clone` | Create new sibling element with same params but a new random seed (redirects) |
| `POST` | `/api/:project/element/:id/regenerate-in-place` | In-place image recreation with same seed and element ID (redirects) |
| `POST` | `/api/:project/element/:id/delete` | Delete element (redirects to gallery) |
| `POST` | `/api/:project/jobs/cancel-all` | Cancel all stuck jobs (JSON) |
| `GET` | `/api/:project/settings` | Project settings JSON |
| `POST` | `/api/:project/settings` | Update project settings JSON |
| `POST` | `/api/:project/delete` | Delete project (redirects to `/`) |
| `POST` | `/api/:project/favorites/toggle` | Toggle favorite (JSON) |
| `POST` | `/api/:project/elements/upload` | Upload external image (multipart/form-data) |
| `POST` | `/api/:project/elements/img2img` | img2img generation — creates element from reference images (JSON) |
| `GET` | `/basic/:project/external` | Upload page — click, drop, or paste images; gallery of uploaded images |

### Batch Extension Routes

| Method | Path | Page |
|---|---|---|
| `GET` | `/basic/:project/batch/:id` | Batch progress page |
| `GET` | `/api/:project/batch/:id/api` | Batch status JSON (2s polling) |
| `POST` | `/api/:project/ext/joleuger/batch/preview` | Expand prompt + seeds → variant list (JSON) |
| `POST` | `/api/:project/ext/joleuger/batch/generate` | Create batch, start first job (JSON) |

### Photobooth Extension Routes

| Method | Path | Page |
|---|---|---|
| `GET` | `/photobooth/` | Photobooth index — project selection grid |
| `GET` | `/photobooth/{project}` | Fullscreen camera page |
| `POST` | `/api/:project/ext/joleuger/photobooth/save` | Save captured image (JSON body with base64 data) |

### Job API

All job API routes return JSON.

| Method | Path | Response |
|---|---|---|
| `GET` | `/api/:project/jobs/active` | `[]JobRecord` — queued/generating jobs |
| `GET` | `/api/:project/jobs/:jobId` | `JobRecord` — single job status (`:jobId` is a per-submission UUID) |
| `POST` | `/api/:project/jobs/:jobId/cancel` | `{"status": "cancelled"}` |

#### JobRecord Structure

```json
{
  "id": "element-uuid",
  "element_id": "element-uuid",
  "project": "default",
  "sdcpp_job_id": "gen_abc123",
  "status": "generating",
  "error_msg": null,
  "sdcpp_started": "2026-07-15T10:00:00Z",
  "sdcpp_completed": null
}
```

#### Job Status Values

| Status | Meaning |
|---|---|
| `queued` | Waiting to start |
| `generating` | sdcpp is producing the image |
| `completed` | Image saved to S3 |
| `failed` | Error during generation |
| `cancelled` | Job was cancelled by user |

### Gallery Query Parameters

```
GET /basic/:project/gallery?page=1&per_page=24&sort=created_at&order=desc&favorites=true
```

| Parameter | Default | Valid Values |
|---|---|---|
| `page` | `1` | ≥ 1 |
| `per_page` | `24` | `24`, `50`, `200`, `all` |
| `sort` | `created_at` | `created_at`, `seed`, `model_name` |
| `order` | `desc` | `asc`, `desc` |

---

## Storage Layout

### S3 Object Structure

```
projects/
├── {project-name}/
│   ├── elements/
│   │   └── {element-uuid}.json   # Element JSON document
│   └── images/
│       └── {element-uuid}.png    # Generated image
```

**Projects start empty.** `CreateProject` writes only to SQLite. Projects are also discovered from element JSON during sync. No `meta.json` is written to S3.

### Element JSON Document

The single source of truth for each generation — the `kind` and `origin` fields determine the shape:

#### Generated Element (txt2img)

```json
{
  "id": "a1b2c3d4-e5f6-7890-abcdef1234567890",
  "project": "default",
  "kind": "image",
  "origin": "generated",
  "schema_version": 1,
  "version": 1,
  "created_at": "2026-07-14T10:30:00Z",
  "generation": {
    "task": "txt2img",
    "model": {
      "architecture": "sdxl",
      "variant": "refiner",
      "params": "1.2",
      "quantization": "FP16",
      "name": "sdxl-refiner-v1.2-fp16.safetensors"
    },
    "prompt": "a cat on a rooftop at sunset",
    "negative_prompt": "blurry, low quality",
    "width": 512,
    "height": 512,
    "seed": 42,
    "sample_steps": 20,
    "txt_cfg": 7.0
  },
  "image": {
    "project_location": "images/a1b2c3d4.png",
    "format": "png",
    "width": 512,
    "height": 512,
    "size_bytes": 245760
  }
}
```

#### Generated Element (img2img)

An img2img element carries `strength` and `reference_images` in its `generation` object:

```json
{
  "id": "b2c3d4e5-f6a7-8901-bcdef2345678901a",
  "project": "default",
  "kind": "image",
  "origin": "generated",
  "schema_version": 1,
  "version": 1,
  "created_at": "2026-07-14T10:35:00Z",
  "generation": {
    "task": "img2img",
    "model": {
      "architecture": "flux2",
      "variant": "klein",
      "params": "9B",
      "quantization": "Q4_K_M",
      "name": "flux2-klein-9b-Q4_K_M.gguf"
    },
    "prompt": "the two of them as cartoon characters",
    "negative_prompt": "blurry, low quality",
    "reference_images": [
      { "element_id": "f00d1234-..." },
      { "element_id": "a1b2c3d4-..." }
    ],
    "strength": 0.75,
    "width": 512,
    "height": 512,
    "seed": 99,
    "sample_steps": 20,
    "txt_cfg": 7.0
  },
  "image": {
    "project_location": "images/b2c3d4e5.png",
    "format": "png",
    "width": 1024,
    "height": 1024,
    "size_bytes": 312000
  }
}
```

#### External Element

An external image has no `generation` object:

```json
{
  "id": "f00d1234-...",
  "project": "default",
  "kind": "image",
  "origin": "external",
  "schema_version": 1,
  "version": 1,
  "created_at": "2026-07-14T10:00:00Z",
  "image": {
    "project_location": "images/f00d1234.png",
    "format": "png",
    "width": 1024,
    "height": 1024,
    "size_bytes": 1876000
  }
}
```

The single source of truth for each generation:

```json
{
  "id": "a1b2c3d4-e5f6-7890-abcdef1234567890",
  "project": "default",
  "kind": "image",
  "created_at": "2026-07-14T10:30:00Z",
  "model": { "stem": "v1-5-pruned-emaonly", "name": "v1-5.ckpt" },
  "prompt": "a cat on a rooftop at sunset",
  "negative_prompt": "blurry, low quality",
  "width": 512,
  "height": 512,
  "seed": 42,
  "sample_steps": 20,
  "txt_cfg": 7.0,
  "job": {
    "id": "gen_abc123",
    "status": "completed",
    "started_at": "2026-07-14T10:30:00Z",
    "completed_at": "2026-07-14T10:30:05Z",
    "duration_seconds": 5.2
  },
  "image": {
    "project_location": "images/a1b2c3d4.png",
    "format": "png",
    "width": 512,
    "height": 512,
    "size_bytes": 245760
  }
}
```

### SQLite Cache

SQLite is automatically created and populated on startup by scanning S3. The schema:

- **projects** — known project names
- **elements** — element records indexed by project and creation date
- **jobs** — job status records linked to elements
- **app_settings** — app-wide key-value settings

**You can safely delete `cache.db`.** It will be recreated on the next startup.

---

## Testing

### Unit Tests

All unit tests use in-memory SQLite and mock S3. No flags required:

```bash
go test ./...
go test -cover ./...    # with coverage
```

### Storage Integration Tests

Runs against real S3 storage (when credentials are available) and file storage (always):

```bash
SDCPP_INTEGRATION=1 go test -tags=integration ./internal/storage/
```

### E2E Tests

Requires a live sdcpp instance and the `e2e_with_sdcpp: true` flag in config:

```bash
# Enable in config.yaml:
# e2e:
#   e2e_with_sdcpp: true

SDCPP_E2E=1 go test -tags=e2e ./internal/server/
```

See [TESTING.md](TESTING.md) for detailed gap analysis and patterns for adding new tests.

---

## Troubleshooting

### "prompt is required" on submit

The `prompt` form field is required. Make sure the textarea is not empty.

### Image never appears in gallery

Check the sdcpp backend logs. Common causes:
- sdcpp is not reachable (verify `sdcpp.backends` in config)
- sdcpp generation failed (check job status in the Active Jobs card)
- S3 upload failed (verify storage credentials and bucket exists)

### S3 connection errors

- Verify `storage.endpoint` is reachable and uses the correct protocol (`http://` vs `https://`)
- Ensure `force_path_style: true` is set for Garage/minio
- Check that the access key has read/write permissions on the bucket
- Verify the bucket exists: `mc ls mygateway/sdcpp-outputs`

### "element not found" on image load

The element's image may not have been uploaded yet (job still running) or the job failed. Check the Active Jobs card for status.

### Accidentally deleted an image or project

Deletion is irreversible — the image, element JSON, and all records are permanently removed from S3 and SQLite. There is no recycle bin or undo. If you have another copy of the S3 data (e.g., a separate backup), you can restore it. Otherwise, the data is gone.

### Cache is stale after deleting S3 objects

Delete `cache.db` and restart. The next startup will resync from S3.

### Jobs show "queued" forever

The background poller checks sdcpp every 2 seconds. If sdcpp is unreachable, jobs may appear stuck. Verify sdcpp is running and the `sdcpp.backends` configuration is correct.

### Template render errors

Templates are embedded at compile time. If you modify template files, rebuild:

```bash
make build && ./app
```
