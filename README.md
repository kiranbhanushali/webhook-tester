<p align="center">
  <a href="https://github.com/tarampampam/webhook-tester#readme">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset="https://socialify.git.ci/tarampampam/webhook-tester/image?description=1&font=Raleway&forks=1&issues=1&logo=https%3A%2F%2Fgithub.com%2Fuser-attachments%2Fassets%2Fe2e659dc-7fb1-4ac2-ad3c-883899f5fc38&owner=1&pulls=1&pattern=Solid&stargazers=1&theme=Dark">
      <img align="center" src="https://socialify.git.ci/tarampampam/webhook-tester/image?description=1&font=Raleway&forks=1&issues=1&logo=https%3A%2F%2Fgithub.com%2Fuser-attachments%2Fassets%2Fe2e659dc-7fb1-4ac2-ad3c-883899f5fc38&owner=1&pulls=1&pattern=Solid&stargazers=1&theme=Light">
    </picture>
  </a>
</p>

# WebHook Tester

This application allows you to test and debug webhooks and HTTP requests using unique, randomly generated URLs. You
can customize the response code, `Content-Type` HTTP header, response content, and even set a delay for responses.

Consider it a free and self-hosted alternative to [webhook.site](https://github.com/fredsted/webhook.site),
[requestinspector.com](https://requestinspector.com/), and similar services.

<p align="center">
  <img src="https://github.com/user-attachments/assets/26e56d78-8a10-4883-9052-d18047206fda" alt="screencast" />
</p>

> [!TIP]
> The demo is available at [wh.tarampamp.am](https://wh.tarampamp.am/). Please note that it is quite limited,
> does not persist data, and may be unavailable sometimes, but feel free to try it.

Built with Go for high performance, this application includes a lightweight UI (written in `ReactJS`) that’s compiled
into the binary, so no additional assets are required. WebSocket support provides real-time webhook notifications in
the UI - no need for third-party solutions like `pusher.com`!

### 🔥 Features list

- Standalone operation with no third-party dependencies needed (persistent **SQLite** storage by default; memory/Redis/fs also available)
- Fully customizable response code, headers, and body for webhooks
- **Dynamic response templates** (Go `text/template`) with signing helpers (e.g. `hmacSHA256`) and an `@status` directive
- **Human-readable session slugs** and **groups**, plus optional **long-lived** (non-expiring) sessions
- **Identifier search** across captured requests (e.g. by `trackingId` / `referenceId`), index-backed on SQLite
- **FIFO events API** with a durable offset cursor for no-skip / no-duplicate incremental polling
- **Replay** any captured request to a target URL (or the session's forward URL)
- **Per-session security headers** applied to every response, and an optional **shared-token auth** for the dashboard/API
- Option to expose your locally running instance to the global internet (via tunneling)
- Fast, built-in UI based on `ReactJS`
- Multi-architecture Docker image based on `scratch`
- Runs as an unprivileged user in Docker
- Well-tested, documented source code
- CLI health check sub-command included
- Binary view of recorded requests in UI
- Supports JSON and human-readable logging formats
- Liveness probes (`/healthz` endpoint)
- Customizable webhook responses
- Built-in WebSocket support
- Efficient in memory and CPU usage
- Free, open-source, and scalable

### 🗃 Storage

The app supports 4 storage drivers: **sqlite** (default), **memory**, **Redis** and **fs** (configured with the
`--storage-driver` flag).

- **SQLite** driver (**default**): A single embedded database file (`--sqlite-path`, default `./webhook-tester.db`)
  using the pure-Go `modernc.org/sqlite` engine (no CGO). Data survives restarts and, crucially, identifier search
  (see below) is **fully indexed**, so it stays fast over long histories. This is the recommended driver for the
  callback-debugging workflow.
- **Memory** driver: Ideal for local debugging when persistent storage isn’t needed, as recorded requests are cleared
  upon app shutdown. Identifier search falls back to a non-indexed scan.
- **Redis** driver: Retains data across app restarts, suitable for environments where data persistence is required.
  Redis is also necessary when running multiple instances behind a load balancer.
- **FS** driver: Keep all the data in the local filesystem, useful when you need to store data between app restarts.

> [!NOTE]
> When running the Docker image with the sqlite driver, point `SQLITE_PATH` at a writable, mounted volume (e.g.
> `-e SQLITE_PATH=/data/wh.db -v "$(pwd)/.wh-data:/data"`); the image’s root filesystem is read-only.

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

Beyond the basic capture-and-inspect flow, this build adds a set of capabilities aimed at debugging real,
long-running callback integrations (capturing provider callbacks, finding a specific transaction, signing
responses, and re-delivering a captured request). All of the dashboard/data endpoints below live under `/api`
and are protected by the optional shared token (see [Authentication](#-authentication)); the webhook-capture
path `/w/...` is always public.

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
  "events":      [ { "seq": 1, "uuid": "…", "method": "POST", "request_payload_base64": "…", "headers": […], "url": "…", "captured_at_unix_milli": … }, … ],
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
result: `status_code`, `headers` and `body_base64`.

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

### 🔐 Authentication

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

## ⁉ FAQ

**Can I have pre-defined (static) webhook URLs (sessions) to ensure that the sent request will be captured even
without data persistence?**

Yes, simply use the `--auto-create-sessions` flag or set the `AUTO_CREATE_SESSIONS=true` environment variable. In
`v1`, you needed to define sessions during app startup to enable this functionality. However, since `v2`, all you
need to do is enable this feature. It works quite simply - if the incoming request contains a UUID-formatted prefix
(e.g., `http://app/11111111-2222-3333-4444-555555555555/...`), a session for this request will be created
automatically. All that's left for you to do is open the session in the UI
(`http://app/s/11111111-2222-3333-4444-555555555555`).

## 🧩 Installation

Download the latest binary for your architecture from the [releases page][link_releases]. For example, to install
on an **amd64** system (e.g., Debian, Ubuntu):

[link_releases]:https://github.com/tarampampam/webhook-tester/releases

```shell
curl -SsL -o ./webhook-tester https://github.com/tarampampam/webhook-tester/releases/latest/download/webhook-tester-linux-amd64
chmod +x ./webhook-tester
./webhook-tester start
```

> [!TIP]
> Each release includes binaries for **linux**, **darwin** (macOS) and **windows** (`amd64` and `arm64` architectures).
> You can download the binary for your system from the [releases page][link_releases] (section `Assets`). And - yes,
> all what you need is just download and run single binary file.

Alternatively, you can use the Docker image:

| Registry                               | Image                                |
|----------------------------------------|--------------------------------------|
| [GitHub Container Registry][link_ghcr] | `ghcr.io/tarampampam/webhook-tester` |
| [Docker Hub][link_docker_hub] (mirror) | `tarampampam/webhook-tester`         |

> [!NOTE]
> It’s recommended to avoid using the `latest` tag, as **major** upgrades may include breaking changes.
> Instead, use specific tags in `X.Y.Z` format for version consistency.

To install it on Kubernetes (K8s), please use the Helm chart from [ArtifactHUB][artifact-hub].

[artifact-hub]:https://artifacthub.io/packages/helm/webhook-tester/webhook-tester

## ⚙ Usage

The easiest way to run the app is by using the Docker image:

```shell
docker run --rm -t -p "8080:8080/tcp" ghcr.io/tarampampam/webhook-tester:2
```

> [!NOTE]
> This command starts the app with the default configuration on port `8080` (the first port in the `-p` argument is
> the host port, and the second is the application port inside the container).

Next, open your browser at [`localhost:8080`](http://localhost:8080) to begin testing your webhooks. To stop the app, press `Ctrl+C` in
the terminal where it's running.

For a **persistent + token-protected** instance (sqlite database on a mounted volume, dashboard/API behind a shared
token, webhook path still public):

```shell
docker run --rm -t -p "8080:8080/tcp" \
  -e AUTH_TOKEN=secret-token \
  -e STORAGE_DRIVER=sqlite \
  -e SQLITE_PATH=/data/wh.db \
  -v "$(pwd)/.wh-data:/data" \
  ghcr.io/tarampampam/webhook-tester:2
```

For custom configuration options, refer to the CLI help below or execute the app with the `--help` flag.

[link_ghcr]:https://github.com/users/tarampampam/packages/container/package/webhook-tester
[link_docker_hub]:https://hub.docker.com/r/tarampampam/webhook-tester/

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

## 🧠 A note on AI-assisted development

AI tools are great assistants - they can autocomplete, review, summarize, and help you move faster. But they’re not a
substitute for understanding what's going on. If you're using AI to contribute here, please make sure you actually
read, understand, and stand behind the changes you’re proposing.

I personally write my code myself, and I encourage others to do the same. Not because AI is "bad", but because blindly
trusting generated code tends to produce... let's say creative results.

And honestly, I'm still waiting for the day "AI-free software" becomes a trend - like organic food, but for code 😄 
Until then: trust, but verify.

## 🤖 AI Agent Instructions

See [AGENTS.md](AGENTS.md) for detailed guidelines for AI agents working with this repository.

## License

This is open-sourced software licensed under the [MIT License][link_license].

[link_license]:https://github.com/tarampampam/webhook-tester/blob/master/LICENSE
