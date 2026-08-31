# Imageproc Extension

**Stateless image-processing engine.** imageproc owns one thing: the processing
engine (currently `gm`/GraphicsMagick, plus a passthrough `none`). It exposes the
engine to in-process consumers (the printer's crop pipeline) via the `Processor`
API and to the UI via two HTTP endpoints.

It has **no processing defaults of any kind** — no default dimensions, fit, or
rotation. Every parameter is supplied by the caller (e.g. the per-printer crop
configuration); zero or invalid values are rejected with an error, not defaulted.

## Dependencies

None — imageproc is a **leaf dependency**. It depends on nothing; other
extensions depend on it (the printer declares `joleuger/imageproc` as
**CompileRequired**, i.e. a Go import: with imageproc disabled, printer startup
fails).

## Configuration

```yaml
extensions:
  joleuger/imageproc:
    enabled: true
    engine: "gm"     # "gm" (default) | "none" (passthrough)
```

- **`engine`** — processing backend. `"gm"` (default) shells out to the `gm`
  binary; `"none"` passes the source file through unmodified (params are still
  validated). An unknown value is a config error at startup. An empty value
  defaults to `"gm"` — that is a *capability-availability* default, not a
  processing default.

## Processor API (in-process)

```go
type Processor interface {
    Name() string
    Available() bool
    Process(ctx context.Context, srcPath string, p Params) (string, error)
}

type Params struct {
    Width  int    // > 0
    Height int    // > 0
    Fit    string // "crop" | "fit"
    Rotate string // "auto" | "never"
}
```

- `Process` writes a temp output file (`sdcpp-imageproc-*.png`) and returns its
  path; the **caller** removes it. The source file is never modified.
- `Process` returns `(srcPath, nil)` — passthrough — **only** for engine
  `"none"` or when `gm` is missing from `PATH` (with a warning log). It never
  passes through bad params: `Params` are validated first, always.
- **gm behavior:**
  - `rotate: "auto"` rotates 90° (clockwise) iff the input is portrait
    (`gm identify -format "%w %h"`); `"never"` never rotates.
  - `fit: "crop"` — `-filter Lanczos [-rotate 90] -resize WxH^ -gravity center -extent WxH`
    (center-crop onto the canvas, ratio never distorted).
  - `fit: "fit"` — letterbox onto a white canvas
    (`-resize WxH -background white -gravity center -extent WxH`).
  - `gm convert` failure deletes the temp output and returns an error with the
    gm output attached.

## S3 Layout

None — imageproc keeps no persistent state (`Migrate` and `Sync` are nil).

## Hooks Used

None.

## Routes

All routes require `view` authorization (`authz.RequireAction(authz.ActionView, …)`) —
processing an image presupposes being able to see it.

| Method | Pattern | Purpose |
|---|---|---|
| `POST` | `/api/{project}/ext/joleuger/imageproc/preview` | `{"element_id", "width", "height", "fit", "rotate"}` → processed image bytes (`Content-Type: image/png`) |
| `GET` | `/api/{project}/ext/joleuger/imageproc/info` | `{"engine": "gm", "available": true}` — which engine is selected and whether it will actually process |

### Preview

```
POST /api/{project}/ext/joleuger/imageproc/preview
Content-Type: application/json

{"element_id": "elem-1", "width": 1800, "height": 1200, "fit": "crop", "rotate": "auto"}

200 → image/png (processed bytes)
400 → {"error": "…"}   (invalid params, element_id missing, wrong project, no image)
404 → {"error": "element not found: …"}
500 → {"error": "…"}   (storage fetch / processing / read failure)
```

All request fields are required and validated (`Params.validate`) — there are no
defaults. The element must belong to the URL's project (project-strict, like the
printer's print endpoint).

## Minimum Base Version

Requires the extension contract (`ext.Extension`) — i.e. the current codebase.

## Known Limitations

- `gm` must be on `PATH` of the host running seedwright for the `gm` engine;
  without it, processing degrades to passthrough with a warning (the info
  endpoint reports `"available": false`).
- Output is always PNG; source formats other than PNG (JPG, …) are converted.
- `gm` on this deployment reports empty `%[pixel:u.p{x,y}]` samples and no `txt:`
  support — consumers that need pixel data should use the 1×1 crop → PPM trick,
  not gm's pixel-pick pseudo-file (see LEARNINGS.md).
