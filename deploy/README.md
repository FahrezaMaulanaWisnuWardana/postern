# Deploying the sync server

The server is one Go binary backed by SQLite. It has no Postgres, Redis,
object storage, or worker processes. This directory contains a
production-leaning Compose stack: the server plus Caddy for automatic TLS.

## What you need

- A Linux host with Docker Engine and the Compose plugin.
- A DNS record (A or AAAA) for your domain pointing at the host.
- Ports 80 and 443 reachable from the internet, so Caddy can obtain a
  certificate.
- Somewhere to back up the `yourpm-data` volume.

## First run

```sh
cd deploy
cp .env.example .env
```

Edit `.env`:

- `YOURPM_DOMAIN` — e.g. `sync.example.com`.
- `YOURPM_JWT_SECRET` — output of `openssl rand -hex 32`.
- `YOURPM_REGISTRATION` — leave `open` for the first start, or seed an account
  with `YOURPM_ADMIN_EMAIL` and `YOURPM_ADMIN_PASSWORD`.

```sh
docker compose up -d --build
docker compose logs -f caddy yourpm
```

Verify:

```sh
curl -fsS https://sync.example.com/healthz
curl -fsS https://sync.example.com/readyz
```

Then, in the desktop app, open **Settings → Sync**, enter
`https://sync.example.com`, create an account or sign in, and link a
workspace.

After the first account exists, set `YOURPM_REGISTRATION=closed` in `.env` and
run `docker compose up -d` again.

## Configuration

| Variable | Default | Notes |
| --- | --- | --- |
| `YOURPM_ADDR` | `:8080` | Set in Compose; the proxy expects this port. |
| `YOURPM_DATA` | `/data` | Compose maps this to the `yourpm-data` volume. |
| `YOURPM_JWT_SECRET` | generated | Required by Compose; set it to a stable random value. |
| `YOURPM_REGISTRATION` | `closed` in Compose | `open` allows public sign-up. |
| `YOURPM_ADMIN_EMAIL` | — | Created at startup if it does not exist. |
| `YOURPM_ADMIN_PASSWORD` | — | Used only when creating that account. |
| `YOURPM_TRUST_PROXY` | `1` in Compose | Trust `X-Forwarded-For`. Safe here because Caddy is the only path. |
| `YOURPM_METRICS_TOKEN` | — | Require this bearer token for `/metrics`. |
| `YOURPM_OP_RETENTION_DAYS` | `0` | Prune sync ops older than this; `0` keeps everything. |

If `YOURPM_JWT_SECRET` is not set (for example when running the binary
directly), the server generates `<data>/jwt.key` with `0600` permissions.
Rotating the secret invalidates all access tokens; users sign in again.

## Data and backups

Everything lives in the `yourpm-data` volume: `sync.db`, its WAL files, and
possibly `jwt.key`. Compose prefixes volumes with the project name, which
defaults to the directory (`deploy_yourpm-data` when started from this folder).

```sh
docker compose stop yourpm
docker run --rm -v deploy_yourpm-data:/data -v "$PWD":/backup alpine \
  tar czf /backup/yourpm-$(date +%F).tar.gz -C /data .
docker compose start yourpm
```

The archive is encrypted workspace content, but account emails and metadata
are inside. Store it accordingly. Losing `jwt.key` only signs users out; losing
`sync.db` loses server copies, not the data already on clients.

## Upgrades

```sh
git pull
docker compose build yourpm
docker compose up -d
```

The server migrates its schema at startup with `CREATE TABLE IF NOT EXISTS`
statements and additive changes. Back up before upgrading anyway.

## Hardening

- Keep registration closed unless you want public accounts.
- Set `YOURPM_METRICS_TOKEN` if `/metrics` leaves the Docker network. Caddy
  does not expose it by default; keep it that way or require the token.
- `YOURPM_TRUST_PROXY=1` is set because Caddy is the only route to the server.
  Remove it only if you stop using the bundled reverse proxy, otherwise rate
  limits would bucket every client under the proxy's address.
- Do not publish port 8080 on a public interface.
- Keep the host and Docker patched; the stack deliberately has a small
  dependency surface.
- Use a firewall that allows only 22, 80, and 443 (adjust for your SSH port).
- The container runs as an unprivileged user and the server never logs
  payloads, headers, or query strings.

## Troubleshooting

- **Caddy cannot get a certificate** — check DNS and that ports 80/443 are
  reachable; `docker compose logs caddy`.
- **`readyz` returns 503** — the SQLite database is unavailable; check volume
  permissions and logs.
- **Clients get 401 after an upgrade** — `YOURPM_JWT_SECRET` changed. Sign in
  again.
- **Registration is disabled** — either seed an admin account via `.env` or
  temporarily set `YOURPM_REGISTRATION=open`, create the account, then close
  it again.

## Running without Docker

```sh
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o yourpm-server ./cmd/yourpm-server

YOURPM_ADDR=127.0.0.1:8080 \
YOURPM_DATA=/var/lib/yourpm \
YOURPM_JWT_SECRET="$(openssl rand -hex 32)" \
YOURPM_REGISTRATION=closed \
./yourpm-server
```

Terminate TLS in front of it with Caddy, nginx, or a load balancer. Do not
send bearer tokens over plain HTTP.
