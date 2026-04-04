# Digits Signaling Server (`signald`)

Central WebRTC signaling relay for the Digits phone network. Brokers SDP/ICE exchange between phones so they can establish peer-to-peer encrypted audio connections. Audio never touches the server -- it is purely a signaling and management layer.

## What It Does

- **Call signaling** -- relays `ring`, `sdp`, `ice`, `answer`, `hangup` messages between phones via WebSocket
- **Household management** -- users belong to households; phones within the same or linked households can call each other
- **Device pairing** -- pairs physical phone hardware to a phone number via time-limited pairing codes
- **Authentication** -- magic link emails and optional Google OAuth with session cookies
- **Phone directory** -- web UI for managing registered phone numbers (CRUD)
- **Call history** -- persistent log of all calls with status and duration
- **Device updates** -- trigger and track OTA firmware/Pi updates on paired devices
- **TURN credentials** -- optional HMAC-SHA1 credential generation for NAT traversal
- **Admin panel** -- separate service (`admind`) for system-wide monitoring and management
- **Single binary** -- templates and static assets embedded, no external file dependencies

## Build

```bash
cd server/
make build          # build bin/signald with version/commit info
make build-dev      # build without ldflags (faster)
make run            # build and run
make test           # go test ./...
make css            # compile Tailwind CSS for web and admin templates
make e2e            # Playwright end-to-end tests (needs running server)
make clean          # remove build artifacts
```

Requires Go 1.26+.

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

### Admin Panel

| Variable               | Description                                |
|------------------------|--------------------------------------------|
| `ADMIN_DATABASE_URL`   | Postgres connection string for admin DB    |
| `ADMIN_ADDR`           | Admin listen address (default `:9090`)     |
| `ADMIN_INITIAL_USER`   | Bootstrap admin username                   |
| `ADMIN_INITIAL_SECRET` | Bootstrap admin password                   |
| `ADMIN_STATS_URL`      | Stats API URL (points to signald)          |
| `ADMIN_STATS_SECRET`   | Must match `ADMIN_SECRET`                  |

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
| `/`                         | Dashboard -- stats, active calls, recent history |
| `/phones`                   | Phone directory -- add, pair, manage phones  |
| `/phones/{number}`          | Phone detail -- device info, update status   |
| `/phones/{number}/edit`     | Edit phone name                              |
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

The UI uses htmx for partial updates and Tailwind CSS for styling. Dark theme throughout.

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
| `device_info`     | Phone -> Server | version fields, `flash_capable`      | Report device version info           |
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
cmd/admind/              Admin panel entry point

internal/
  admin/                 Admin panel: server, auth, DB, stats dashboard
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

## Notes

- Authentication is required -- magic link emails or Google OAuth.
- Rate limiting is applied to auth and pairing endpoints.
- The admin panel runs as a separate process on port 9090 with its own database and auth.
- TURN credentials are time-limited (24h) and use HMAC-SHA1.
