# Photobooth Extension

**Fullscreen camera UI for quick image capture, with optional print-in-preview (photobooth 2.0).**
Captures a frame from the device camera, saves it as a project element, and — when the
printer extension is available — offers a copy-count selector and a print button directly
in the capture preview.

## Dependencies

| Dependency | Kind | Purpose |
|---|---|---|
| `joleuger/printer` | **Runtime optional** | The capture preview calls the printer's HTTP API (`GET …/ext/joleuger/printer/printers`, `POST …/ext/joleuger/printer/print`) from JavaScript to print the captured photo. No Go-level import. |

Degrade behavior when the dependency is disabled: the capture preview falls back to the
classic **Retake / Keep** buttons; the *Print in capture preview* project setting remains
editable but has no effect until the printer extension is enabled; the *Printer* select on
the settings page shows a "Printer extension is not enabled" hint (the saved value is
kept as-is, never wiped).

The dependency is declared in `dependencies.go` (`Dependencies()`) and checked at runtime
via `a.ExtDeps.IsEnabled("joleuger/printer")` (nil-safe).

## S3 Layout

Captured images are stored as regular elements:

```
projects/{project}/elements/photobooth_{unixnano}.png
projects/{project}/elements/photobooth_{unixnano}.json
```

Project settings use the delta-file pattern (EXTENDING.md, Part 1.5):

```
projects/{project}/ext/joleuger/photobooth/settings.json
```

**Delta file schema:**
```json
{
  "id": "project-name",
  "version": 3,
  "post_filter_prompt": "Please cartoonify",
  "post_filter_reference_image": "element-id",
  "capture_trigger_binding": "KeyA",
  "print_enabled": true,
  "print_printer": "cups://localhost:631/printers/office",
  "keep_on_cancel": true,
  "max_photos": 5
}
```

- **id**: the project name
- **version**: delta version, incremented on each write
- **post_filter_prompt** / **post_filter_reference_image**: optional txt2img pass after capture
- **capture_trigger_binding**: `KeyboardEvent.code` bound to the shutter (e.g. a Bluetooth remote key)
- **print_enabled**: print workflow in the capture preview (default `true` when absent)
- **print_printer**: CUPS URI of the printer the capture preview prints to
  (default: none — the print button is disabled with a hint). The person in
  front of the photobooth is never asked which printer to use; the operator
  chooses it once on the project settings page, where the select is
  populated from the printer extension's printers API.
- **keep_on_cancel**: save the photo when the preview is closed with ✕ (default `true` when absent)
- **max_photos**: upper bound of the copy-count buttons, 1–10 (default `5` when absent, clamped)

Files are created lazily on first set; default state = no file.

**Validation (settings saver):** the `saveProjectSettings` saver owns the write. It
rejects unknown field names, requires strict types (strings for the text fields,
booleans for the toggles), and clamps `max_photos` to 1–10 (accepts JSON numbers,
ints, and numeric strings from the settings page inputs). Validation failures return
`*server.ValidationError` → the endpoint answers 400 with the message, and nothing
is written.

## SQLite Schema

Pattern B (extension columns on the `projects` table):

```sql
ALTER TABLE projects ADD COLUMN ext_joleuger_photobooth_post_filter_prompt TEXT;
ALTER TABLE projects ADD COLUMN ext_joleuger_photobooth_post_filter_reference_image TEXT;
ALTER TABLE projects ADD COLUMN ext_joleuger_photobooth_trigger_binding TEXT;
```

Only the post-filter and trigger-binding settings are mirrored to SQLite (they are read on
the hot save path). The print settings are S3-delta-only — the page handler reads them via
`ProjectRepository.GetExtensionSettings`. On startup, `Sync` populates the columns from the
delta file (the file is not rewritten, so delta-only fields survive).

## Capture Overlay (photobooth 2.0)

When `print_enabled` is on **and** the printer extension is enabled at runtime, the
capture overlay shows:

- **Copy-count buttons** (1 .. `max_photos`) on the left — each button draws N overlapping
  photo icons, so the count is readable without reading digits. Port of the project
  owner's `NumberOfPhotosButton` from `old-photo-app.html` (MIT), vanilla JS.
- **Retake** — discards the shot (no element).
- **Print** (🖨 icon) — saves the element, then submits
  `POST /api/{project}/ext/joleuger/printer/print {element_id, printer_uri, copies}`
  using the **configured printer** (`print_printer` project setting).
  Save failure keeps the preview open for retry; print failure after a successful save
  keeps the element in the gallery (retry from the element page).
- **✕ (Done)** — closes the preview; saves the photo iff `keep_on_cancel`.

There is **no printer selector in the overlay** — the printer is configured once in the
project settings. The print button is enabled only when a printer is configured *and*
its URI is in the printer extension's current list; otherwise it is disabled with a
hint ("No printer configured — see project settings" / "Printer not available right
now").

Otherwise (printing not allowed) the overlay shows the classic **Retake / Keep** buttons.

## Hooks Used

| Hook | Method | Purpose |
|---|---|---|
| `MoreNavItems` | `MoreNavItems` | 📷 Photobooth link in the "More" dropdown |
| `SettingsSection` | `SettingsSection` | *Photobooth* section on the project settings page (post-filter, trigger key, print settings) |
| `SettingsSavers` | `saveProjectSettings` | Write path for the section — validates the submitted fields and read-modify-writes the extension's own `photoboothSettings` delta file, mirroring hot-path columns into `projects` |

## Routes

| Method | Pattern | Auth | Handler |
|---|---|---|---|
| `GET` | `/photobooth/` | Public | Project selection index |
| `GET` | `/photobooth/{project}` | Public | Fullscreen camera page |
| `GET` | `/photobooth/{project}/` | Public | Trailing-slash redirect |
| `POST` | `/api/{project}/ext/joleuger/photobooth/save` | Public | Save captured base64 image → element (JSON `{"element_id"}` on `Accept: application/json`, redirect otherwise) |

## Post-Filter Pass

When `post_filter_prompt` is set, saving a capture also starts a txt2img job that uses the
captured photo (plus the optional `post_filter_reference_image`) as reference images.
The job creates a second element that references the capture.

## Licensing of Reused Code

- **Copy-count buttons + layout**: ported from `old-photo-app.html` (project owner's own
  code, MIT). The port is in `photobooth.html` (`.pb-copy-btn*` CSS, `buildCopyCountButtons`
  and friends in the page script).
- **Icons** (`materialio_photo`, `materialio_print`, `materialio_cancel`): imported from
  [material.io](https://fonts.google.com/icons), licensed under the
  [Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0.html). They are embedded
  in the hidden SVG defs in `photobooth.html`.

## Minimum Base Version

Requires the extension contract + dependency graph (`ext.Extension`, `internal/extdep`)
and the `ProjectSettingsDelta` JSON round-trip — i.e. the current codebase.

## Known Limitations

- Print submission is fire-and-forget: no job polling in the overlay (the CUPS job queue is
  the source of truth; the element page offers the same print path).
- The copy-count buttons are fixed at 36 px icons; the layout is tuned for tablet use.
- `max_photos` is capped at 10 (settings input `max=10`, clamped in
  `overlaySettingsFromDelta`).
