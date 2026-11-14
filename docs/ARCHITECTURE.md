# Architecture

Postern is one Go module that produces three programs:

| Program | Path | Purpose |
| --- | --- | --- |
| `postern` | `app/` | Native desktop client; forwards arguments to the CLI. |
| `postern-server` | `cmd/yourpm-server` | Self-hosted sync service. |
| `brandgen` | `cmd/brandgen` | Build-time white-label flag generator. |

The dependency direction is strictly one-way:

```
app ─┬─> ui ──> core/service ──> core/{store,httpx,sync,importer,exporter,...}
     └─> cli ─┘
cmd/yourpm-server ──> server ──> core/crypto
```

`core` never imports `ui`, `cli`, or `server`. Both interfaces (window and
shell) run the same application layer, so behaviour cannot drift between them.

## Packages

### `core/model`

Plain data types shared across boundaries: workspaces, entities, request and
response documents, history entries, sync metadata. Entity payloads are
JSON-encoded into `Entity.Data`, which keeps the SQLite schema small and lets
new fields ship without migrations.

An entity is one of:

- `collection` — variables, inherited auth, scripts.
- `folder` — inherited auth and scripts.
- `request` — method, URL, params, headers, body, auth, scripts, settings.
- `environment` — a named variable set.

### `core/store`

SQLite via `modernc.org/sqlite` (pure Go, no CGO). One file per install,
WAL mode, single writer connection.

Tables:

| Table | Contents |
| --- | --- |
| `workspaces` | Name, timestamps, sync server/remote ID, cursor. |
| `entities` | Workspace, kind, JSON blob, sealed flag, deleted flag, timestamps, revision, dirty flag. |
| `history` | Method, resolved URL, status, timing, size, timestamp. |
| `settings` | Key/value pairs: UI state, vault metadata, sync account. |

Notable behaviours:

- **Soft delete.** `DeleteEntity` marks the row and every descendant deleted
  inside one transaction. Tombstones sync; nothing is removed until the
  workspace itself is deleted. `ListTrash` and `RestoreEntity` expose deleted
  roots and bring a subtree back without recreating IDs.
- **Dirty tracking.** Every save bumps `rev`, sets `dirty = 1`, and records
  `updated_at`, which is what the sync client pushes.
- **Encryption at rest.** When the vault is enabled, the JSON blob is sealed
  and `sealed = 1`. Reads transparently open it with the in-memory key; a
  locked store refuses to return entities rather than leaking metadata. Secret
  settings (sync access and refresh tokens) are sealed with the same DEK and a
  settings-specific AAD; `PublicSettings` filters them out of UI payloads.
  Enabling the vault migrates any legacy plaintext secrets by sealing them.
- **Portable history.** History rows keep the resolved URL, not the templated
  one, so "retry" and audit views show what was actually called.

### `core/crypto`

Two independent uses:

1. **Vault (end-to-end).** A random 32-byte data encryption key (DEK) is
   wrapped by a key encryption key derived from the vault passphrase with
   Argon2id (t=2, m=64 MiB, p=4, 32-byte output). Records are sealed with
   XChaCha20-Poly1305. The additional authenticated data is
   `yourpm:entity:v1:<entity-id>` (and analogous strings for history and the
   wrapped key), binding ciphertext to its record and purpose.
2. **Server passwords.** Argon2id hashes for account passwords; SHA-256 for
   refresh-token lookup. Access tokens are HS256 JWTs.

Because the AAD is stable and contains no machine-specific data, a sealed
blob can be copied verbatim from the local database to the server and back
without re-encryption.

### `core/vars`

`{{name}}` interpolation with recursive resolution (depth-limited), `{{$...}}`
dynamic values, and a lookup function supplied by the service. Precedence is
request → environment → collection → workspace → global. Disabled variables
are skipped.

### `core/httpx`

The HTTP engine. It receives a fully loaded `RequestData`, resolves variables,
runs the pre-request script, builds the body, applies auth, executes with
timeouts and redirect policy, captures timings, parses cookies, and runs test
scripts against the response. It always returns a `Response`; transport
failures land in `Response.Error` so the UI can render them inline.

Auth types: none, inherit, bearer, basic, API key (header or query), and OAuth2.
The engine performs client-credentials and password token requests directly.
The authorization-code grant is handled by `core/service/oauth.go`: it opens the
system browser, listens on a loopback `127.0.0.1` port for the redirect, uses
PKCE (S256) and a random `state`, exchanges the code, and stores the result as a
sealed setting. Later sends read that token through a provider callback and
refresh it when the provider issued a refresh token. The engine never opens a
browser or blocks a request on user interaction.

### `core/script`

A goja runtime with a deliberately small `pm` surface: `pm.environment`,
`pm.collectionVariables`, `pm.variables`, `pm.globals`, `pm.request`,
`pm.response`, `pm.test`, `pm.expect`, and `console`. The scope objects are
views over the resolved variable map for that request. Scripts are CPU- and
time-bounded; there is no filesystem, network, or module access. Console
output and test results are returned to the caller.

### `core/service`

The application facade. It opens the store and engine, creates and saves
entities with defaults, resolves inheritance, prepares requests, sends them,
records history, manages the vault, imports and exports collections, and
exposes workspace bootstrap data. The UI and CLI are thin clients of this
package. It also owns the collection runner (`runner.go`), which walks a
collection or folder in tree order and sends each request through the normal
send path so variables, auth, and scripts apply, and workspace backup/restore
(`backup.go`), which emits plaintext JSON by default or a passphrase-encrypted
envelope, with entity IDs remapped on import.

Inheritance rules:

- A request with `auth.type == "inherit"` (or empty) takes the nearest
  collection's auth.
- Collection pre-request scripts run before request scripts; collection test
  scripts run before request test scripts.
- Folders store auth and script fields, and the nearest collection is found by
  walking parent IDs, but folder-level auth/script inheritance is not applied
  in v1 (only collection-level inheritance is enforced).

### `core/importer` / `core/exporter`

Postman v2.1 collections become entity trees. The importer normalises the two
ways Postman encodes URLs (raw string and structured host/path/query), removes
query parameters duplicated between them, converts body modes, and maps auth.
The exporter emits a schema-valid collection or environment, and a cURL
renderer produces copy-pasteable commands with secrets redacted where
appropriate.

### `core/sync`

The client half of the sync protocol: login/refresh, device listing and
revocation, workspace creation and linking, push, pull, conflict copies, and
cursor persistence. See [SYNC.md](SYNC.md).

### `ui`

A native Gio interface. Files are split by surface:

- `app.go` — application state, tab lifecycle, background work bridge, window
  size persistence, dirty-tab saving.
- `layout.go` — root layout, top bar, sidebar, tabs, status bar, lock screen,
  keyboard shortcuts, inactivity and auto-sync timers.
- `request.go`, `response.go`, `history.go`, `tree.go`, `kv.go`, `dialogs.go`.
- `brandmark.go` — optional embedded logo; falls back to the letter mark.
- `theme.go` — palette, typography, and reusable drawing primitives.
- `icons.go` — Material icons, resolved once at startup.

Request tabs include a Settings pane for per-request timeout, redirect limit,
proxy, TLS verification, redirects, and cookies. Response tabs expose copy and
save, decoding pre-base64 binary bodies. The workspace menu opens the trash,
workspace backup export/restore, and the collection runner.

Threading model: Gio owns the UI goroutine. Slow work (requests, import,
sync, Argon2id) runs in goroutines and posts a closure back through a mutex
queue drained at the start of each frame. State is therefore touched by one
goroutine at a time, with no locks around model fields.

The theme uses the Go font family, a charcoal surface (`#0B0E13`), and an
accent colour compiled in from the brand. Every custom widget draws with
`op/clip` and `paint` rather than embedding a browser.

### `server`

`net/http` with Go 1.22+ method-and-path routing. SQLite in WAL mode with one
writer connection. Responsibilities:

- Account registration/login, JWT access tokens, hashed refresh tokens,
  device records, revocation.
- Per-IP rate limiting on auth endpoints and a separate API-wide limit; client
  IPs come from `X-Forwarded-For` only when `YOURPM_TRUST_PROXY` is on.
- Workspace membership checks and role enforcement.
- Append-only sync operations with per-workspace sequence numbers, plus
  optional op-log pruning controlled by `YOURPM_OP_RETENTION_DAYS`.
- Opaque ciphertext storage; the server never decrypts workspace data.
- `/metrics` with an optional bearer token (`YOURPM_METRICS_TOKEN`).
- Structured `slog` JSON logs with method, path template, status, duration,
  and byte counts — never bodies, query strings, headers, or tokens.

### `cli`

Flag-based commands over `core/service`: `version`, `workspaces`, `envs`,
`ls`, `run`, `run-collection`, `authorize`, `backup`, `restore`, `sync`,
`devices`, `revoke-device`, `import`, `export`, `history`. `run` supports
ad-hoc URLs, saved requests, headers, body overrides, variable overrides,
timeouts, TLS verification control, output files, and CI-friendly exit codes
(`2` for HTTP errors with `--fail`, `3` for failed tests with `--fail-tests` or
a failed collection run). `authorize` performs the same browser/PKCE flow as
the desktop Auth tab for a saved request.

### `cmd/brandgen`

Reads a minimal YAML subset (`key: value`, comments, quoted values) and emits
`-X` linker flags or make fragments. An optional `logo:` path is embedded as
base64. Linker values are emitted with quote-safe delimiters so brand taglines
containing apostrophes or spaces survive both the shell and Go's `-ldflags`
parser. Keeping this out of the product avoids a YAML dependency in the client
and server binaries.

## Request lifecycle

```
user presses Send
  └─ ui collects tab editors into model.RequestData
      └─ service.Send(ws, req, env, collection, requestID)
          ├─ PrepareRequest: copy collection auth/scripts when inheriting
          ├─ VariableMap: workspace → collection → environment → overrides
          ├─ httpx.Send
          │    ├─ run pre-request script (may add variables)
          │    ├─ interpolate URL, params, headers, body, auth
          │    ├─ encode body (raw, form-data, urlencoded, binary)
          │    ├─ execute with timeout, redirect and TLS policy
          │    ├─ capture headers, body (size-capped), timing, cookies
          │    └─ run test script, collect console and assertions
          └─ record history (resolved URL, trimmed body)
      └─ UI drains queue on next frame and renders response
```

Response capture is capped at 32 MiB to bound memory; the response reports
`truncated` when this happens. History stores at most 1 MiB of a body (plus a
marker), so history does not grow with large downloads.

## Data directory

```
<config>/<brand-slug>/
  <brand-slug>.db   SQLite database (WAL and SHM files alongside)
```

The brand slug controls both the directory and the database filename, so
white-label builds do not collide with each other. When the default `yourpm`
slug is used and an older `rezapostmanalt` directory or database exists, the
service keeps using the legacy path so an upgrade does not silently start with
an empty workspace.

The server keeps `sync.db` and, when no secret is supplied, `jwt.key` in
`YOURPM_DATA`. File permissions are `0700` for directories and `0600` for the
key.

## Testing strategy

- Unit tests for crypto, variables, and the service layer, including secret
  setting sealing/migration, tombstone restore, backup round-trips with ID
  remapping, and collection-runner outcomes.
- Protocol tests that start the real server against a temporary directory and
  exercise registration, login, workspace creation, push, conflict, replay,
  pull, cursor advancement, rate limiting, metrics authentication, and
  retention pruning.
- An end-to-end sync test that opens two independent client databases, links
  both to one server, and verifies plaintext and encrypted round-trips,
  including vault creation from a workspace key.

There are no golden UI tests; the interface is verified by launching the
binary and inspecting logs and the resulting database.
