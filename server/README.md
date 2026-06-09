# Digits Signaling Server (`signald`)

Central WebRTC signaling relay for the Digits phone network. Brokers SDP/ICE exchange between phones so they can establish peer-to-peer encrypted audio connections. Audio never touches the server -- it is purely a signaling and management layer.

## What It Does

- **Call signaling** -- relays `ring`, `sdp`, `ice`, `answer`, `hangup`, and `call_return` messages between phones via WebSocket
- **Household management** -- users belong to households; phones within the same or linked households can call each other
- **Device pairing** -- pairs physical phone hardware to a phone number via time-limited pairing codes
- **Authentication** -- magic link emails and optional Google OAuth with session cookies
- **Phone directory** -- web UI for managing registered phone numbers (CRUD)
- **Call history** -- persistent log of all calls with status and duration
- **Device updates** -- trigger and track OTA firmware/Pi updates on paired devices
- **TURN credentials** -- optional HMAC-SHA1 credential generation for NAT traversal
- **Single binary** -- templates and static assets embedded, no external file dependencies

## Build

```bash
cd server/
make build          # build bin/signald with version/commit info
make build-dev      # build without ldflags (faster)
make run            # build and run
make test           # go test ./...
make e2e            # Playwright end-to-end tests (needs running server)
make clean          # remove build artifacts
```

Requires Go 1.26+.

## Local development

For iterating on the web UI you can spin up a disposable Postgres in Docker, seed a signed-in dialup-themed user, and run host-native `signald` against it with one command.

```bash
cd server/
make dev-up         # start local Postgres, seed dev user, run signald
make dev-seed       # re-seed the dev user (no-op if already present)
make dev-down       # stop signald, remove DB container + volume
make dev-logs       # tail the user-db container logs
```

`make dev-up` runs `signald` in the foreground. Stop it with Ctrl+C, then run `make dev-down` to remove the Docker volume and wipe state. Running `make dev-up` again gives a clean slate.

When the server prints `server started addr=:8080`, open the URL that `dev-seed` printed in your browser:

```
http://localhost:8080/auth/dev-session?email=dev@digits.local
```

That endpoint is only mounted when `DEV_MODE=true`. It sets a session cookie and redirects to the dialup-themed dashboard. The app ships three themes: `intercom` (default), `dialup`, and `answering-machine`; switch at `/settings/theme`.

### Config

Defaults live in `.env.dev.example` (committed). To override, copy it to `.env.dev` (gitignored):

```bash
cp .env.dev.example .env.dev
```

Common overrides:

| Variable         | Default                                                        | Notes                                          |
|------------------|----------------------------------------------------------------|------------------------------------------------|
| `SIGNALD_ADDR`   | `:8080`                                                        | Change if port 8080 is taken on your machine.  |
| `BASE_URL`       | `http://localhost:8080`                                        | Must match `SIGNALD_ADDR`.                     |
| `DEV_DB_PORT`    | `5433`                                                         | Host port for the dev Postgres container.      |
| `DATABASE_URL`   | `postgres://digits:digits@127.0.0.1:5433/digits?sslmode=disable` | Update the port if you changed `DEV_DB_PORT`. |
| `DEV_SEED_EMAIL` | `dev@digits.local`                                             | Email for the seeded user.                     |

The user-db container exposes `5433` on `127.0.0.1` only, so host-native `signald` can reach it without opening the port to the network.

Prod uses a separate compose file (`docker-compose.prod.yml`) and is unaffected by these dev targets.

## Run

```bash
DATABASE_URL=postgres://... ./bin/signald
# signald listening on :8443
```

Visit `http://localhost:8443` for the web UI. New users are prompted to create a household on first login.

## Environment Variables

### Core

| Variable       | Default                          | Description                           |
|----------------|----------------------------------|---------------------------------------|
| `SIGNALD_ADDR` | `:8443`                          | HTTP/WebSocket listen address         |
| `DATABASE_URL` | (required)                       | Postgres connection string            |
| `BASE_URL`     | `https://app.digits.family`      | Public base URL for links and OAuth   |
| `ADMIN_SECRET` | (required)                       | Shared secret for internal stats API  |

### TLS (optional)

| Variable           | Description            |
|--------------------|------------------------|
| `SIGNALD_TLS_CERT` | TLS certificate path   |
| `SIGNALD_TLS_KEY`  | TLS key path           |

### TURN Server (optional)

| Variable               | Description                                |
|------------------------|--------------------------------------------|
| `SIGNALD_TURN_ENABLED` | Set to `true` to enable                    |
| `SIGNALD_TURN_SECRET`  | HMAC shared secret for credential generation |
| `SIGNALD_TURN_DOMAIN`  | TURN server domain (e.g. `turn.example.com`) |

### Authentication

| Variable               | Description                                       |
|------------------------|---------------------------------------------------|
| `GOOGLE_CLIENT_ID`     | Google OAuth client ID (optional)                 |
| `GOOGLE_CLIENT_SECRET` | Google OAuth client secret (optional)             |
| `GOOGLE_REDIRECT_URL`  | OAuth callback URL (must match Google Console)    |
| `COOKIE_DOMAIN`        | Cookie domain for subdomain sharing               |

### Email (SMTP)

| Variable    | Default                  | Description                |
|-------------|--------------------------|----------------------------|
| `SMTP_HOST` |                          | SMTP server hostname       |
| `SMTP_PORT` | `587`                    | SMTP server port           |
| `SMTP_USER` |                          | SMTP username              |
| `SMTP_PASS` |                          | SMTP password              |
| `SMTP_FROM` | `noreply@digits.family`  | From address on emails     |

### Metrics

| Variable               | Description                                |
|------------------------|--------------------------------------------|
| `SIGNALD_METRICS_ADDR` | Prometheus metrics listener (default `:9091`, empty disables) |

The `/metrics` endpoint runs on a separate listener from public traffic.
The metric set is deliberately privacy-respecting (aggregate counters,
no per-user or per-call data, no caller/callee identifiers). See
[`docs/metrics.md`](docs/metrics.md) for the full list and the rules
around what is and is not collected.

### Multi-replica signaling

| Variable                | Description                                                    |
|-------------------------|----------------------------------------------------------------|
| `REDIS_URL`             | Redis connection URL (`redis://host:port` or `rediss://...`), or a comma-separated list of Sentinel addresses for failover mode. When set, signald enables both cross-pod signaling (pub/sub) and shared cluster state (device presence, active calls and conferences, dashboard SSE events). Empty disables Redis (single-instance mode). |
| `REDIS_SENTINEL_MASTER` | Sentinel master name. When set together with a comma-separated `REDIS_URL`, the client switches to failover-aware mode. Leave empty for a direct connection. |

Without `REDIS_URL`, behavior is identical to the single-instance default
and Redis is not a runtime dependency. With it, every pod publishes to a
shared `digits:signal` channel when a local lookup misses, subscribing pods
deliver to their local connections, and the following cluster state moves
into Redis so multi-pod queries see a consistent view:

- **Device presence:** a pod records the device-to-pod mapping for each
  connected device, and other pods read it to route calls to the owning pod.
- **Active calls and conferences:** the call tracker writes per-call and
  per-conference records (with a 30-minute safety-net TTL) so any pod can
  answer "is this number busy?" or "what calls are live?".
- **Dashboard events:** the SSE broadcaster fans out across pods via Redis
  pub/sub so `/api/dashboard/stream` re-renders counters regardless of which
  pod the SSE client connected to.

Running multiple replicas without Redis silently breaks all of these:
calls land on the wrong pod, devices appear offline to other pods, and
dashboard counters reflect only the local pod's events.

### Tracing and Profiling

| Variable                       | Description                                |
|--------------------------------|--------------------------------------------|
| `OTEL_EXPORTER_OTLP_ENDPOINT`  | OTLP collector host:port. Empty disables the trace exporter. |
| `OTEL_EXPORTER_OTLP_PROTOCOL`  | `grpc` (default) or `http`.                |
| `OTEL_EXPORTER_OTLP_INSECURE`  | `false` to require TLS. Defaults to `true` for in-cluster collectors. |
| `OTEL_TRACES_SAMPLER_ARG`      | Head-based sample ratio, 0..1. Default `1.0`. |
| `OTEL_RESOURCE_ATTRIBUTES`     | `k=v[,k=v...]` resource attributes.        |
| `OTEL_SERVICE_NAME`            | Override service name; the binary supplies `signald` by default. |
| `PYROSCOPE_SERVER_ADDRESS`     | Pyroscope HTTP ingest URL. Empty disables the profiler. |
| `PYROSCOPE_AUTH_TOKEN`         | Bearer token for hosted Pyroscope. Empty for in-cluster.  |
| `PYROSCOPE_TENANT_ID`          | `X-Scope-OrgID` for multi-tenant Pyroscope. |
| `DEPLOYMENT_ENV`               | Operator-supplied environment tag (e.g. `k8s`, `docker`). |

OpenTelemetry traces and Pyroscope continuous profiles share the
privacy posture documented in `docs/metrics.md`: span attributes,
events, and profile labels are a closed set, with HTTP routes bucketed
through the same `metrics.RouteOf` helper so phone numbers, call IDs,
magic-link tokens, and household IDs never reach a span. The W3C Trace
Context propagator is installed unconditionally, so leaving the
exporter off still propagates inbound `traceparent` headers; turning
exporters on later requires no code change. See
[`docs/tracing.md`](docs/tracing.md) for the full attribute list and
the rules around what is and is not collected.

### Development

| Variable     | Description                                      |
|--------------|--------------------------------------------------|
| `DEV_MODE`   | Set to `true` to log magic link URLs to stdout   |
| `LOG_FORMAT` | Set to `json` for structured JSON logging        |

### GitHub Releases (optional)

| Variable       | Description                                |
|----------------|--------------------------------------------|
| `GITHUB_REPO`  | Repo in `owner/repo` format for updates    |
| `GITHUB_TOKEN` | GitHub personal access token (optional)    |

See `.env.example` for a starter config file.

## Web UI

### Authenticated Routes

| Route                       | Description                                  |
|-----------------------------|----------------------------------------------|
| `/`                         | Overview -- line list, active calls, recent history |
| `/phones`                   | Pair a handset (button-launched from the Overview) |
| `/phones/{number}`          | Phone detail -- device info, settings, update status |
| `/calls`                    | Call history -- full log, auto-refreshes     |
| `/settings`                 | Household settings, call history toggle      |
| `/links`                    | Household links -- invite and connect households |
| `/onboard`                  | First-login household creation               |

### Auth Routes

| Route                     | Description               |
|---------------------------|---------------------------|
| `/auth/login`             | Login page                |
| `POST /auth/magic`        | Request magic link email  |
| `/auth/magic/{token}`     | Verify magic link         |
| `/auth/google/login`      | Google OAuth login        |
| `/auth/google/callback`   | Google OAuth callback     |
| `POST /auth/logout`       | Log out                   |

### API Routes

| Route                   | Description                              |
|-------------------------|------------------------------------------|
| `/healthz`              | Health check                             |
| `/ws`                   | WebSocket endpoint for phone signaling   |
| `/api/version`          | Server version info                      |
| `/api/status`           | Current call status                      |
| `/api/active-calls`     | Active calls list                        |
| `/internal/stats`       | Internal stats (requires `ADMIN_SECRET`) |

The UI uses htmx for partial updates and Tailwind CSS for styling. Three themes ship: `intercom` (default), `dialup`, and `answering-machine`; the per-user choice lives on `users.theme`.

## WebSocket Protocol

Phones connect to `ws://<host>/ws` and exchange JSON messages.

### Connection Flow

```
Phone A                    Server                    Phone B
  |                           |                          |
  |-- register {number:A} --> |                          |
  |-- device_info {...}    --> |                          |
  |                           | <-- register {number:B} --|
  |                           |                          |
  |-- call {to:B} ----------> |                          |
  |                           |-- ring {from:A} -------> |
  |                           |                          |
  |                           | <-- answer {to:A} -------|
  |<-- answer {from:B} -------|                          |
  |                           |                          |
  |-- sdp {to:B, sdp:...} --> |-- sdp {from:A, ...} --> |
  |<-- sdp {from:B, ...} -----|<-- sdp {to:A, ...} -----|
  |                           |                          |
  |-- ice {to:B, ...} ------> |-- ice {from:A, ...} --> |
  |                           |                          |
  |-- hangup {to:B} --------> |-- hangup {from:A} ----> |
```

After SDP/ICE exchange, audio flows peer-to-peer via WebRTC DTLS-SRTP.

### Message Types

| Type              | Direction      | Key Fields                            | Purpose                              |
|-------------------|----------------|---------------------------------------|--------------------------------------|
| `register`        | Phone -> Server | `number`                             | Register phone number on connect     |
| `device_info`     | Phone -> Server | version fields                       | Report device version info           |
| `call`            | Phone -> Server | `to`                                 | Initiate call to another number      |
| `ring`            | Server -> Phone | `from`                               | Notify callee of incoming call       |
| `sdp`             | Bidirectional  | `to`, `from`, `sdp`                  | Relay SDP offer/answer               |
| `ice`             | Bidirectional  | `to`, `from`, `candidate`            | Relay ICE candidates                 |
| `answer`          | Phone -> Server | `to`                                 | Callee accepts the call              |
| `hangup`          | Bidirectional  | `to`                                 | Either side hangs up                 |
| `busy`            | Server -> Phone | --                                   | Callee is already in a call          |
| `error`           | Server -> Phone | `error`                              | Error message                        |
| `request-ice-servers` | Phone -> Server | --                              | Request TURN/STUN credentials        |
| `ice-servers`     | Server -> Phone | `servers`                            | TURN/STUN server list with credentials |
| `pairing_code`    | Phone -> Server | `pairing_code`, `hardware_id`        | Device presents pairing code         |
| `paired`          | Server -> Phone | `device_token`                       | Device successfully paired           |
| `update_trigger`  | Server -> Phone | `target_pi_version`, `target_fw_version` | Trigger OTA update on device     |
| `update_status`   | Phone -> Server | `update_status`, `update_detail`     | Report update progress               |
| `ice_restart`     | Bidirectional  | `to`, `from`, `sdp`                  | ICE restart offer with new credentials |

## Architecture

```
cmd/signald/             Entry point -- wires all components

internal/
  auth/                  User authentication (magic links, Google OAuth, sessions)
  calls/                 Call lifecycle tracking and history
  config/                Environment variable config loading
  db/                    Postgres connection and schema migrations
  dbutil/                Database utilities
  device/                Physical device persistence and pairing
  email/                 SMTP email sender (magic link delivery)
  household/             Household CRUD, membership, onboarding
  httputil/              HTTP helpers (healthz)
  line/                  Phone number CRUD, call authorization
  logging/               Structured logging setup (slog + tint)
  pairing/               Pairing code generation and claim flow
  ratelimit/             IP-based token bucket rate limiter
  signaling/             WebSocket hub, message relay, protocol types
  turn/                  TURN credential generation (HMAC-SHA1)
  updates/               GitHub release index fetching and caching
  version/               Build-time version info
  web/                   HTTP routes, handlers, embedded templates
```

## Database

PostgreSQL with auto-running migrations on startup. Core tables:

- **users** / **sessions** / **magic_links** -- authentication
- **households** / **household_members** -- family groups and membership
- **household_links** -- connections between households (invite/accept/revoke)
- **lines** -- phone numbers belonging to a household
- **devices** -- physical hardware, pairing codes, device tokens, firmware versions
- **calls** -- call history with status, timestamps, duration

Call authorization: phones can call each other if their lines belong to the same household or to linked households.

## Docker

The server includes Docker support via `docker-compose.yml` and `Dockerfile`. See the `deploy` Makefile target for production deployment.

## Autodeploy

The prod GPU box runs a systemd timer (`digits-autodeploy.timer`) that fires every 60 seconds and invokes `/usr/local/bin/autodeploy`. Each run:

1. Asks the GitHub Releases API for the newest `server/v*` tag.
2. If it matches `last_deployed_tag` in `/var/lib/digits-autodeploy/state.json`, exits immediately.
3. Otherwise logs in to GHCR, pulls `ghcr.io/justinlindh/digits/signald:vX.Y.Z`, and runs `docker compose up -d --wait signald`.
4. Polls `/healthz` on both services for a version match (timeout 90s).
5. On success: updates state, exits 0.
6. On failure: re-pins compose to the previous version, sends an SMTP alert, and exits 1.

Spin-protection: a tag that already failed once is skipped until a newer tag appears or `autodeploy --retry` is run.

### First-time install

```
sudo GHCR_TOKEN=<pat> SMTP_PASS=<pass> server/scripts/install-autodeploy.sh
```

Edit `/etc/digits-autodeploy/config.env` if any values are missing, then re-run the installer (idempotent).

### Upgrade autodeploy itself

From a fresh checkout on the prod box:

```
sudo server/scripts/install-autodeploy.sh
```

The installer rebuilds the binary, preserves config/state, and reloads systemd units.

### Useful commands

```
systemctl status digits-autodeploy.service
systemctl list-timers digits-autodeploy.timer
journalctl -u digits-autodeploy.service -n 200
sudo /usr/local/bin/autodeploy --dry-run
sudo /usr/local/bin/autodeploy --retry    # clear spin-protection before the next tick
sudo cat /var/lib/digits-autodeploy/state.json
```

### Manual deploy (escape hatch)

`server/deploy.sh` still works. It now pulls from GHCR by default. To deploy a commit that has not been released yet, pass `--local-build`:

```
cd ~/src/digits/server && ./deploy.sh --local-build signald
```

## Notes

- Authentication is required -- magic link emails or Google OAuth.
- Rate limiting is applied to auth and pairing endpoints.
- TURN credentials are time-limited (24h) and use HMAC-SHA1.
