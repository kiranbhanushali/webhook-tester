# Deploying the Webhook Tester (EC2)

A single self-contained Go binary (the React UI is embedded — no separate web server).
Default storage is **SQLite** (one file, durable, survives restarts). No external services required.

## What runs where

| URL (replace HOST with your EC2 host, port 8080) | Purpose |
|---|---|
| `http://HOST:8080/` | **Dashboard** — live + recent events viewer (search, filters, detail) |
| `http://HOST:8080/s/{session-uuid}` | **Configure** an endpoint (status/body, response script, inbound auth, security headers, slug, group, …) |
| `http://HOST:8080/w/{slug}/...` | **The webhook endpoint you give to the caller.** This is what they POST to. PUBLIC (not behind the dashboard token). |
| `http://HOST:8080/api/*` | JSON API (gated by `--auth-token`). Pull events: `GET /api/session/{ref}/events?after=&limit=`, search: `GET /api/search`, recent: `GET /api/events`. |
| `http://HOST:8080/healthz` | Liveness probe (always public) |

> Note: the example URL `…:8080/s/{uuid}` is the **config** page for an endpoint. The address the
> bank actually calls is `…:8080/w/{slug}`.

## Where data is stored

- All data lives in **one SQLite database file** at `--sqlite-path` (env `SQLITE_PATH`, default `./webhook-tester.db`).
- Alongside it SQLite keeps two sidecar files in WAL mode: `<db>-wal` and `<db>-shm`. These are part of the database — back up / move all three together (or checkpoint first).
- It is **durable**: sessions, captured webhooks, extracted identifiers (trackingId/referenceId…) and the
  monotonic event cursor all persist and are intact after a restart.
- **Backup:** stop the process (or run `sqlite3 <db> "PRAGMA wal_checkpoint(TRUNCATE);"`) then copy the `.db`.
- Pick a path on a persistent disk, e.g. `/var/lib/webhook-tester/wh.db`.

## Key flags / env vars

| Flag | Env | Default | Notes |
|---|---|---|---|
| `--port` | `HTTP_PORT` | `8080` | |
| `--addr` | `SERVER_ADDR` / `LISTEN_ADDR` | `0.0.0.0` | bind all interfaces |
| `--storage-driver` | `STORAGE_DRIVER` | `sqlite` | keep `sqlite` |
| `--sqlite-path` | `SQLITE_PATH` | `./webhook-tester.db` | **set to a persistent path** |
| `--auth-token` | `AUTH_TOKEN` | _(empty = open)_ | **set this** in production: gates dashboard + `/api/*`. The `/w/{slug}` webhook path stays public so callers can post. |
| `--max-requests` | `MAX_REQUESTS` | `128` | per endpoint; `0` = unlimited (use a value, e.g. `5000`, unless you want unbounded growth) |
| `--identifier-keys` | `IDENTIFIER_KEYS` | `trackingId,referenceId` | JSON/query keys indexed for search |
| `--hot-index-window` | `HOT_INDEX_WINDOW` | `168h` | in-memory search window. On a **very large** existing DB the boot-time warm-start of this index can be slow; set e.g. `--hot-index-window 30m` for fast startup (older data is still searchable via the SQLite index). |
| `--response-script-timeout` | `RESPONSE_SCRIPT_TIMEOUT` | `1s` | per-request response-template timeout |

Auth from the browser: open the dashboard and enter the token (it sets a `wh_token` cookie used by the
live WebSocket). API clients send `Authorization: Bearer <token>`.

---

## Option A — Docker (recommended)

The repo `Dockerfile` builds everything (runs codegen + frontend build + compiles) into a tiny `scratch` image.

```bash
# on a build host with the repo:
docker build -t webhook-tester:latest .

# run (persist data in a host dir, set a token, expose 8080):
mkdir -p /var/lib/webhook-tester
docker run -d --name webhook-tester --restart unless-stopped \
  -p 8080:8080 \
  -e AUTH_TOKEN='change-me-strong-token' \
  -e STORAGE_DRIVER=sqlite \
  -e SQLITE_PATH=/data/wh.db \
  -e MAX_REQUESTS=5000 \
  -v /var/lib/webhook-tester:/data \
  webhook-tester:latest start
```

Or `docker compose up -d` (see `compose.yml`, the `app-sqlite` service).

Data persists in the `/var/lib/webhook-tester` host volume.

---

## Option B — Raw binary + systemd

Match your EC2 arch: **Graviton = arm64** (e.g. the `ec2-host` box), classic Intel/AMD = amd64.
`scripts/build-linux.sh` produces static binaries (no cgo, no libc dependency) for both:
`dist/webhook-tester-linux-arm64` and `dist/webhook-tester-linux-amd64`.

```bash
# 1) build it (on your Mac/build host, from the repo):
./scripts/build-linux.sh

# 2) copy the matching arch to the EC2 box (arm64 for Graviton):
scp dist/webhook-tester-linux-arm64 ec2-host:/tmp/webhook-tester

# 3) on the EC2 box:
sudo install -m 0755 /tmp/webhook-tester /usr/local/bin/webhook-tester
sudo useradd --system --no-create-home --shell /usr/sbin/nologin webhook || true
sudo mkdir -p /var/lib/webhook-tester
sudo chown webhook:webhook /var/lib/webhook-tester

# 4) install the service, set the token, start it:
sudo cp deployments/webhook-tester.service /etc/systemd/system/
sudo systemctl edit webhook-tester   # set AUTH_TOKEN (see the [Service] Environment line)
sudo systemctl daemon-reload
sudo systemctl enable --now webhook-tester
sudo systemctl status webhook-tester
journalctl -u webhook-tester -f
```

The unit (see `deployments/webhook-tester.service`) runs as the `webhook` user, stores data in
`/var/lib/webhook-tester/wh.db`, and restarts on failure / boot.

---

## EC2 checklist

- **Security group:** allow inbound TCP **8080** from the callers that need it (the caller egress IPs, and your own IP for the dashboard). Prefer not exposing it to `0.0.0.0/0`.
- **Always set `AUTH_TOKEN`** so the dashboard/API aren't open. The webhook path `/w/{slug}` is intentionally public; protect individual endpoints with per-endpoint **inbound auth** (configure a required header + value on the endpoint's config page).
- **HTTPS:** for real bank traffic put it behind an ALB or an nginx/Caddy TLS terminator on the instance, proxying to `127.0.0.1:8080`. (Bank callbacks usually require HTTPS.)
- **Backups:** snapshot `/var/lib/webhook-tester/` (or the EBS volume) on a schedule.

## Upgrading

- Docker: rebuild the image, `docker compose up -d` (the `/data` volume keeps your DB; schema migrations run automatically on start).
- Binary: rebuild, `scp`, replace `/usr/local/bin/webhook-tester`, `sudo systemctl restart webhook-tester`. The DB file is forward-migrated on startup.
