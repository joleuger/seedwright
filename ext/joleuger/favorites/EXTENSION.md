# Favorites Extension

**Element bookmarking as an extension to seedwright.** Elements can be marked as favorites from the
gallery or element detail page, and a dedicated favorites gallery page lists them.

## S3 Layout

Each favorite is a per-element delta file that stores the authoritative state.
The delta file is the **source of truth**; the SQLite column is a cache rebuilt at startup.

```
projects/
├── {name}/
│   └── ext/
│       └── joleuger/
│           └── favorites/
│               └── elements/
│                   └── {element_id}.json    ← delta file
```

**Delta file schema:**
```json
{
  "id": "uuid-of-element",
  "version": 1,
  "is_favorite": true
}
```

- **id**: the element UUID (foreign key to the base element)
- **version**: delta version, incremented on each toggle
- **is_favorite**: boolean flag, `true` means favorited
- Files are created **lazily** on first set-to-`true`
- Default state (not favorite) = no file exists
- Toggling back to `false` deletes the delta file (back to default)

## SQLite Schema

Pattern B (extension columns on a base table). The core `elements` table gets one extra column:

```sql
ALTER TABLE elements ADD COLUMN ext_joleuger_favorites_is_favorite INTEGER DEFAULT 0;
```

On startup, `Sync` reads delta files from S3 and populates `ext_joleuger_favorites_is_favorite` on the elements table.
Orphaned delta files (elements deleted from the elements table) are removed from S3.

## Query Builder

The favorites extension registers with the `querybuilder.Builder` to contribute to `ListElements` queries:

| Contribution | Type | Details |
|---|---|---|
| Filter | `favorites` | `WHERE e.ext_joleuger_favorites_is_favorite = 1` — activated by `?favorites=true` in URL |
| Column | `e.ext_joleuger_favorites_is_favorite` | Additional SELECT column — scanned after base columns, populated into `elem.IsFavorite` |

Registration happens in `NewExtension()` via `ext.Register(a.QueryBuilder)`. The handler populates `opts.Filters["favorites"] = "1"` when the URL param is `true`.

## Hooks Used

| Hook | Method | Purpose |
|---|---|---|
| `NavBarItems` | `NavBar` | Inject "Favorites" nav link in project navigation bar |
| `ElementActions` | `ElementActions` | Inject ⭐ Favorite toggle button on element detail page |

## Routes

| Method | Pattern | Handler |
|---|---|---|
| `POST` | `/{project}/favorites/toggle` | Toggle favorite status (JSON API) |
| `GET` | `/{project}/favorites` | Render favorites gallery page |
| `GET` | `/{project}/favorites/api` | List favorite element IDs (JSON) |

## API

### Toggle Favorite

```
POST /{project}/favorites/toggle
Content-Type: application/json

{"element_id": "<element-id>"}

Response: {"favorite": true, "icon": "⭐"}
         {"favorite": false, "icon": "☆"}
```

### List Favorites (JSON)

```
GET /{project}/favorites/api

Response: {"element_ids": ["uuid-1", "uuid-2", ...]}
```

## Minimum Base Version

None — works with current codebase.

---

## User Cases

### UC-F01 — Toggle Favorite

**Goal:** Mark an element as favorite and verify persistence.

| Step | Action | Expected Result |
|---|---|---|
| 1 | Generate an image (UC-02) | Element `{id}` exists |
| 2 | Navigate to `GET /test-01/element/{id}` | Element detail page shows toggle button (☆ unfavourited) |
| 3 | Click toggle button | `POST /test-01/favorites/toggle` (JSON: `{"element_id": "{id}"}`) |
| 4 | Verify response | 200 OK JSON: `{"favorite": true, "icon": "⭐"}` |
| 5 | Verify S3 | `projects/test-01/ext/joleuger/favorites/elements/{id}.json` created with `is_favorite: true` |
| 6 | Toggle again (unfavorite) | `POST /test-01/favorites/toggle` (same payload) |
| 7 | Verify response | 200 OK JSON: `{"favorite": false, "icon": "☆"}` |
| 8 | Verify S3 | Delta file deleted (back to default state) |

**Element actions bar:** The toggle button is also available in the element actions bar (inline with other action buttons) for quick toggling from the gallery view.

### UC-F02 — Browse Favorites Gallery

**Goal:** Filter gallery to show only favorited elements.

| Step | Action | Expected Result |
|---|---|---|
| 1 | Generate 5 images (UC-02) | 5 elements in gallery |
| 2 | Favorite 2 of them (UC-F01) | — |
| 3 | Click "Favorites" link in navigation bar | Redirects to `GET /test-01/gallery?favorites=true` |
| 4 | Verify gallery content | 200 OK; only 2 favorited elements shown |
| 5 | Remove favorite from one element | — |
| 6 | Refresh gallery | 200 OK; only 1 favorited element shown |

### UC-F03 — Favorites Count in Nav

**Goal:** Navigation bar shows the count of favorited elements.

| Step | Action | Expected Result |
|---|---|---|
| 1 | Generate 5 images; favorite 3 (UC-F01) | — |
| 2 | `GET /test-01` (any page) | Nav bar shows "Favorites (3)" link |
| 3 | Favorite one more | Nav bar shows "Favorites (4)" |

**Nav bar integration:** The "Favorites" link jumps to `/{project}/gallery?favorites=true` via `#favoritesJump` anchor, enabling quick access from any page.

## Known Limitations

- The favorites count in the nav bar is computed per-request (indexed SQLite query).
- No keyboard shortcut or drag-and-drop support.
- S3 cleanup on toggle-to-false uses a direct `DeleteObject` — if S3 fails, the delta file remains
  but SQLite is updated, creating a transient inconsistency (resolved on next `Sync`).
