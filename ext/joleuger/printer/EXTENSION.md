# Printer Extension

**Image printing via CUPS (`lp`).** A print button appears on the element detail
page (opening a modal with preview, printer selection, and copy count). The same
endpoints are used by the photobooth capture preview (its only consumer of the
print API besides the element page).

## Dependencies

| Dependency | Kind | Purpose |
|---|---|---|
| `joleuger/imageproc` | **Compile required** | Crop printers process the element image onto the print canvas via imageproc's `Processor` API before handing the file to `lp`. |

Because the dependency is CompileRequired (a Go import), **disabling imageproc
fails printer startup** — there is no degraded print mode without it. Raw
(non-crop) printers do not call imageproc at print time, but the extension
itself cannot start with imageproc off.

## Configuration

```yaml
extensions:
  joleuger/printer:
    enabled: true
    rotate: "auto"                      # optional: "auto" (default) | "never" — applied to crop printers
    default_printer: ""                 # optional, reserved
    printers:
      - name: "Office"
        uri: "cups://printserver.local/printers/office"            # raw: prints the element image as-is
      - name: "Dye-sub"
        uri: "cups://printserver.local/printers/dyesub"
        crop: true
        dimensions: "1800x1200"       # optional: print canvas, gm-style "WxH"
```

- **`printers`** — configured printers (any `cups://` URI). They are always
  listed, with `configured: true`, and sorted before discovered ones.
- **`crop`** (per printer) — when `true`, the element image is processed onto
  the printer's canvas via imageproc (center-crop, no distortion) before
  printing. Entries without `crop` print the element image as-is.
- **`dimensions`** (per printer) — the print canvas as gm-style `"WxH"` (e.g.
  `"1800x1200"`). When `crop: true` and `dimensions` is omitted, the default
  canvas **1800x1200** is used. Values are validated at startup (malformed →
  config error).
- **`rotate`** — applied to crop printers: `"auto"` (default) rotates portrait
  inputs 90° before the center crop; `"never"` never rotates. Raw printers are
  unaffected.
- There is no `base_url` setting: the image is always a **local file** when
  `lp` runs (fetched from storage first), so the CUPS server never needs to
  reach this UI.

## How Printing Works

- **Print flow** (`print` endpoint): validate body → `GetElement` (404 if
  missing) → project-strict check (400 if the element belongs to another
  project) → image presence check (400) → `storage.LocalFile` materializes the
  element image into a local/temp file → if the printer is configured with
  `crop: true`, imageproc processes it onto the canvas (500 on failure; the
  processed temp file is removed after the request) → `lp` submits the file.
  Printers **not present in the config** (e.g. a stale dialog entry) print the
  raw file — processing is a configured-printer feature, not a URI feature.
- **Printer list** = configured printers + local printers discovered via
  `lpstat -p` on the host running seedwright (URI
  `cups://localhost:631/printers/{name}`). An `lpstat` failure is non-fatal —
  only configured printers are shown. `GET …/printers?configured=true` skips
  the `lpstat` discovery entirely — that is what the print dialog and the
  photobooth settings select use, so only explicitly configured printers reach
  those UIs.
- **URI parsing** (`parsePrinterURI`): `cups://host:port/printers/name`. Hosts
  `localhost`, `127.0.0.1`, or empty are treated as the **local** CUPS server —
  `lp` gets no `-h` flag. Remote hosts default to port 631 when the port is
  omitted.
- **lp invocation**: copies use CUPS' `-n {copies}` flag (only when > 1) —
  there is no `-#` option in CUPS `lp`. The image file is always the last
  argument. Local: `lp -d {name} [-n {copies}] {file}`.
  Remote: `lp -h host:port -d {name} [-n {copies}] {file}`.
- **Job ID** is parsed from lp's output (`"lp: job id is N"`); on an
  unparseable success output a synthetic ID (`{printer}-1`) is returned.

## S3 Layout

None — the printer extension keeps no persistent state (`Sync` is a no-op).

## Hooks Used

| Hook | Method | Purpose |
|---|---|---|
| `ElementActions` | `renderPrintButton` | 🖨️ Print button + modal on the element detail page (raw preview, printer select, copies 1–99, **crop-preview toggle**, last-used printer per element in localStorage) |

**Modal behavior:** the dialog loads `GET …/printers?configured=true` (a single
flat list — no local-discovery group; with no configured printers it shows
"No printers configured — see `extensions.joleuger/printer.printers`"). The
*Preview crop* checkbox (on by default for crop printers, disabled for raw
ones) makes the preview show what the printer will actually print: it fetches
`POST …/ext/joleuger/imageproc/preview` with the selected printer's canvas
(`fit: "crop"`, `rotate: "auto"` — the dialog does not know the config-level
rotate) and displays the returned bytes as an object URL. Any preview failure
silently falls back to the raw image — a preview problem never blocks
printing. The object URL is revoked when the modal closes.

## Routes

All routes require `view` authorization (`authz.RequireAction(authz.ActionView, …)`).

| Method | Pattern | Purpose |
|---|---|---|
| `GET` | `/api/{project}/ext/joleuger/printer/printers` | `{"printers": [{name, uri, configured, status, crop, dimensions}]}` — configured first (with effective `crop`/`dimensions`), then local, each group sorted by name. `?configured=true` returns only configured printers (skips `lpstat`). |
| `POST` | `/api/{project}/ext/joleuger/printer/preview` | `{"element_id"}` → `{"image_url", "filename"}` (image URL is relative to the server root, prefix-aware; browsers resolve it against the page origin). Documented API — the dialog's live preview uses the imageproc endpoint instead. |
| `POST` | `/api/{project}/ext/joleuger/printer/print` | `{"element_id", "printer_uri", "copies"}` → `{"job_id", "status": "queued"}` |

## API

### Print

```
POST /api/{project}/ext/joleuger/printer/print
Content-Type: application/json

{"element_id": "elem-1", "printer_uri": "cups://localhost:631/printers/office", "copies": 2}

200 → {"job_id": "12345", "status": "queued"}
400 → {"error": "element_id is required"}
400 → {"error": "printer_uri is required"}
404 → {"error": "element not found: …"}
400 → {"error": "element does not belong to project …"}
400 → {"error": "element has no image"}
500 → {"error": "…"}   (storage fetch, image processing, or lp failure — incl. "invalid printer URI" for malformed URIs)
```

`copies` is clamped to 1–99 server-side.

## Minimum Base Version

Requires the extension contract + dependency graph (`ext.Extension`,
`internal/extdep`), the `storage.StorageBackend.LocalFile` materialization
method, and the `joleuger/imageproc` extension — i.e. the current codebase.

## Known Limitations

- Fire-and-forget: job status is not polled after submission (CUPS is the
  source of truth).
- Every print materializes the image to a local file (S3 storage needs a temp
  download) and crop printers add a second temp file for the processed image;
  both are removed when the request completes.
- `lpstat` must be on `PATH` (CUPS client tools installed) for local
  discovery; with `?configured=true` it is never called.
