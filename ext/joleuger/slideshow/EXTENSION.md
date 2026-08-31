# Slideshow Extension

**Fullscreen slideshow of gallery elements.** A "▶ Slideshow" button in the
gallery toolbar plays the images selected by the gallery's *active filters*
one slide at a time on a black fullscreen page.

## Dependencies

None. Slideshow is a leaf extension. Its only dependency is the **core**
elements API (`GET /api/{project}/elements`) — core is not an extension and
therefore not expressible as an `extdep` edge.

## Configuration

```yaml
extensions:
  joleuger/slideshow:
    enabled: true          # default: true
```

No other config surface. Playback interval (4 s/slide) and the queue cap
(500 slides) are named constants in `slideshow.html` — deliberately not
project settings (v1 scope).

## How It Works

- **Entry point** — the "▶ Slideshow" button lives in the **core** gallery
  template (`internal/server/templates/gallery.html`), hard-coded behind
  `{{ if hasExtension "joleuger/slideshow" }}` — the favorites pattern.
  There is intentionally no generic UI-injection hook: the button's
  placement and style are part of the gallery layout, and UI slots are too
  specific for injected HTML fragments.
- **Filter inheritance** — the button navigates to
  `GET /basic/{project}/slideshow` with the gallery's current query params,
  minus `page`/`per_page`. The page fetches
  `GET /api/{project}/elements?per_page=500` *plus the inherited params*, so
  whatever the gallery can filter by (favorites, sort, order, origin, and
  future extension-registered filters via the query builder registry), the
  slideshow inherits without either side knowing about the other.
- **Exit** — Esc, the ✕ button, or (when the queue is empty) "Back to
  gallery" navigates back to `/basic/{project}/gallery` **with the same
  filter params**, so the user returns to the exact view they started from.
- **Player** — vanilla JS (no framework, per the project's frontend
  exclusions): one image at a time (`object-fit: contain`), auto-advance
  every 4 s with a progress bar, next slide preloaded via `new Image()`,
  caption with prompt / seed / model / date, counter. Controls:
  space or ⏯ = play/pause, ←/→ = step, click image = element detail page,
  Esc = exit. Loops at the end. Missing images are skipped; if every image
  is missing the player stops with an error state.
- **State** — none. No schema, no Migrate, no Sync, no S3 objects: the queue
  is computed live from the elements table through the core API.

## Route

| Route | Method | Authz | Response |
|---|---|---|---|
| `GET /basic/{project}/slideshow` | GET | `RequireAction(ActionView)` | Fullscreen HTML page |
