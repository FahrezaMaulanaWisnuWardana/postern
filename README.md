# Postern

A native, private API client for people who would rather own their tools.

Postern is a Postman-style workspace — collections, environments, scripts, history —
built as a single Go desktop binary with a real native UI (Gio), local-first SQLite
storage, and an optional self-hosted sync server that only ever sees ciphertext.
No Electron, no embedded browser, no account required, no telemetry.

```
┌──────────────────────────────────────────────────────────────┐
│  Postern                                  ● synced   ⚙       │
├───────────────┬──────────────────────────────────────────────┤
│  Personal  ▾  │  GET  {{baseUrl}}/v1/users          Send  ▸  │
│  ▸ Demo API   │  Params  Headers  Body  Auth  Scripts        │
│    ▸ Echo     │                                              │
│    ▸ Folder   │  200 OK · 42 ms · 1.2 KB                     │
│  History      │  { "users": [ … ] }                          │
└───────────────┴──────────────────────────────────────────────┘
```

## Why another API client?

- **Native, not a web page in a trench coat.** The window is drawn by Gio with
  platform text shaping and input. The shipped binary links no Chromium, no
  WebKit, and no Node runtime. It starts instantly and works on a plane.
- **Local-first.** Your workspace is a SQLite file on your disk. Everything
  works offline; sync is an optional convenience, not a dependency.
- **End-to-end encrypted sync.** When you do sync, the server stores opaque
  blobs. It cannot read request names, URLs, headers, tokens, or bodies.
- **Yours to host and brand.** One static server binary, one Docker image, one
  environment file. Rebrand the client and server with a five-line file.
- **Quiet.** No analytics, no crash reporting, no AI features, no phoning home.

## Features

- Collections, nested folders, and requests with drag-free reordering.
- Tabs with per-tab request/response state and unsaved-change markers.
- HTTP methods, query params, headers, raw / form-data / URL-encoded / binary
  bodies, and captured response cookies. Form-data file fields have a native
  file picker.
- Per-request transport settings: timeout, maximum redirects, proxy, TLS
  verification, redirect and cookie policy.
- Auth inheritance: none, bearer, basic, API key, and OAuth2 client
  credentials, password grant, or authorization code with PKCE (browser
  consent, loopback redirect, automatic token refresh).
- `{{variable}}` interpolation with environment, collection, and workspace
  scope plus Postman-style dynamic values (`{{$guid}}`, `{{$timestamp}}`, …).
- Pre-request and test scripts on a sandboxed JavaScript runtime (goja),
  with `pm.*` helpers, console capture, and test results in the response pane.
- Collection runner that executes a collection or folder in tree order and
  reports pass/fail per request.
- Response viewer with status, timing, size, headers, pretty JSON, raw body,
  copy, save to file, and binary download.
- Request history, searchable in the sidebar.
- Trash with per-item restore for deleted collections, folders, requests, and
  environments.
- Workspace backup and restore (plaintext JSON by default, or encrypted with a
  passphrase; see the security note below).
- Postman v2.1 collection import and export; environment export; cURL export.
- Optional vault: Argon2id + XChaCha20-Poly1305 encryption of all workspace
  data at rest, unlocked with a passphrase, with an inactivity auto-lock timer.
- Optional sync: multi-device encrypted sharing with per-record conflict
  detection, op-log pull cursors, automatic interval sync, and a device list
  that can revoke other sessions. **Settings → Team** adds email-addressed
  invitations, owner/admin/editor/viewer roles, role changes, invitation
  revocation and member removal. Encrypted team keys are separate from private
  vault keys; each teammate chooses their own workspace passphrase.
- Headless CLI that shares the exact same service layer as the GUI, including
  collection runs, OAuth 2.0 authorization, backups, sync, and device
  management.
- White-label builds: name, slug, version, tagline, accent colour, optional
  logo, default sync server, and website via linker flags.

## Install and build

Desktop installer builds support Windows x64 (`.exe` installer and portable
`.zip`), separate macOS Apple Silicon/Intel `.dmg` files, and Linux x64
(`.deb` and `.AppImage`). Packaging, signing credentials, release automation,
and the pre-publication checklist are described in [docs/RELEASING.md](docs/RELEASING.md).
These are build targets, not a claim that signed downloads have been published.

Requirements:

- Go 1.27.4 or newer (includes required standard-library security fixes).
- Desktop client: a graphics stack — Metal on macOS, X11 or Wayland on Linux,
  Direct3D on Windows. CGO is required for the window layer. On Debian/Ubuntu
  install the headers with
  `sudo apt install gcc libwayland-dev libx11-dev libx11-xcb-dev libxkbcommon-x11-dev`.
- Server: no CGO. `CGO_ENABLED=0` builds a static binary.

```sh
git clone <your-fork> postern
cd postern

make build          # bin/postern and bin/postern-server
make test           # unit and protocol tests
make run            # run the desktop app from source
make run-server     # run a local sync server on :8080
```

Or without Make:

```sh
go build -trimpath -ldflags "-s -w" -o bin/postern ./app
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/postern-server ./cmd/yourpm-server
```

### Windows

The client and server build for Windows amd64 and arm64. Cross-compile from
macOS or Linux with:

```sh
make windows                 # bin/postern.exe, bin/postern-cli.exe, bin/postern-server.exe
make windows WIN_ARCH=arm64  # same files with an -arm64 suffix
```

Windows decides between a console and a GUI program at link time, and a
single executable cannot be both. The build therefore produces a pair that
shares all code:

| File | Purpose |
| --- | --- |
| `postern.exe` | Native desktop app, linked as a GUI program so no console window appears. |
| `postern-cli.exe` | Console build for `run`, `import`, `export`, `history`, and the rest. |
| `postern-server.exe` | Self-hosted sync service. |

Use `postern-cli.exe` for scripts and terminals: the GUI binary has no attached
stdout. Data lives in `%AppData%\postern`; run the server with
`YOURPM_DATA` pointing at a writable directory.

The Windows binaries are compiler-verified here, not runtime-verified on a
Windows machine. Gio uses Direct3D for the window, and SQLite is pure Go, so
there are no DLLs to install.

The desktop app creates its database in the per-user config directory under
the brand slug (`~/Library/Application Support/postern` on macOS,
`%AppData%\postern` on Windows, `~/.config/postern` on Linux). Set
`YOURPM_DATA` to override it — useful for portable installs and tests.

Existing pre-Postern databases are reused in place when unambiguous. If several
legacy data directories exist, select one with `YOURPM_DATA` / `--data-dir`;
Postern does not guess or move your files. The `YOURPM_*` variables and internal
storage formats keep their historical names for compatibility.

## Desktop quick start

1. Launch the binary with no arguments.
2. Press **⌘N** / **Ctrl+N** for a new request, type a URL, press **⌘↵**.
3. Save it into a collection with **⌘S**.
4. Create an environment in the sidebar and reference values as `{{host}}`.
5. Enable the vault in **Settings → Vault** to encrypt the local database.
   Pick an auto-lock timeout there as well.
6. Point **Settings → Sync** at your own server to sync between devices.
7. Right-click a collection and choose **Run** to execute it, or use the
   workspace menu for **Trash** and workspace backups.
8. For OAuth 2.0, open **Auth → OAuth 2.0**, choose **Authorization code**, fill
   in the authorization and token URLs, then press **Authorize**. The system
   browser handles consent and the app stores and refreshes the token locally.

Backups are plaintext JSON unless you give a passphrase when exporting
(`postern backup --passphrase …`, or the passphrase field in the export
dialog). A passphrase-encrypted backup is sealed with Argon2id and
XChaCha20-Poly1305 and cannot be opened without that passphrase — there is no
recovery key. Plaintext backups are readable by anyone who can read the file,
even when the vault is on; store them somewhere you trust or delete them after
use.

## Command line

Passing any argument switches to headless mode; no window is opened.

```sh
postern version
postern workspaces
postern envs
postern ls
postern history --limit 20

# ad-hoc request
postern run GET https://api.example.com/health

# saved request, pretty JSON, extra header, variable override
postern run --request "List users" --env Staging --pretty \
  --header "X-Trace: abc" --var token=dev-token

# import / export
postern import ./collection.postman_collection.json
postern export "Demo API" --out demo.json

# fail CI on HTTP errors or failed pm.test() assertions
postern run --request "Smoke test" --fail
postern run --request "Smoke test" --fail-tests

# run every request in a collection or folder
postern run-collection "Demo API" --env Staging --fail-tests --json

# connect an authorization-code request (opens your browser)
postern authorize --request "List users" --env Staging

# workspace backup and restore (add --passphrase to encrypt)
postern backup --out demo.postern.json
postern backup --out demo.postern.enc.json --passphrase "…"
postern restore demo.postern.json --name "Restored API"
postern restore demo.postern.enc.json --passphrase "…" --name "Restored API"

# sync and device management
postern sync
postern devices
postern revoke-device dev_abc123
```

`run` writes the response body to stdout and a one-line summary to stderr, so
it composes with pipes:

```sh
postern run --request "List users" | jq '.data[].id'
```

Exit codes: `0` success; `2` for `run --fail` when the response is HTTP 400 or
above; `3` for `run --fail-tests` when a `pm.test()` assertion fails.
`run-collection --fail-tests` exits `3` when any request fails (transport error
or HTTP 400 and above) or any assertion fails. `run-collection --json` prints
the full summary for CI logs.

Environment variables:

| Variable | Used by | Meaning |
| --- | --- | --- |
| `YOURPM_DATA` | client, CLI | Data directory override. |
| `YOURPM_PASSPHRASE` | CLI | Vault passphrase when the vault is enabled. |
| `YOURPM_BACKUP_PASSPHRASE` | CLI | Encrypts `backup`, or opens an encrypted `restore`. |

## Team sync

1. Upgrade the desktop app and self-hosted server. Everyone signs in to the
   **same server with their own account**.
2. Enable/unlock **Settings → Vault**. In **Settings → Sync**, enter a new
   workspace passphrase (10+ characters), choose **New remote**, and link it.
   Confirm sharing if the local workspace already contains records.
3. Open **Settings → Team**, enter a teammate's sign-in email, select a role,
   and choose **Create invitation**. No email is sent.
4. For encrypted workspaces, use **Copy unlock code** and send that code
   privately. Do not share your vault or workspace passphrase.
5. The teammate opens **Settings → Team → Refresh**, enters the code, chooses
   their own workspace passphrase, and clicks **Accept**. The app creates a
   separate local workspace and runs its first sync.
6. Use **Sync now** or opt into automatic interval sync. Owners/admins can
   manage eligible roles, remove members, and revoke pending invitations.

Invitations expire after seven days. Viewers only pull; local experiments are
not uploaded. Shared environments and stored request credentials are visible
to members, while history stays local. Member removal stops future server
access, not offline access to downloaded data; there is no automatic key
rotation. Team sync is not live collaborative editing.

**Legacy encrypted remote?** Back up local data, unlink it, then create/link a
new encrypted remote with a new workspace passphrase. Old remotes may reuse
the private vault key and cannot invite teammates. Your local data is kept and
uploaded to the new remote. See [docs/SYNC.md](docs/SYNC.md).

## Sync server

The server is a single binary backed by SQLite. It has no external services,
no Redis, no object store, and no message queue.

```sh
YOURPM_DATA=/var/lib/postern \
YOURPM_JWT_SECRET=$(openssl rand -hex 32) \
YOURPM_REGISTRATION=closed \
./bin/postern-server
```

| Variable | Default | Meaning |
| --- | --- | --- |
| `YOURPM_ADDR` | `:8080` | Listen address. |
| `YOURPM_DATA` | `./data` | Directory for `sync.db` and the JWT key. |
| `YOURPM_JWT_SECRET` | generated | HS256 key. If unset, a random key is generated at `<data>/jwt.key`. |
| `YOURPM_REGISTRATION` | `open` | `open` allows sign-up; `closed` disables it. |
| `YOURPM_ADMIN_EMAIL` | — | Creates an initial account at startup. |
| `YOURPM_ADMIN_PASSWORD` | — | Password for that account. |
| `YOURPM_TRUST_PROXY` | off | Trust `X-Forwarded-For` for client IPs. Enable only behind a proxy you control. |
| `YOURPM_METRICS_TOKEN` | unset | Require this bearer token (or `?token=`) for `/metrics`. |
| `YOURPM_OP_RETENTION_DAYS` | `0` | Prune historical sync ops older than this. `0` keeps everything. |

Rate limits are applied per client IP: 20 auth attempts per minute and 600 API
requests per minute. Health endpoints are exempt. If `YOURPM_TRUST_PROXY` is
off the server ignores `X-Forwarded-For`, so put it behind a proxy that is
allowed to set that header and enable the flag deliberately.

`YOURPM_OP_RETENTION_DAYS` bounds the server's operation log. Pruning keeps
the newest operation for each document, but a device offline longer than the
retention window may miss intermediate revisions and needs to re-link. Leave it
at `0` unless you need to cap database growth.

Routes: `/healthz`, `/readyz`, `/metrics`, `/v1/auth/*`, `/v1/devices`,
`/v1/devices/{id}` (revoke), `/v1/workspaces`, `/v1/workspaces/{id}/push`,
`/v1/workspaces/{id}/pull`.
See [docs/SYNC.md](docs/SYNC.md) for the protocol and [docs/SECURITY.md](docs/SECURITY.md)
for the threat model.

### Docker Compose

```sh
cd deploy
cp .env.example .env      # set YOURPM_DOMAIN and secrets
docker compose up -d
```

This starts the server and a Caddy reverse proxy that obtains a TLS
certificate automatically. The database lives in a named volume; back up that
volume and you have backed up the service. See [deploy/README.md](deploy/README.md).

## White-label builds

Copy `brand/brand.yaml`, edit it, and build:

```yaml
name: AcmeAPI
slug: acme
version: 1.0.0
tagline: Acme's private API workspace
server: https://sync.acme.example
accent: "#5BC8FF"
website: https://acme.example
logo: brand/acme-logo.png
```

```sh
make build BRAND_FILE=brand/acme.yaml
```

This produces `bin/acme` and `bin/acme-server`. The window title, data
directory, database filename, user agent, accent colour, empty state, default
sync server, and logo all follow the brand. `logo` is optional and points at a
PNG that `brandgen` embeds as base64; a missing or malformed image falls back
to the letter mark. Linker flags are generated by `cmd/brandgen`, which reads a
deliberately tiny YAML subset and pulls in no YAML dependency. A macOS `.app`
bundle is available with `make bundle`.

## Project layout

```
core/            UI-free domain and application layer
  brand/         build-time identity
  crypto/        Argon2id, XChaCha20-Poly1305, password hashing
  model/         entities, requests, responses, workspaces
  store/         SQLite persistence, encryption at rest, tombstones
  httpx/         HTTP engine, auth, redirects, TLS, timing
  vars/          variable interpolation and dynamic values
  script/        sandboxed JavaScript runtime and pm.* API
  importer/      Postman v2.1 reader
  exporter/      Postman and cURL writers
  service/       facade shared by the GUI and the CLI
  sync/          E2EE sync client
server/          self-hosted sync service
ui/              native Gio interface
app/             desktop entrypoint (dispatches to CLI with arguments)
cli/             headless commands
cmd/             brandgen and server entrypoints
deploy/          Dockerfile, Compose, Caddy, environment template
docs/            architecture, sync protocol, security
brand/           example white-label files
```

## How it works

- The UI never touches SQLite or the network directly. It calls `service`,
  which owns validation, encryption, inheritance, and history.
- The HTTP engine resolves variables, applies inherited auth and scripts,
  executes the request, captures timings, and trims oversized bodies before
  they reach history.
- Entities are stored as JSON blobs. When the vault is enabled the blob is
  sealed with XChaCha20-Poly1305 and the record ID as authenticated data.
  Sync re-encrypts locally between the private vault key and an isolated
  shared workspace key; the server never receives either unwrapped key.
- Sync pushes dirty records with their last known server version. The server
  accepts only matching versions, records a monotonic sequence per workspace,
  and returns conflicting records instead of overwriting them; the client
  keeps both sides by creating a conflict copy.
- Deletes are tombstones, not row removals, so they propagate and cannot
  resurrect from another device.

More detail: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Performance, honestly

Measured on an Apple-silicon macOS machine with Go 1.27, stripped release
builds:

| Artifact | Size |
| --- | --- |
| Desktop client | ~25 MB |
| Sync server | ~10 MB |

Memory is the weak spot. An empty Gio window on this machine reports about
160 MB RSS before application code does anything; Postern idles around
160–190 MB depending on the session. The original target was 40–70 MB, which
Gio's GPU and font stacks do not meet. Making the app lighter would mean
replacing the toolkit, not trimming application allocations. This is
documented rather than hidden.

Request execution, import, export, search, and sync do not add meaningful
steady-state memory; history is capped and large bodies are truncated before
storage.

## Status and limits

Postern is a working v1, not a Postman feature-for-feature clone.

- No WebSocket, gRPC, or GraphQL-specific editing yet.
- OAuth2 authorization code uses the system browser and a loopback redirect.
  Custom URL schemes, device-code flow, and tokens imported from other clients
  are not supported. Tokens live in the local database, sealed when the vault is
  enabled.
- Team invitations appear in-app, not by email. Encrypted invitations require
  an unlock code shared privately and expire after seven days. Use the same
  upgraded self-hosted server for all teammates.
- Legacy encrypted remotes must be replaced with a new encrypted remote before
  inviting members, because older remotes reused the private vault key.
- Removing a member stops future server access; it does not erase downloaded
  copies or rotate the workspace key. Rotate exposed API credentials separately.
- Sync is whole-document, not field-level merge. Concurrent edits to the same
  request produce a conflict copy for manual resolution.
- Workspace backups are plaintext unless you supply a passphrase when exporting.
  Plaintext files are readable even when the vault is on. Treat them like
  secrets.
- Trash restore is subtree-based: restoring a collection or folder also
  revives any of its descendants that are in the trash. There is no separate
  deletion-batch history.
- No code signing, notarization, auto-update, or platform installer packages.
  `make bundle` produces an unsigned macOS `.app`, and the Windows binaries are
  unsigned as well. Expect Gatekeeper/SmartScreen prompts on other machines.
- The local database is encrypted when the vault is on, but the vault key is
  held in memory while unlocked. Lock it when you leave; the auto-lock timer
  helps.
- Server-side op-log pruning is optional and off by default. Devices offline
  longer than a configured retention window must re-link.

## Tests

```sh
go test ./...
go vet ./...
```

CI (`.github/workflows/ci.yml`) runs formatting, vet, and tests on Linux,
macOS, and Windows, smoke-tests the Windows CLI, and builds the server Docker
image on every push and pull request.

The suite covers crypto round-trips and vault rewrapping, settings-at-rest
sealing and migration, variable scoping, request execution and inheritance,
vault lock/unlock behaviour, soft-delete tombstones and restore, workspace
backup/restore round-trips (plaintext and passphrase-encrypted),
collection-runner pass/fail handling,
import/export round-trips, and the server's auth and sync protocol (including
conflicts, idempotent replays, metrics auth, rate limits, and retention).
Two-device encrypted sync is exercised end to end.

## License

MIT. See [LICENSE](LICENSE).
