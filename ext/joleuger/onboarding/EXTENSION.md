# Onboarding Extension

**First-run setup wizard + permanent "Setup & Customize" page.** It asks, in
order: *what do you actually want?* (profiles), where your images live,
where sdcpp runs, and what to call it — then shows a **live preview of the
config it would produce**, from which the user either **writes** it back to
the path the app was started with (gated, see *Safe write gate*) or
**downloads** it as a file.

This is the extension that makes "download the binary, double-click, get an
explanation what to do" possible: with **no config file at all**, the app
boots on first-run defaults (memory storage, `http://127.0.0.1:1234`) and
this extension is on by default, so a fresh start always finds a working
setup page at `/onboarding`.

## Dependencies

None. Leaf extension.

## Configuration

```yaml
application:
  onboarding: joleuger/onboarding   # default (also: empty). "none" turns the wizard off;
                                    # any other extension key = off for this one.
extensions:
  joleuger/onboarding:
    enabled: true          # default
    allow_config_write: false   # default — let the wizard OVERWRITE an existing config.yaml
```

`allow_config_write` guards **overwrites only** — a missing config file is
always writable (a first run has nothing to overwrite). It is read from the
*running* config, so changing it needs a restart to take effect.

Selection logic: `enabled` must be true **and** `application.onboarding`
must select this key — empty means "default" (this extension), `"none"`
disables all onboarding, any other key selects a different onboarding
provider (this one then stays off).

## Routes

| Method | Pattern | Auth | Purpose |
|---|---|---|---|
| `GET` | `/onboarding` | Public | The Customize page (status card, **"What do you actually want?"** profiles grid + a link box to the repo's `CUSTOMIZE.md`, 3-step wizard, **config preview** with Write/Download) |
| `POST` | `/api/onboarding/verify` | Public | Probe `target: "storage"` (builds a throwaway backend from the submitted fields, read-only `ListObjects`) or `target: "backend"` (GET `{url}/sdcpp/v1/capabilities`, reports the model name) |
| `POST` | `/api/onboarding/complete` | `manage_permissions` | Validates the wizard payload (incl. optional `profile_key`), passes the safe-write gate, writes `config.yaml`, best-effort creates the first project → `{"ok", "config_path", "restart_required": true, "message"}` |
| `POST` | `/api/onboarding/preview` | Public | Renders the exact config the wizard would produce (**secrets masked** — `access_key`/`secret_key` become `••••••••`) without writing anything → `{ok, yaml, config_exists, write_allowed, confirm_required, write_blocked_reason, ephemeral_warning}` |
| `POST` | `/api/onboarding/download` | `manage_permissions` | Same render, **unmasked**, as a `text/yaml` attachment (`config.yaml`) — the escape hatch when the write gate is closed or the storage is ephemeral |
| `POST` | `/api/onboarding/profile` | `manage_permissions` | Applies a profile (title + extension enabled flags) to the config file → same restart-hint response (gated like `complete`) |

Page, verify, and preview are **public** on purpose: a first-time user has
nothing configured yet, and the preview reveals no secrets. Every write
(`complete`, `profile`, `download`) requires `manage_permissions` (root by
default) **and** passes the safe-write gate below.

## Safe write gate

Writing `config.yaml` is a privileged act — it can break a working setup.
The gate is two-fold for **existing** files:

1. The *running* config must set
   `extensions.joleuger/onboarding.allow_config_write: true` (default
   `false`). Off → writes are refused with an explanation, and the page
   degrades to preview + download.
2. The request must carry `confirm_overwrite: true` (the page shows an
   explicit checkbox before enabling the Write button).

A **missing** config file is always writable — that is the first-run path,
and there is no running config yet that could hold the flag. `preview` is
the only way to see the result when the gate is closed.

**Ephemeral storage warning:** on Linux the page checks the filesystem type
of the config path via `statfs` — `tmpfs` and `overlayfs` (the usual
container root FS) get a warning that a written config disappears on
reboot/container recreation, with "use Download" as the alternative. On
Windows and macOS this check is a documented no-op.

## Config file semantics

- **In place (file exists):** YAML node surgery — only the `storage`,
  `sdcpp.backends[0]`, and `application` sections the wizard knows about are
  touched; `auth`, `extensions`, extra backends, and comments-adjacent
  structure survive. Written via temp file + rename with `0600` (the file
  may contain S3 secrets).
- **Fresh (no file):** a minimal readable document with `server.listen`,
  one `sdcpp.backends` entry, the chosen `storage`, `database.sqlite_path`,
  `application` (title + default_project), and — when the payload carries a
  `profile_key` — an `extensions` section with that profile's toggles (an
  empty title defaults to the profile's title).
- **Profiles** (`applyProfile`) touch `application.title` and
  `extensions.<key>.enabled` **only for keys the profile lists**; unlisted
  keys keep their state. Storage is never a profile's business.
- Nothing is applied without a restart — the response always says so.

## Profiles

| Key | Title | Effect |
|---|---|---|
| `try-it` | Seedwright | All bundled extensions on |
| `photobooth` | Photobooth | photobooth/printer/imageproc/favorites/batch on; console_code_authorizer, slideshow, authz-simple off |
| `family-archive` | Family Photos | favorites on; photobooth/printer/imageproc/batch/slideshow off |
| `minimal` | Image Box | only onboarding on |

## Customization catalog (lives in CUSTOMIZE.md)

The wizard's "What do you actually want?" section carries a link box to the
repo-root [`CUSTOMIZE.md`](../../../CUSTOMIZE.md) — the single source of
truth for everything beyond the profiles: the scenario catalog (photobooth,
family photos, different model, story book, Telegram chatbot; tiers
config / agent / agent + strip), copy-paste agent prompts built from one
referential template (it points the agent at **AGENTS.md + EXTENDING.md**,
the repo's own contract, instead of re-explaining the stack), and fork
instructions. The in-page catalog was removed deliberately — it duplicated
the profile cards; a doc page is cheaper to maintain and to grow.

## Hooks Used

| Hook | Purpose |
|---|---|
| `WelcomeExtras` | "✨ Setup & Customize →" banner on the welcome (project selection) page — the restart entry point after a config change |

`WelcomeExtras` is a core hook (`internal/server`): a list of
`func(ctx) (template.HTML, error)` with no project context, rendered after
the project grid.

## S3 Layout

None — the extension keeps no persistent state. Its only owned file is
`config.yaml` itself.

## Known Limitations

- No self-restart: the response tells the user to restart (a restart
  endpoint would need its own trust story).
- `verify` for S3 performs a real network probe with the submitted
  credentials; a 15s request timeout bounds the wait.
- `complete`'s project creation is best-effort — a storage hiccup logs a
  warning and the config write still succeeds; the user can create the
  project from the welcome page after restart.
- **No delta view yet:** the preview renders the full resulting config, not
  a diff against the current one. A delta view is a deliberate future task
  (node-level diffing of the two YAML documents) — see git history for the
  decision.
- The ephemeral-storage warning is Linux-only (`statfs` magic numbers for
  `tmpfs`/`overlayfs`); on Windows/macOS it is a no-op by design.
- Windows cgo-free build is not covered here (a `Dockerfile` ships at the
  repo root; a Windows cross-build is a distribution task, not an
  extension one).
