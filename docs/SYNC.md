# Sync protocol

Postern sync is a small, deliberately boring protocol: clients push encrypted
records with the version they last saw, pull everything newer than a cursor,
and resolve conflicts themselves. The server is a sequencer and an opaque
blob store, not an editor.

## Vocabulary

- **Workspace** — a local database namespace. Only linked workspaces sync.
- **Document (`docId`)** — one entity: collection, folder, request, or
  environment.
- **Version** — per-document counter. Starts at 0 on the server, increments by
  one for every accepted write. The client stores it as the entity revision.
- **Sequence (`seq`)** — per-workspace, monotonically increasing cursor over
  accepted operations. Every accepted write gets the next value.
- **Operation (`opId`)** — client-generated unique ID used for idempotent
  retries.
- **Tombstone** — a deleted document. It still carries a version and stays in
  the operation log so the deletion reaches every device.

## Authentication

```
POST /v1/auth/register   { email, password, device } -> { token, refreshToken, deviceId, user }
POST /v1/auth/login      { email, password, device } -> { token, refreshToken, deviceId, user }
POST /v1/auth/refresh    { refreshToken }            -> { token, refreshToken }
POST /v1/auth/logout     Authorization: Bearer …      -> 204
GET  /v1/me              Authorization: Bearer …      -> { user }
GET  /v1/devices         Authorization: Bearer …      -> [ … ]
DELETE /v1/devices/{id}  Authorization: Bearer …      -> 204
```

- Access tokens are HS256 JWTs valid for one hour, carrying `uid` and the
  device ID `did`.
- Refresh tokens are random, stored only as SHA-256 hashes, and rotated on
  refresh. The client keeps the current refresh token in its settings and
  retries a rejected request once after refreshing.
- Every authenticated request re-checks the device row, so revoking a device
  invalidates its access tokens immediately rather than waiting for expiry.
- Registration can be closed with `YOURPM_REGISTRATION=closed`; the first
  account can be seeded with `YOURPM_ADMIN_EMAIL` / `YOURPM_ADMIN_PASSWORD`.
- Auth endpoints are rate limited per client IP (20 attempts per minute); all
  other API routes share a 600-request-per-minute per-IP limit.
- `GET /v1/devices` lists devices; `DELETE /v1/devices/{id}` revokes another
  device's refresh token and immediately invalidates its access tokens. The
  desktop app exposes this in **Settings → Sync**, and the CLI as `devices` /
  `revoke-device`.

## Endpoints

```
GET    /v1/workspaces                 list memberships
POST   /v1/workspaces                 create { name, encrypted, keyScope, kdfSalt, wrappedKey }
GET    /v1/workspaces/{id}            workspace metadata
DELETE /v1/workspaces/{id}            owner only
POST   /v1/workspaces/{id}/push       { deviceId, ops: [...] }
GET    /v1/workspaces/{id}/pull?since=…&limit=…
GET    /v1/workspaces/{id}/team
POST   /v1/workspaces/{id}/invitations
DELETE /v1/workspaces/{id}/invitations/{invite}
PATCH  /v1/workspaces/{id}/members/{member}   { role }
DELETE /v1/workspaces/{id}/members/{member}
GET    /v1/invitations
POST   /v1/invitations/{invite}/accept       { kdfSalt, wrappedKey }
GET    /healthz /readyz /metrics
```

Roles are `owner`, `admin`, `editor`, `viewer`; push requires owner, admin, or
editor, pull requires any role. Only owners can grant or change admin roles.
Admins can invite and manage editors/viewers, but cannot change owners or other
admins. The owner cannot be removed or demoted. Non-owners may leave via DELETE
on their own membership. Role changes are checked against the server on sync;
viewers skip push even when they have unsynced local experiments.

## Team invitations

Use **Settings → Team** after signing in to the same server. Invitations target
the teammate's account email and expire in seven days; registration may happen
after the invitation. No email is sent. Only the addressed authenticated account
can list the key envelope or accept the invitation. Acceptance is transactional,
single-use, and rechecks the inviter's current permissions. Owners/admins can
revoke pending invitations. Reissuing replaces the old invitation ID and code.
Removing an inviter invalidates their pending invitations.

For encrypted workspaces, the inviter generates a random 256-bit unlock code
locally and wraps only the shared workspace key with Argon2id and
XChaCha20-Poly1305. Share that code privately, outside the sync server.
The recipient enters it and chooses a personal workspace passphrase; their
client unwraps the key and sends a fresh salt and wrapped key for their own
membership. The code and both passphrases never reach the server. The desktop
app creates a new local workspace on acceptance so private data is not merged.
Other devices of that member can link using the personal workspace passphrase,
without the original invitation code or the owner's passphrase.

Removal blocks future metadata/push/pull access, not previously downloaded data.
There is no automatic cryptographic key rotation; rotate exposed API credentials
separately. Shared environments and stored request credentials are visible to
members. History and local-only settings are not synced.

## Linking a workspace

The client stores a sync account in settings (`sync.server`, `sync.email`,
`sync.deviceId`) plus the access and refresh tokens. When the vault is enabled
the tokens are sealed with the DEK (`crypto.SettingAAD`) rather than written as
plaintext; leaving the vault off means they sit in the settings table protected
only by file permissions. A workspace row carries `sync_remote_id`,
`sync_encrypted`, and `sync_cursor`.

Automatic sync is opt-in. When enabled (`sync.auto=1`, `sync.interval` in
minutes) the desktop app syncs a linked workspace on startup and then on the
interval while the vault is unlocked. CLI `sync` performs one run on demand.

**Plaintext workspace**

1. `POST /v1/workspaces` with `encrypted: false`.
2. Store the returned ID locally and reset the cursor to 0.
3. Push all local dirty records; pull from 0.

**Encrypted workspace**

1. Create a separate random 32-byte workspace key, wrapped with a workspace
   passphrase. Publish `encrypted: true`, `keyScope: "workspace"`, `kdfSalt`
   and `wrappedKey`. The private local vault key is never published.
2. Linking retrieves the authenticated member's envelope and unwraps it
   locally. The workspace key is stored in a secret setting, sealed by the
   local vault key. Existing vault keys and passphrases are not replaced.
3. A device without a vault creates a separate local vault using the entered
   workspace passphrase. This encrypts local records and secret settings.
4. Push decrypts the local blob and seals it with the workspace key; pull
   authenticates/decrypts the remote blob and seals it with the local vault key.
   Document AAD is unchanged, so old ciphertext remains compatible.

Older remotes have `keyScope: "legacy"` and remain usable for personal
multi-device sync. They cannot issue team invitations because their key may be
the owner's private vault key. Keep the local data, unlink the old remote, and
create/link a new encrypted remote with a new workspace passphrase to migrate.
Local records are marked dirty with revision zero for a new link.

No passphrase, KEK, or DEK is ever sent to the server. A stolen server database
yields ciphertext and wrapped keys; an attacker still needs the passphrase.

## Push

Request:

```json
{
  "deviceId": "dev_…",
  "ops": [
    {
      "opId": "op_…",
      "docId": "ent_…",
      "kind": "request",
      "baseVersion": 3,
      "ciphertext": "base64 nonce||ciphertext",
      "updatedAt": 1763000000000,
      "deleted": false
    }
  ]
}
```

Rules:

- Maximum 500 operations per request; the client batches dirty records in
  groups of 200.
- `baseVersion` is the version the client last synced (`rev`). A new document
  uses 0.
- If the document exists and its current version differs from `baseVersion`,
  the write is **not applied**. The server returns the current document in
  `conflicts` instead.
- Accepted writes get `newVersion = baseVersion + 1` and the next workspace
  `seq`. The operation log and the current-document row are updated in one
  transaction.
- `opId` makes retries safe: replaying an accepted op returns its original
  `seq` and `version` instead of creating a revision. This matters when a
  response is lost after the server committed.
- For encrypted workspaces `ciphertext` is the JSON payload sealed with the
  shared workspace key, separately from local vault encryption. Authentication data is
  `yourpm:entity:v1:<docId>`, so the server cannot transplant a blob to
  another document. For plaintext workspaces the blob is base64 of the JSON
  payload.
- Deletes are pushes with `deleted: true` and no ciphertext.

Response:

```json
{
  "accepted": [ { "opId": "op_…", "seq": 41, "version": 4 } ],
  "conflicts": [
    {
      "docId": "ent_…",
      "version": 7,
      "kind": "request",
      "ciphertext": "…",
      "updatedAt": 1763000000000,
      "deleted": false
    }
  ]
}
```

## Pull

```
GET /v1/workspaces/{id}/pull?since=<cursor>&limit=500
```

```json
{
  "ops": [
    {
      "seq": 42,
      "docId": "ent_…",
      "version": 4,
      "kind": "request",
      "ciphertext": "…",
      "updatedAt": 1763000000000,
      "deleted": false
    }
  ],
  "next": 42,
  "hasMore": false
}
```

- `since` is exclusive; the returned `next` becomes the new cursor.
- `limit` defaults to 500 and is capped at 1000.
- The client loops until `hasMore` is false, then persists the cursor on the
  workspace row. A second run with no changes pushes and pulls nothing.
- Pulled encrypted records are authenticated and decrypted with the workspace
  key, then sealed with the local vault key before storage.
- Pulled plaintext records are decoded from base64 and also sealed locally if
  the local vault is enabled. Records from another local workspace cannot be overwritten.

## Conflicts

Whole-document conflict detection, not field-level merge. When a push
conflicts:

1. If the local record is dirty and not deleted, the client creates a copy
   named `<name> (conflicted copy)` with a new ID and a position just after
   the original.
2. The server's version is applied to the original record and marked clean.
3. The copy is dirty, so the next sync pushes it as a new document.

The result is deterministic and lossless, at the cost of manual cleanup. This
is the right trade-off for a v1: silently merging headers or scripts from two
devices is worse than showing both.

Deletion conflicts follow the same path: a delete against a changed document
comes back as a conflict carrying the current ciphertext, which is restored
locally; the tombstone is pushed on the next run.

## What syncs

Synced:

- Collections, folders, requests, environments (including tombstones).

Not synced:

- History, settings, UI state, and the sync account itself.
- Workspace list metadata beyond link records.

History stays local by design: it contains live credentials and response
bodies, and is more sensitive than the collection that produced it. Moving it
between devices would widen the exposure surface for little benefit.

## Failure behaviour

- Network errors abort the run; already-accepted operations stay accepted and
  the cursor is only advanced after a successful pull page.
- Per-record seal/open errors are collected in `Result.Errors` and surfaced in
  the UI; other records continue.
- A locked vault refuses to sync an encrypted workspace rather than sending
  plaintext or an empty record.
- The server is single-writer SQLite; a busy database returns 500 and the
  client retries on the next run.
- A push that would store more than 16 MiB for one document is ignored.

## Server operations

- `X-Forwarded-For` is ignored unless `YOURPM_TRUST_PROXY` is enabled. Only
  enable it when the server is reachable solely through a reverse proxy you
  control; otherwise clients can spoof their source IP and evade rate limits.
- `YOURPM_METRICS_TOKEN` optionally requires a bearer token (or `?token=`) for
  `/metrics`. Unauthenticated metrics are convenient but leak traffic volume.
- `YOURPM_OP_RETENTION_DAYS` prunes historical operations at startup and on
  nothing else (there is no background job). Pruning keeps the newest operation
  for every `(workspace, document)` pair, so a new device can still pull the
  current state. A device offline longer than the window may miss intermediate
  revisions and should re-link. `0` disables pruning.
- Auth endpoints allow 20 attempts per minute per IP; the rest of the API
  allows 600 requests per minute per IP. `/healthz` and `/readyz` are exempt.

## Limits worth knowing

- 500 ops per push, 1 000 ops per pull page.
- 64 MiB request body cap on push; 16 MiB per document ciphertext.
- One workspace owner; no ownership transfer UI.
- No key rotation. Member removal revokes server access, not downloaded data.
- Changing the local vault passphrase does not change the personal workspace
  passphrase or server-side member envelope.
