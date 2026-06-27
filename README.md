<p align="center">
  <a href="https://github.com/kiranbhanushali/webhook-tester#readme">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset="https://socialify.git.ci/kiranbhanushali/webhook-tester/image?description=1&font=Raleway&forks=1&issues=1&logo=https%3A%2F%2Fgithub.com%2Fuser-attachments%2Fassets%2Fe2e659dc-7fb1-4ac2-ad3c-883899f5fc38&owner=1&pulls=1&pattern=Solid&stargazers=1&theme=Dark">
      <img align="center" src="https://socialify.git.ci/kiranbhanushali/webhook-tester/image?description=1&font=Raleway&forks=1&issues=1&logo=https%3A%2F%2Fgithub.com%2Fuser-attachments%2Fassets%2Fe2e659dc-7fb1-4ac2-ad3c-883899f5fc38&owner=1&pulls=1&pattern=Solid&stargazers=1&theme=Light">
    </picture>
  </a>
</p>

# WebHook Tester —  fork

> **A fork of [tarampampam/webhook-tester](https://github.com/tarampampam/webhook-tester).** It keeps the upstream's
> fast Go core and embedded React UI, and adds a layer of capabilities aimed at debugging **real, long-running
> callback integrations** — capturing provider callbacks, searching for a specific transaction by identifier,
> signing responses, and re-delivering a captured request. It was built for bank / callback integration
> integration testing, where callbacks carry identifiers like `trackingId` / `referenceId`, but nothing here is
> specific — it's a general-purpose, self-hosted webhook debugger.

This application lets you test and debug webhooks and HTTP requests using unique URLs. You can customize the response
code, headers, body, and even a response delay — and, in this fork, generate the response dynamically from the
incoming request, search captured requests by identifier, and replay them downstream.

Consider it a free and self-hosted alternative to [webhook.site](https://github.com/fredsted/webhook.site),
[requestinspector.com](https://requestinspector.com/), and similar services.

Built with Go for high performance, with a lightweight `ReactJS` UI compiled into the binary, so no additional assets
are required. WebSocket support provides real-time webhook notifications in the UI — no third-party services needed.

## ✨ What this fork adds

Everything below is **net-new on top of upstream** (see [Credits](#-credits--upstream)). Each links to its details.

- **[Pure-Go SQLite storage (now the default)](#-storage)** — a single embedded DB file via `modernc.org/sqlite`
  (no CGO, still a single static binary / `scratch` image). Survives restarts and makes identifier search **fully
  indexed**.
- **[Identifier search](#identifier-search)** — find a captured request by `trackingId` / `referenceId` (or any
  configurable key) across the JSON body, query string, and headers. Index-backed on SQLite, with an in-memory hot
  index for instant recent lookups.
- **[Human-readable slugs, groups & long-lived sessions](#webhook-capture-url-slugs--groups)** — address endpoints
  by `my-callback` instead of a UUID, organise them into groups, and keep a fixed callback URL valid
  indefinitely.
- **[Dynamic response templates](#dynamic-response-templates)** — a Go `text/template` evaluated against the incoming
  request to build the response body and status, with signing helpers (`hmacSHA256`, `sha256`, …) and an `@status`
  directive.
- **[Replay](#replay)** — re-deliver any captured request to a target URL (or the session's forward URL) using its
  original method, body, and headers.
- **[FIFO events API](#fifo-events-api-incremental-polling)** — pull a session's requests in order via a durable,
  never-reused cursor for exactly-once incremental polling.
- **[Per-endpoint inbound auth](#-per-endpoint-inbound-auth)** — require a secret header on the public capture path;
  unauthorized calls are still recorded (and flagged) but get a `401`.
- **[Firehose + unified dashboard](#-firehose--unified-dashboard)** — a single all-sessions live WebSocket stream and
  a redesigned dashboard (endpoint rail + live stream + request detail drawer).
- **[JSON-query selector in the payload viewer](#json-query-selector)** — pull a value out of a captured JSON payload
  with `data.txn.trackingId` / `items[*].id`, no dependencies.
- **[Paginated requests list with infinite scroll](#paginated-requests-list)** — cursor-paginated, newest-first
  request history that loads more as you scroll.
- **[Shared-token dashboard/API auth](#-dashboard--api-authentication)** — protect the dashboard and all `/api`
  endpoints with a single token; the webhook-capture path stays public.

### Inherited from upstream

Standalone operation (no third-party dependencies), fully customizable response code/headers/body, optional public
tunneling (`ngrok`), built-in WebSocket notifications, a `scratch`-based multi-arch Docker image running as an
unprivileged user, a CLI health-check sub-command, JSON / human-readable logging, liveness probes (`/healthz`), a
binary view of recorded requests, and efficient memory/CPU usage.

### 🗃 Storage

The app supports 4 storage drivers: **sqlite** (default, added by this fork), **memory**, **Redis** and **fs**
(configured with the `--storage-driver` flag).

- **SQLite** driver (**default**): A single embedded database file (`--sqlite-path`, default `./webhook-tester.db`)
  using the pure-Go `modernc.org/sqlite` engine (no CGO). Data survives restarts and, crucially, identifier search
  (see below) is **fully indexed**, so it stays fast over long histories. This is the recommended driver for the
  callback-debugging workflow.
- **Memory** driver: Ideal for local debugging when persistent storage isn't needed, as recorded requests are cleared
  upon app shutdown. Identifier search falls back to a non-indexed scan.
- **Redis** driver: Retains data across app restarts, suitable for environments where data persistence is required.
  Redis is also necessary when running multiple instances behind a load balancer.
- **FS** driver: Keep all the data in the local filesystem, useful when you need to store data between app restarts.

> [!NOTE]
> When running the Docker image with the sqlite driver, point `SQLITE_PATH` at a writable, mounted volume (e.g.
> `-e SQLITE_PATH=/data/wh.db -v "$(pwd)/.wh-data:/data"`); the image's root filesystem is read-only.

### 📢 Pub/Sub

For WebSocket notifications, two drivers are supported for the pub/sub system: **memory** and **Redis** (configured
with the `--pubsub-driver` flag).

When running multiple instances of the app, the Redis driver is required.

### 🚀 Tunneling

Capture webhook requests from the global internet using the `ngrok` tunnel driver. Enable it by setting the
`--tunnel-driver=ngrok` flag and providing your `ngrok` authentication token with `--ngrok-auth-token`. Once enabled,
the app automatically creates the tunnel for you – no need to install or run `ngrok` manually (even using docker).

With this public URL, you can test your webhooks from external services like GitHub, GitLab, Bitbucket, and more.
You'll never miss a request!

## 🛰 Sessions, search, events, replay & dynamic responses

Beyond the basic capture-and-inspect flow, this fork adds a set of capabilities aimed at debugging real,
long-running callback integrations. All of the dashboard/data endpoints below live under `/api` and are protected
by the optional shared token (see [Authentication](#-dashboard--api-authentication)); the webhook-capture path
`/w/...` is always public (optionally gated by [per-endpoint inbound auth](#-per-endpoint-inbound-auth)).

### Webhook capture URL, slugs & groups

Send (capture) a webhook with any HTTP method to:

```
http(s)://<host>/w/{ref}/<anything you like>
```

- `{ref}` is either a **slug** or a session **UUID**. A slug is human-readable (2–49 chars, lowercase letters,
  digits and dashes, must start with a letter or digit), e.g. `my-callback`. If you don't supply a slug when
  creating a session, one is generated for you. The session remains addressable by its UUID as well.
- Any trailing numeric path segment overrides the response status code, e.g. `POST /w/my-callback/orders/202`
  responds with `202`. The last numeric segment wins.
- **Groups** are an optional label (`group`) used to organise sessions; the listing endpoint can filter by group.
- **Long-lived** sessions (`long_lived: true`) ignore the normal `--session-ttl` expiry, so a fixed callback URL
  stays valid indefinitely.

### Listing sessions

`GET /api/sessions` returns every session with its `uuid`, `slug`, `group`, `status_code`, `requests_count`,
`last_request_unix_milli`, timestamps and `long_lived`. Optional query params: `group` (exact match) and `q`
(case-sensitive substring over id/slug/group).

### Identifier search

At capture time the app extracts **searchable identifiers** from each request and indexes them. By default it looks
for the keys `trackingId` and `referenceId` (configurable via `--identifier-keys`) in the **JSON body** and the
**URL query string**, plus any **header** names listed in `--identifier-headers`. Identifier keys are stored
lower-cased; values keep their original casing.

```
GET /api/search?value=T-123&key=trackingId&match=exact
```

| Query param | Meaning |
|-------------|---------|
| `value`     | **required** — identifier value to match |
| `key`       | identifier key to match (omit to match any key) |
| `match`     | `exact` (default) or `prefix` |
| `group`     | restrict to sessions in this group |
| `session`   | restrict to a single session (UUID or slug) |
| `from`,`to` | capture-time bounds, unix milliseconds |
| `limit`     | maximum number of results |

Each result item contains `session_uuid`, `session_slug`, `request_uuid`, `key`, `value` and
`captured_at_unix_milli`. With the **sqlite** driver the search is index-backed (and supports `prefix`/`group`);
the in-memory hot index (retention `--hot-index-window`, default 7 days) serves recent exact-key lookups on the
fast path. The memory/fs drivers fall back to a non-indexed scan.

### FIFO events API (incremental polling)

`GET /api/session/{ref}/events?after={cursor}&limit={n}` returns a session's captured requests in **FIFO order**
(oldest first) with `seq` greater than `after`, up to `limit` (default 100, max 1000). The response is:

```jsonc
{
  "events":      [ { "seq": 1, "uuid": "…", "client_address": "…", "method": "POST", "request_payload_base64": "…", "headers": […], "url": "…", "captured_at_unix_milli": … }, … ],
  "next_cursor": 2,      // pass this as `after` on the next poll
  "has_more":    true    // true when the page was full (more may remain)
}
```

`seq` is a **durable, strictly-increasing, never-reused** sequence, so a consumer that always passes back
`next_cursor` gets every event exactly once — no skips, no duplicates — even across request eviction. Typical
consumer loop:

```bash
cursor=0
while :; do
  resp=$(curl -s -H "Authorization: Bearer $TOKEN" \
    "$BASE/api/session/my-callback/events?after=$cursor&limit=100")
  echo "$resp" | jq -c '.events[]'
  cursor=$(echo "$resp" | jq -r '.next_cursor')
  [ "$(echo "$resp" | jq -r '.has_more')" = "true" ] || sleep 2   # caught up → back off
done
```

### Replay

Re-deliver a previously captured request to any URL (using its original method, body and headers; hop-by-hop
headers are stripped):

```
POST /api/session/{ref}/requests/{request_uuid}/replay
{ "target_url": "https://downstream.example.com/hook" }
```

If `target_url` is omitted, the session's configured `forward_url` is used. The response reports the **downstream**
result: `status_code`, `headers` and `body_base64`. Redirects are **not** followed.

> [!WARNING]
> Replay is an operator-facing tool and performs **no SSRF protection** — the target URL is fully trusted. Do not
> expose the dashboard/API to untrusted callers.

### Dynamic response templates

A session may carry a `response_script`: a [Go `text/template`](https://pkg.go.dev/text/template) evaluated against
the incoming request to build the response **body** and (optionally) **status code**.

> [!IMPORTANT]
> A response script sets the response **body and status only — it does NOT set response headers**. Response headers
> come from the session's static `headers` plus its `security_headers`. (If a script fails to parse/execute, the app
> falls back to the session's static response. Execution is bounded by `--response-script-timeout`, default `1s`.)

Template **data** (the `.` value):

| Field | Description |
|-------|-------------|
| `.Method` | request method |
| `.Path`   | request URL path |
| `.Slug`   | session slug |
| `.Body`   | raw request body (string) |
| `.Query`  | `map[string]string` of query params (first value per key) |
| `.Header` | `map[string]string` of headers (canonical name → first value) |
| `.Now`    | capture time (`time.Time`) |
| `.JSON`   | request body parsed as JSON (`map`/`slice`/scalar), or `nil` if not valid JSON |

Helper **functions**: `json v`, `jsonPath v "a.b.0.c"`, `uuid`, `now [layout]`, `randInt min max`, `randHex n`,
`base64 s`, `sha256 s`, `hmacSHA256 key msg`, `upper s`, `lower s`, `default def v`, `seq n`.

**Status directive:** if the **first line** of the rendered output is exactly `@status NNN` (100–599), that line is
consumed and used as the HTTP status; the rest is the body.

Example — echo the incoming `trackingId` and return an HMAC-SHA256 signature **in the body**:

```gotemplate
{{ $id := jsonPath .JSON "trackingId" }}@status 200
{"echo":"{{ $id }}","sig":"{{ hmacSHA256 "secret" $id }}"}
```

For `POST /w/my-callback/anything` with body `{"trackingId":"T-123"}` this responds `200` with
`{"echo":"T-123","sig":"5304a3bf…"}`. (To put a signature in a *header* instead, add it as a static/security header
on the session — scripts cannot set headers.)

### Security headers

A session's `security_headers` (name/value pairs) are added to **every** captured-request response, on both the
script and the static-response paths — handy for asserting things like `X-Content-Type-Options: nosniff`.

### 🛡 Per-endpoint inbound auth

Independently of the dashboard/API token, an individual capture endpoint can require a secret on the **public**
`/w/{ref}/...` path. Configure two session fields:

| Session field | Meaning |
|---------------|---------|
| `inbound_auth_header` | name of the header the caller must send (empty = endpoint is open) |
| `inbound_auth_value`  | the expected secret value |

When `inbound_auth_header` is set, each incoming webhook must present that header with a value matching
`inbound_auth_value` (compared in constant time). On mismatch the app responds `401`
(`{"error":"unauthorized webhook"}`) and **skips** the response script / static response. The request is **still
captured** either way and carries an `authorized` boolean, so you can see — and search — calls that failed auth.
In the UI, unauthorized requests show a red **Unauthorized** badge in the request detail and live stream.

### 🔭 Firehose + unified dashboard

The original UI showed one session at a time over a per-session WebSocket. This fork adds a single **firehose**
stream and a redesigned dashboard on top of it:

- **Firehose stream** — `GET /api/firehose/subscribe` (WebSocket, operator-only) pushes a `create` event for
  **every** captured request across **all** sessions. Each event carries `action`, `session_uuid`, `session_slug`
  and a compact `request` (`uuid`, `client_address`, `method`, `url`, `captured_at_unix_milli`, `authorized`).
  Headers and bodies are intentionally **omitted** from firehose events (fetch the full request from its session)
  so no inbound-auth secrets travel over the cross-session topic.
- **Unified dashboard** — an **endpoint rail** (all sessions, with request counts and a pulsing "live" dot when an
  endpoint received a request in the last few seconds), an **all-sessions live stream** in the centre, and a
  **request detail drawer** that slides in when you click a row. Pick "All endpoints" for the cross-session view or
  a single endpoint to filter.

### JSON-query selector

When a captured payload is valid JSON, the payload viewer shows a small query box for pulling a value straight out
of it. The evaluator is **dependency-free** and supports:

- dotted paths — `data.txn.trackingId`, `.a.b`
- array indices — `items[0]`, `[0]`
- bracketed/quoted keys — `data["weird key"]`
- wildcards — `items[*].id` (collect a field from every element), `data.*` (all values of an object)

It resolves **own-properties only** (never `__proto__` / `constructor`), and updates the result as you type.

### Paginated requests list

A session's request history is **cursor-paginated, newest-first**. The list loads the first page and then fetches
older pages automatically as you scroll (infinite scroll via an intersection observer). New requests arriving over
the WebSocket are prepended live, and already-seen requests are de-duplicated when older pages load.

```
GET /api/session/{ref}/requests?before={cursor}&limit={n}
```

Omit `before` (or pass `0`) for the newest page, then pass the returned `next_before` as `before` to fetch the next
(older) page. The response (`CapturedRequestsPage`) carries `items[]`, `next_before` (the next cursor) and
`has_more`. Each item's `seq` is the durable capture sequence used as the cursor.

### 🔐 Dashboard / API authentication

Set `--auth-token` (env `AUTH_TOKEN`) to protect the **dashboard and all `/api` endpoints** with a shared token.
The webhook-capture path (`/w/...`) and the health probes (`/healthz`, `/ready`) stay **public**. An empty token
(the default) disables auth entirely.

Two ways to present the token:

- **Header** (API/automation): `Authorization: Bearer <token>` on every `/api` request.
- **Cookie** (browser/WebSocket): `POST /api/auth/login` with `{"token":"<token>"}` sets an HttpOnly `wh_token`
  cookie; `GET|POST /api/auth/logout` clears it.

```bash
curl -H "Authorization: Bearer secret-token" http://localhost:8080/api/sessions
```

> [!NOTE]
> This dashboard/API token is **separate** from [per-endpoint inbound auth](#-per-endpoint-inbound-auth): the former
> gates the operator-facing dashboard/API, the latter gates an individual public capture endpoint.

## ⁉ FAQ

**Can I have pre-defined (static) webhook URLs (sessions) to ensure that the sent request will be captured even
without data persistence?**

Yes, simply use the `--auto-create-sessions` flag or set the `AUTO_CREATE_SESSIONS=true` environment variable. If
the incoming request contains a UUID-formatted prefix (e.g.,
`http://app/11111111-2222-3333-4444-555555555555/...`), a session for this request will be created automatically.
All that's left for you to do is open the session in the UI
(`http://app/s/11111111-2222-3333-4444-555555555555`).

> [!TIP]
> With the default **sqlite** driver, sessions and requests persist across restarts anyway, so a slug-based callback
> URL such as `/w/my-callback/...` stays valid without `--auto-create-sessions`.

## 🧩 Installation

This fork doesn't publish prebuilt release binaries or container images — build it from source. You need **Go 1.26+**
and **Node.js** (for the embedded frontend). The frontend is compiled into the Go binary, so the build is a single
static artifact with no runtime assets.

### Docker (recommended)

The `Dockerfile` builds the frontend and the Go binary in one shot and produces a minimal `scratch`-based image:

```shell
git clone https://github.com/kiranbhanushali/webhook-tester.git
cd webhook-tester
docker build -t webhook-tester .
docker run --rm -t -p "8080:8080/tcp" webhook-tester
```

### From source

```shell
git clone https://github.com/kiranbhanushali/webhook-tester.git
cd webhook-tester

# build the embedded frontend (outputs to web/dist, which the Go binary embeds)
npm --prefix ./web ci
npm --prefix ./web run build

# build the binary (the frontend is embedded into it)
go build -o webhook-tester ./cmd/webhook-tester
./webhook-tester start
```

> [!TIP]
> A `Makefile` + `compose.yml` are included for a containerised dev workflow: `make up` starts the Go server
> (`:8080`) and the Vite dev server (`:8081`) with hot reload. See `make help` for all targets.

## ⚙ Usage

Start the app (after building per [Installation](#-installation)):

```shell
docker run --rm -t -p "8080:8080/tcp" webhook-tester
```

> [!NOTE]
> This starts the app with the default configuration on port `8080` (the first port in `-p` is the host port, the
> second is the application port inside the container).

Open your browser at [`localhost:8080`](http://localhost:8080) to begin testing your webhooks. To stop the app,
press `Ctrl+C` in the terminal where it's running.

For a **persistent + token-protected** instance (sqlite database on a mounted volume, dashboard/API behind a shared
token, webhook path still public):

```shell
docker run --rm -t -p "8080:8080/tcp" \
  -e AUTH_TOKEN=secret-token \
  -e STORAGE_DRIVER=sqlite \
  -e SQLITE_PATH=/data/wh.db \
  -v "$(pwd)/.wh-data:/data" \
  webhook-tester
```

For custom configuration options, refer to the CLI help below or execute the app with the `--help` flag.

<!--GENERATED:CLI_DOCS-->
<!-- Documentation inside this block generated by github.com/urfave/cli-docs/v3; DO NOT EDIT -->
## CLI interface

webhook tester.

Usage:

```bash
$ app [GLOBAL FLAGS] [COMMAND] [COMMAND FLAGS] [ARGUMENTS...]
```

Global flags:

| Name               | Description                                 | Type   | Default value | Environment variables |
|--------------------|---------------------------------------------|--------|:-------------:|:---------------------:|
| `--log-level="…"`  | Logging level (debug/info/warn/error/fatal) | string |   `"info"`    |      `LOG_LEVEL`      |
| `--log-format="…"` | Logging format (console/json)               | string |  `"console"`  |     `LOG_FORMAT`      |

### `start` command (aliases: `s`, `server`, `serve`, `http-server`)

Start HTTP/HTTPs servers.

Usage:

```bash
$ app [GLOBAL FLAGS] start [COMMAND FLAGS] [ARGUMENTS...]
```

The following flags are supported:

| Name                            | Description                                                                                                                                                  | Type     |         Default value         |    Environment variables     |
|---------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------|----------|:-----------------------------:|:----------------------------:|
| `--addr="…"`                    | IP (v4 or v6) address to listen on (0.0.0.0 to bind to all interfaces)                                                                                       | string   |          `"0.0.0.0"`          | `SERVER_ADDR`, `LISTEN_ADDR` |
| `--port="…"`                    | HTTP server port                                                                                                                                             | uint     |            `8080`             |         `HTTP_PORT`          |
| `--read-timeout="…"`            | maximum duration for reading the entire request, including the body (zero = no timeout)                                                                      | duration |            `1m0s`             |     `HTTP_READ_TIMEOUT`      |
| `--write-timeout="…"`           | maximum duration before timing out writes of the response (zero = no timeout)                                                                                | duration |            `1m0s`             |     `HTTP_WRITE_TIMEOUT`     |
| `--idle-timeout="…"`            | maximum amount of time to wait for the next request (keep-alive, zero = no timeout)                                                                          | duration |            `1m0s`             |     `HTTP_IDLE_TIMEOUT`      |
| `--storage-driver="…"`          | storage driver (sqlite/memory/redis/fs)                                                                                                                      | string   |          `"sqlite"`           |       `STORAGE_DRIVER`       |
| `--session-ttl="…"`             | session TTL (time-to-live, lifetime)                                                                                                                         | duration |          `168h0m0s`           |        `SESSION_TTL`         |
| `--max-requests="…"`            | maximal number of requests to store in the storage (zero means unlimited)                                                                                    | uint     |             `128`             |        `MAX_REQUESTS`        |
| `--fs-storage-dir="…"`          | path to the directory for local fs storage (directory must exist)                                                                                            | string   |                               |       `FS_STORAGE_DIR`       |
| `--sqlite-path="…"`             | path to the SQLite database file (created if absent; used by the sqlite storage driver)                                                                      | string   |    `"./webhook-tester.db"`    |        `SQLITE_PATH`         |
| `--auth-token="…"`              | shared token protecting the dashboard and /api endpoints (empty disables auth)                                                                               | string   |                               |         `AUTH_TOKEN`         |
| `--identifier-keys="…"`         | JSON body field and query-param names to extract as searchable identifiers                                                                                   | string   | `"trackingId", "referenceId"` |      `IDENTIFIER_KEYS`       |
| `--identifier-headers="…"`      | HTTP header names to extract as searchable identifiers                                                                                                       | string   |                               |     `IDENTIFIER_HEADERS`     |
| `--response-script-timeout="…"` | maximum execution time for a session response (go-template) script                                                                                           | duration |             `1s`              |  `RESPONSE_SCRIPT_TIMEOUT`   |
| `--hot-index-window="…"`        | retention window for the in-memory identifier hot index (search fast path)                                                                                   | duration |          `168h0m0s`           |      `HOT_INDEX_WINDOW`      |
| `--max-request-body-size="…"`   | maximal webhook request body size (in bytes), zero means unlimited                                                                                           | uint     |              `0`              |   `MAX_REQUEST_BODY_SIZE`    |
| `--auto-create-sessions`        | automatically create sessions for incoming requests                                                                                                          | bool     |            `false`            |    `AUTO_CREATE_SESSIONS`    |
| `--pubsub-driver="…"`           | pub/sub driver (memory/redis)                                                                                                                                | string   |          `"memory"`           |       `PUBSUB_DRIVER`        |
| `--tunnel-driver="…"`           | tunnel driver to expose your locally running app to the internet (ngrok, empty to disable)                                                                   | string   |                               |       `TUNNEL_DRIVER`        |
| `--ngrok-auth-token="…"`        | ngrok authentication token (required for ngrok tunnel; create a new one at https://dashboard.ngrok.com/authtokens/new)                                       | string   |                               |      `NGROK_AUTHTOKEN`       |
| `--public-url-root="…"`         | public URL root override for webhook URLs (e.g., http://webhook-tester.k8s.internal); if not set, the URL shown in the UI is based on the browser's location | string   |                               |      `PUBLIC_URL_ROOT`       |
| `--redis-dsn="…"`               | redis-like (redis, keydb) server DSN (e.g. redis://user:pwd@127.0.0.1:6379/0 or unix://user:pwd@/path/to/redis.sock?db=0)                                    | string   |  `"redis://127.0.0.1:6379/0"` |         `REDIS_DSN`          |
| `--shutdown-timeout="…"`        | maximum duration for graceful shutdown                                                                                                                       | duration |             `15s`             |      `SHUTDOWN_TIMEOUT`      |
| `--use-live-frontend`           | use frontend from the local directory instead of the embedded one (useful for development)                                                                   | bool     |            `false`            |            *none*            |

### `start healthcheck` subcommand (aliases: `hc`, `health`, `check`)

Health checker for the HTTP(S) servers. Use case - docker healthcheck.

Usage:

```bash
$ app [GLOBAL FLAGS] start healthcheck [COMMAND FLAGS] [ARGUMENTS...]
```

The following flags are supported:

| Name         | Description      | Type | Default value | Environment variables |
|--------------|------------------|------|:-------------:|:---------------------:|
| `--port="…"` | HTTP server port | uint |    `8080`     |      `HTTP_PORT`      |

<!--/GENERATED:CLI_DOCS-->

## 🙏 Credits & upstream

This is a fork of **[tarampampam/webhook-tester](https://github.com/tarampampam/webhook-tester)** by
[@tarampampam](https://github.com/tarampampam), which provides the Go core, the embedded React UI, the OpenAPI-driven
handler architecture, the pubsub→WebSocket live updates, the memory/Redis/fs storage drivers, and the ngrok tunnel
integration. All of that hard work belongs to the upstream project and its contributors — huge thanks.

The features under [What this fork adds](#-what-this-fork-adds) were built on top of that base for callback /
callback-integration debugging. The upstream demo is at [wh.tarampamp.am](https://wh.tarampamp.am/) (it does not include
this fork's additions).

## 🤖 AI Agent Instructions

See [AGENTS.md](AGENTS.md) for guidelines for AI agents working with this repository.

## License

This is open-sourced software licensed under the [MIT License](LICENSE), inherited from the upstream project.
