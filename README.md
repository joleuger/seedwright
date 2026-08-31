# seedwright

A lightweight web UI for [stable-diffusion.cpp](https://github.com/leejet/stable-diffusion.cpp). Submit image generation jobs, browse results in a browsable gallery, manage your diffusion pipeline — all from a single Go binary.

**Think Lightroom for AI image generation.** Not a node graph. Not a raw engine. A curated, opinionated tool that lets you type a prompt, get an image, batch-seed variations, and find the right one — without ever touching the gearbox.

[📸 Screenshot: Home page with project selection grid]

## What It Does

| Feature | Description |
|---|---|
| **Generate** | Submit image generation jobs to your sdcpp instance |
| **Batch Generate** | Queue multiple seeds sequentially — generate a set of variations and come back to a finished batch |
| **Gallery** | Browse generated images per project with pagination, sorting, and favorites |
| **Element Detail** | View full metadata (prompt, seed, parameters) for each image |
| **Regenerate** | Recreate an image in-place with the exact same parameters — useful after accidental deletion |
| **Retry** | Retry failed or cancelled jobs with new random seeds |
| **External Images** | Upload photos, captured images, or imported assets alongside generated ones |
| **img2img (Denoise)** | Use external images as reference input for generation — adjust strength, pick references, generate |
| **Active Jobs** | Live job tracking with partial-DOM polling, cancel support, and "Cancel All" button |
| **Job Recovery** | Automatic stuck job cleanup on restart; retry from the UI |

Everything is stored in object storage — S3, a local folder, or ephemeral in-memory (10 MB, the first-run default). SQLite is a disposable, rebuildable cache.

## Quick Start

### First run — no config file needed

```bash
make build
./app
```

The app boots on first-run defaults (10 MB of ephemeral in-memory storage, sdcpp expected at `http://127.0.0.1:1234`) and a **Setup & Customize** wizard is waiting at [http://localhost:8080/onboarding](http://localhost:8080/onboarding) (also linked from the home page). It probes your storage and sdcpp and shows a live preview of the `config.yaml` it would write — on a first run the write is always allowed; overwriting an existing file is only possible if the running config allows it (`allow_config_write: true`) — otherwise you download the file and place it yourself. Either way it tells you to restart.

### Docker

```bash
docker build -t seedwright .
docker run -d -p 8080:8080 -v seedwright-data:/data seedwright
```

Same first-run flow; `config.yaml` lands in the `/data` volume. sdcpp runs outside the container (it needs the GPU); the wizard points the app at it.

### Classic setup

- Go 1.26+, a running [sdcpp](https://github.com/leejet/stable-diffusion.cpp) instance, and any storage: S3-compatible (tested with [Garage](https://garagehq.deuxfleurs.fr/)), a local folder, or memory.
- Copy `config.example.yaml` to `config.yaml`, adjust, then `make build && ./app`:

```yaml
server:
  listen: ":8080"

sdcpp:
  backends:
    - name: "default"
      base_url: "http://127.0.0.1:1234"

storage:
  type: "s3"
  endpoint: "http://localhost:3900"
  region: "garage"
  bucket: "sdcpp-outputs"
  access_key: ""
  secret_key: ""
  force_path_style: true

database:
  sqlite_path: "cache.db"
```

(`storage.type` can also be `"file"` with `file_path`, or `"memory"` with an optional `capacity`.)

See [USAGE.md](USAGE.md) for the full configuration reference, detailed UI guide, and troubleshooting.

## Project-Based Organization

Create multiple projects to organize your generations — each with its own gallery, settings, and backend selection.

[📸 Screenshot: Project dashboard with generate form, active jobs, and recent images grid]

Projects are not auto-created. Type a project name, click **Create Project**, and start generating.

## Extensions

Extensions add features without touching core code. Each is independently enabled or disabled in `config.yaml`.

| Extension | Purpose |
|---|---|
| **Onboarding** | First-run setup wizard + permanent Customize page (`/onboarding`) — profiles first, live config preview with gated Write / Download, probes storage/sdcpp, links to `CUSTOMIZE.md` (scenarios + agent prompts) |
| **Batch** | Multi-seed generation folded into the generate form — combination syntax (`{a,b,c}`), preview, sequential enqueue |
| **Favorites** | Star-toggle and `?favorites=true` gallery filter |
| **Photobooth** | Fullscreen camera UI for quick image capture — optimized for iPad/tablet |
| **Printer** | CUPS printing with per-printer crop canvases (via **Imageproc**) |
| **Imageproc** | Stateless image-processing engine (`gm`) — crop/fit canvas pipeline |
| **Slideshow** | Fullscreen slideshow of the gallery's current selection |

See [EXTENDING.md](EXTENDING.md) for the extension contract and how to build your own.

## Architecture

```
config.yaml  →  HTTP Server (Go net/http)  →  S3 (storage) + SQLite (cache)
```

- **Single Go binary** — self-contained, static build with `CGO_ENABLED=1`
- **REST calls to sdcpp** — thin Go client over HTTP, no Python/PyTorch dependency
- **S3-authoritative** — every element is a JSON document in S3; SQLite is a rebuildable projection
- **Inline templates** — all frontend is inline Go templates, no SPA framework, no external CSS/JS

## Data Model

Each generated image is an **Element** — a JSON document containing the complete generation context:

```json
{
  "id": "a1b2c3d4-...",
  "project": "default",
  "kind": "image",
  "origin": "generated",
  "schema_version": 1,
  "created_at": "2026-07-14T10:30:00Z",
  "generation": {
    "task": "txt2img",
    "model": { "architecture": "sdxl", "variant": "refiner" },
    "prompt": "a cat on a rooftop at sunset",
    "negative_prompt": "",
    "width": 512, "height": 512,
    "seed": 42, "sample_steps": 20, "txt_cfg": 7.0
  },
  "image": {
    "project_location": "images/a1b2c3d4.png",
    "format": "png",
    "width": 512, "height": 512,
    "size_bytes": 245760
  }
}
```

## API Reference

### Browser Routes

| Method | Path | Page |
|---|---|---|
| `GET` | `/` | Project selection |
| `GET` | `/basic/:project` | Dashboard (generate form, active jobs, recent elements) |
| `GET` | `/basic/:project/gallery` | Gallery grid (supports `?favorites=true`) |
| `GET` | `/basic/:project/element/:id` | Element detail |
| `GET` | `/basic/:project/batch/:id` | Batch progress |
| `GET` | `/basic/:project/settings` | Project settings |
| `GET` | `/basic/:project/external` | External images upload |
| `GET` | `/photobooth/` | Photobooth index |

### Job API (JSON)

| Method | Path | Response |
|---|---|---|
| `POST` | `/api/:project/generate` | Submit job |
| `GET` | `/api/:project/jobs/active` | Active jobs list |
| `GET` | `/api/:project/jobs/:jobId` | Job status (`:jobId` is a per-submission UUID) |
| `POST` | `/api/:project/jobs/:jobId/cancel` | Cancel job |

Full API reference: [USAGE.md#api-reference](USAGE.md#api-reference)

## Deployment

```
app          ← single Go binary
config.yaml  ← runtime configuration (optional — see first-run above)
cache.db     ← created automatically on first run (rebuildable)
```

No additional dependencies. No Python, no Node, no database server. The binary is self-contained. A multi-stage [Dockerfile](Dockerfile) ships at the repo root (Debian slim + GraphicsMagick for crop printing; `cups-client` is intentionally left out — CUPS printing targets a server outside the container).

## Testing

```bash
# All unit tests
go test ./...

# Storage integration tests (S3 + file backend)
SDCPP_INTEGRATION=1 go test -tags=integration ./internal/storage/

# E2E tests (requires live sdcpp)
SDCPP_E2E=1 go test -tags=e2e ./internal/server/
```

See [TESTING.md](TESTING.md) for the full test strategy.

## License

MIT

---

*seedwright is a lightweight, self-hosted web UI. It does not bundle or redistribute stable-diffusion.cpp — you run sdcpp separately and seedwright talks to it over HTTP.*
