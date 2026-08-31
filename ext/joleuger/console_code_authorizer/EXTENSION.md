# Console Code Authorizer — Control-Plane Extension

**Package:** `ext/joleuger/console_code_authorizer`
**Engine key:** `ext/joleuger/console_code_authorizer` (not an Enforcer — a ControlPlaneAuthenticator)
**Config key:** `auth.control_plane: ext/joleuger/console_code_authorizer`
**Classification:** Control-Plane AuthN — step-up credential verification

## Purpose

This extension implements the **control plane** half of seedwright's v4 authorization design: it is the mechanism by which a principal who is *reachable* via authentication gets *into* the data plane's own bookkeeping when the bookkeeping's own rules would otherwise make that impossible.

It uses the **OAuth Device Authorization Grant (RFC 8628)** pattern — the same shape behind `gh auth login`, Docker's device flow, Tailscale's device approval, and every "smart TV" sign-in flow that shows a code and a URL.

**Why this is AuthN, not AuthZ.** The extension's interface takes `*http.Request`, not a `Principal`. It answers one question: "does this request carry valid proof of identity?" — not "given we know who you are, what are you allowed to do?" That's step-up **authentication**, not authorization. The subsequent `ClaimGlobalOwnership()` call (which receives a Principal and mutates ownership records) is the authorization-flavored mutation that happens *after* authentication succeeds.

## How It Works

1. **Generate** — The extension creates an 8-character high-entropy alphanumeric code (logged to stdout) and starts a 10-minute expiry window.
2. **Surface** — The user visits `/console_code/{project}` from the "More" menu to see the code. A "Generate New Code" button and a 30-second auto-refresh keep the code current.
3. **Authenticate** — The user enters the code on the core `/claim-ownership` page. The extension's `Authenticate()` method checks it against the current code (constant-time comparison, single-use).
4. **Claim** — If `Authenticate()` returns true, core calls `ClaimGlobalOwnership()` on the active Enforcer (e.g., `StaticEnforcer`), which grants the principal Global Admin (Owner).

## Routes

| Route | Method | Purpose |
|---|---|---|
| `/console_code/` | GET | Redirect to first project |
| `/console_code/{project}` | GET | Console code display page |
| `/api/{project}/console_code` | GET | JSON: current code + expiry status |
| `/api/{project}/console_code/generate` | POST | JSON: force-new code |
| `/claim-ownership` | GET/POST | Core's generic claim-ownership page (posts `code` form field) |

All routes are marked `authz.Public()` — they bypass authorization, because their whole purpose is to *bootstrap* a principal who has no standing yet.

## Configuration

```yaml
auth:
  control_plane: ext/joleuger/console_code_authorizer

extensions:
  joleuger/console_code_authorizer:
    enabled: true   # default
```

No additional config fields are needed — code generation, expiry, and logging are all internal.

## Safety Recommendation

This extension's credential (console/SSH access to the machine) is meaningfully weaker than, say, being a provisioned Entra Global Administrator. Where Azure's upstream credential is already exclusive enough that standing, repeat availability is safe, this extension's own credential isn't, on its own.

**Recommended additional gate:** `ConsoleCode.Authenticator.Authenticate()` should check that the target scope (Global) currently has zero Admins before honoring a code match — a pure break-glass condition. This is the same invariant Microsoft enforces: a Global Administrator can never remove their own assignment specifically to prevent an organization from reaching zero admins.

Whether this gate is needed depends on how strong a *specific* deployment's upstream credential is. A future, stronger mechanism (checking a real IdP's own admin-group membership) could reasonably skip it. Keeping that judgment call at the extension level, not core's, is consistent with everything else this document set has pushed into extension-specific design.

## Implementation Notes

- **Code entropy:** 8 alphanumeric characters = ~47.6 bits of entropy (36^8 possible values). This is the same order of magnitude GitHub uses for device codes.
- **Single-use:** A successful match invalidates the code immediately. The user must visit the console page again to get a new one.
- **Expiry:** 10 minutes from generation. Expired codes are silently rejected.
- **Constant-time comparison:** Uses `crypto/subtle.ConstantTimeCompare` to prevent timing attacks.
- **Logging:** Each generated code is logged at WARN level with its value and expiry timestamp.

## Relationship to Data Plane

This extension does **not** implement an `Enforcer` — it does not decide what a principal can do *given* they have standing. It answers the structurally different question: *how does a principal get standing in the first place?*

When `Authenticate()` returns true, core's claim-ownership handler calls `ClaimGlobalOwnership()` on whichever `Enforcer` is active. For `StaticEnforcer`, this sets the `primary_owner` column on all projects via `ProjectOwnerUpdater`. The data plane then sees this principal as holding the Admin role.
