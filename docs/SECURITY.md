# Security

Postern is a local-first tool with an optional self-hosted sync service. This
document states what is protected, what is not, and which assumptions the
design makes.

## Summary

- Workspace contents can be end-to-end encrypted with a passphrase only you
  know. The sync server stores ciphertext, wrapped keys, and routing metadata.
- The server never receives the passphrase, the key-encryption key, or the
  data-encryption key in plaintext.
- Network traffic uses TLS when you deploy behind a reverse proxy (the Compose
  setup does this automatically).
- There is no telemetry, crash reporting, update check, or third-party request.
- Local metadata (workspace names, record count, timestamps, sizes) is visible
  to anyone who can read the database file unless full-disk encryption is used.

## Threat model

Protected against:

- **A curious or compromised sync server.** It sees ciphertext, document IDs,
  versions, sizes, and timestamps, but not request names, URLs, headers,
  bodies, tokens, scripts, or environment values.
- **Database theft from the server.** Backups and disks contain sealed blobs
  and Argon2id-wrapped keys. Without the passphrase the data is unreadable.
- **Network attackers.** TLS protects credentials, tokens, and ciphertext in
  transit. Access tokens are short-lived and device-bound.
- **A stolen or lost device.** With the vault enabled and the app locked, the
  local database is ciphertext. Device revocation stops further sync.
- **Cross-record ciphertext substitution by the server.** Every blob is bound
  to its record ID and purpose through authenticated additional data.

Not protected against:

- **A compromised client while the vault is unlocked.** The data key lives in
  process memory; malware, a debugger, or a memory dump can read it.
- **A weak passphrase.** Argon2id raises the cost of guessing but cannot make
  `password123` safe. Use a long, unique passphrase.
- **Metadata analysis.** The server knows workspace IDs, document counts,
  update timing, and ciphertext sizes. Traffic analysis can reveal activity
  patterns even though content stays private.
- **A malicious server serving stale or missing data.** It cannot forge
  content, but it can withhold, replay, or roll back whole records within the
  protocol's version rules. Clients detect version mismatches, not
  equivocation. Keep independent backups.
- **Compromised endpoints during sync.** If an attacker controls a device
  while it is unlocked, encryption at rest does not help.
- **Legal coercion of the server operator.** The operator cannot decrypt, but
  can be forced to hand over ciphertext and metadata, or to stop service.

## Key hierarchy

```
passphrase
   │  Argon2id (t=2, m=64 MiB, p=4, salt=16 random bytes)
   ▼
KEK (32 bytes, never stored or transmitted)
   │  XChaCha20-Poly1305, AAD "yourpm:vault:v1"
   ▼
wrapped DEK  ── stored locally for the private vault
   │  unwrap
   ▼
DEK (32 random bytes)
   │  XChaCha20-Poly1305, AAD "yourpm:entity:v1:<entity-id>"
   ▼
encrypted entity blobs
```

- New encrypted remotes use a separate random workspace key, not the private
  vault DEK shown above. Each member has their own passphrase-wrapped workspace
  key on the server. Local copies of workspace keys are sealed secret settings.
- Linking a second device unwraps that member's workspace key. It does not
  replace the local vault key or passphrase. Sync re-encrypts between local
  vault and remote workspace keys.
- Inviting a teammate wraps only the workspace key with a locally generated
  random 256-bit unlock code shared outside the server. Recipients choose their
  own workspace passphrase. Codes expire with the invitation after seven days;
  no email is sent. The server receives neither codes nor passphrases.
- Changing the local vault passphrase rewraps its DEK, not synced workspace
  member envelopes. The workspace passphrase used on other devices is separate.
- Legacy remotes may reuse the owner's private vault DEK. Personal sync remains
  compatible, but team invitations require creating a new isolated-key remote.
- History bodies use a separate AAD (`yourpm:history:v1:<id>`) and stay
  local. History is not synced.
- Passwords for server accounts are hashed with Argon2id and verified in
  constant time. Refresh tokens are stored as SHA-256 hashes; access tokens
  are HS256 JWTs.

Owners/admins can remove members and revoke invitations. Removal blocks future
server access but cannot erase downloaded copies or revoke known encryption keys.
There is no key rotation or per-member key epoch UI. Rotate exposed API
credentials separately. The server schema has `key_epoch` fields for future use.

## Authentication and session handling

- Access tokens expire after one hour. They include the user ID and device ID
  and are signed with the server's HS256 key.
- If `YOURPM_JWT_SECRET` is unset, a random 32-byte key is generated once at
  `<data>/jwt.key` with `0600` permissions.
- Refresh tokens are 32 random bytes, rotated on every refresh. Replaying an
  old refresh token fails.
- Every authenticated request checks the device row; revoking a device
  invalidates its access tokens immediately.
- Registration can be closed. Auth endpoints are rate limited per source IP
  (20 attempts per minute). Responses for unknown accounts and wrong passwords
  are identical.
- The server never logs request bodies, query strings, headers, or tokens.
  Logs contain method, path template, status, duration, and response size.

## Transport and deployment

- Run the server behind a TLS terminator. `deploy/docker-compose.yml` includes
  Caddy with automatic certificates and HSTS.
- Keep `YOURPM_REGISTRATION=closed` after creating accounts unless you intend
  to accept public sign-ups.
- The client warns before creating an unencrypted remote workspace. Heed it:
  for `encrypted: false` the server stores readable request contents, headers,
  tokens, and bodies.
- Do not expose the server directly on the internet without TLS; bearer
  tokens and workspace ciphertext would travel in clear text.
- The process runs as a non-root user in the container. The data volume should
  be readable only by that user.
- Back up the data volume. Backups contain ciphertext, but they are still
  sensitive: they reveal account emails, IP-derived metadata is not stored,
  and loss of the passphrase makes the data unrecoverable.

## Local storage

| Data | Where | Protection |
| --- | --- | --- |
| Entities | `postern.db` | Sealed when the vault is enabled. |
| History bodies | `postern.db` | Sealed when the vault is enabled. |
| Sync access and refresh tokens | `postern.db` settings | Sealed with the DEK when the vault is enabled; plaintext otherwise. |
| OAuth2 access and refresh tokens | `postern.db` settings | Sealed with the DEK when the vault is enabled; plaintext otherwise. |
| Vault salt and wrapped DEK | `postern.db` settings | Salt and wrapped key only. |
| JWT signing key (server) | `<data>/jwt.key` | Plain file, `0600`; set an env secret to avoid it. |

Existing installations can retain a historical database filename when Postern
reuses their data in place; see [RELEASING.md](RELEASING.md).

The database directory is created with `0700`, and the database plus its WAL
sidecars are chmodded to `0600` on POSIX systems (best effort; Windows ACLs
apply instead). This is not a substitute for full-disk encryption: filenames,
database size, and the existence of workspaces remain visible.

When the vault is unlocked, the DEK is held in memory until the app locks or
exits. Locking drops the key and makes entity reads fail. The optional
auto-lock timer locks the vault after a configured number of inactive minutes;
pointer and keyboard activity reset the timer. The CLI requires
`YOURPM_PASSPHRASE` for locked vaults and does not cache it.

OAuth2 authorization-code tokens are stored as sealed settings under a key
derived from the grant, endpoints, client ID, scope, and configured redirect.
The interactive flow uses PKCE (S256) and a random `state`, and binds the
listener to `127.0.0.1`; an explicit redirect must also be a loopback URL. The
client secret and tokens are never sent anywhere except the provider's own token
endpoint. A provider-issued refresh token is used automatically when the access
token expires. Without the vault, these tokens are plaintext in `postern.db`, so
enable the vault if the machine is shared.

Workspace backups (`postern backup`, **Export workspace backup**) are plaintext
JSON unless you supply a passphrase. Plaintext archives are readable regardless
of vault state; they exist for migration and offline copies, so delete them
after use or keep them on encrypted media. A passphrase-encrypted backup uses
Argon2id and XChaCha20-Poly1305 with a fresh salt and is independent of the
vault key: it can be restored on a machine without the vault passphrase, and the
backup passphrase cannot be recovered if lost.

## What the server can see

For an encrypted workspace:

- Account email, device names, last-seen timestamps, and source IPs (in
  memory for rate limiting; not persisted by the app).
- Workspace ID, name, creation time, owner, and member list.
- Document IDs, kinds, versions, deletion flags, ciphertext lengths, and
  update timestamps.
- Passphrase-wrapped workspace keys and KDF salts; invitation recipient emails,
  roles, expiry times and unlock-code-wrapped workspace keys (not the codes).

It cannot see:

- Request names, URLs, query parameters, headers, bodies, auth values, or
  environment variables.
- Scripts, test results, response bodies, or history.
- The passphrase or any unwrapped key.

For a plaintext workspace (`encrypted: false`), the server still stores blobs
as base64 but they are **not encrypted**. Use encrypted workspaces for
anything sensitive.

## Operational guidance

- Use a unique passphrase per workspace and store it in a password manager.
  There is no recovery mechanism; losing it means losing synced data.
- Lock the vault when leaving the machine. The lock screen drops the key;
  unlock with the passphrase. Configure auto-lock in **Settings → Vault** if
  you tend to forget.
- Revoke a lost device in **Settings → Sync → Signed-in devices**, or with
  `yourpm devices` and `yourpm revoke-device <id>`. Revocation invalidates that
  device's access tokens immediately.
- Treat the sync server as untrusted infrastructure. Keep local exports or
  filesystem backups of important collections, and encrypt those backups with a
  passphrase unless they live on encrypted media.
- Rotate `YOURPM_JWT_SECRET` only by signing everyone out; existing access
  tokens become invalid immediately.
- Set `YOURPM_METRICS_TOKEN` if `/metrics` is reachable from anywhere you do
  not fully trust. Without it metrics are unauthenticated by design.
- Enable `YOURPM_TRUST_PROXY` only when a reverse proxy you control is the sole
  path to the server. Otherwise `X-Forwarded-For` is ignored, which is the safe
  default for direct exposure.
- `YOURPM_OP_RETENTION_DAYS` bounds the operation log but can strand devices
  that have been offline longer than the window; they must re-link. Leave it at
  `0` unless database growth is a real problem.

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Contact the
maintainer privately (the address used to distribute your build) with:

- the affected version and platform,
- a description and reproduction steps,
- whether the issue affects the client, the server, or the protocol.

You will get an acknowledgement and a coordinated disclosure timeline.
