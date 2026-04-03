# Digits Signaling Server (`signald`)

Central WebRTC signaling relay for the Digits phone network. Brokers SDP/ICE exchange between phones so they can establish peer-to-peer audio connections. Audio never touches the server — it's purely a signaling relay.

## What It Does

- **Phone registration** — phones connect via WebSocket on boot and register their number
- **Call routing** — relays `ring`, `sdp`, `ice`, `answer`, `hangup` messages between peers
- **Phone directory** — web UI for managing registered phones (CRUD)
- **Call log** — persistent history of all calls with status and duration
- **Single binary** — templates embedded, no external file dependencies

## Build

```bash
cd server/
go build -o bin/signald ./cmd/signald/
```

Or via Makefile:

```bash
make build      # build binary
make run        # build and run
make test       # run tests
make clean      # remove build artifacts
```

Requires Go 1.22+.

## Run

```bash
./bin/signald
# signald listening on :8443
```

Visit `http://localhost:8443` for the web dashboard.

## Environment Variables

| Variable       | Default      | Description                        |
|----------------|--------------|------------------------------------|
| `SIGNALD_ADDR` | `:8443`      | HTTP/WebSocket listen address      |
| `SIGNALD_DB`   | `digits.db`  | SQLite database path               |

Example:

```bash
SIGNALD_ADDR=:9000 SIGNALD_DB=/var/lib/digits/digits.db ./bin/signald
```

## Web UI

| Route        | Description                           |
|--------------|---------------------------------------|
| `/`          | Dashboard — stats, active calls, recent history |
| `/phones`    | Phone directory — add/edit/delete phones |
| `/calls`     | Call log — full history, auto-refreshes |
| `/settings`  | Server info and protocol reference    |

The UI uses htmx for partial updates (no full page reloads on add/delete) and Tailwind CSS for styling, both via CDN. Dark theme throughout.

## WebSocket Protocol

Phones connect to `ws://<host>/ws` and exchange JSON messages.

```json
{
  "type": "register",
  "number": "3140001"
}
```

### Message Types

| Type       | Direction      | Fields              | Purpose                              |
|------------|----------------|---------------------|--------------------------------------|
| `register` | Phone → Server | `number`            | Register phone number on connect     |
| `call`     | Phone → Server | `to`                | Initiate call to another number      |
| `ring`     | Server → Phone | `from`              | Notify callee of incoming call       |
| `sdp`      | Bidirectional  | `to`, `from`, `sdp` | Relay SDP offer/answer               |
| `ice`      | Bidirectional  | `to`, `from`, `candidate` | Relay ICE candidates          |
| `answer`   | Phone → Server | `to`                | Callee accepts the call              |
| `hangup`   | Bidirectional  | `to`                | Either side hangs up                 |
| `busy`     | Server → Phone | —                   | Callee is already in a call          |
| `error`    | Server → Phone | `error`             | Error message                        |

### Connection Flow

```
Phone A                    Server                    Phone B
  |                           |                          |
  |-- register {number:A} --> |                          |
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

After SDP/ICE exchange, audio flows peer-to-peer via WebRTC DTLS-SRTP — the server is no longer in the media path.

## Architecture

```
cmd/signald/main.go          Entry point, wires components
internal/config/             Env-var config loading
internal/db/                 SQLite init and migrations
internal/directory/          Phone CRUD (Add/Get/List/Update/Delete)
internal/signaling/          WebSocket hub, relay, protocol types
internal/calls/              Call lifecycle tracking and history
internal/web/                HTTP handlers and embedded HTML templates
```

## Database Schema

SQLite database with three tables:

- **phones** — registered phone numbers with labels
- **calls** — call history with status, timestamps, duration
- **settings** — key/value server settings (future use)

## Notes

- No authentication — LAN-only, trusted network. Auth is a future hardening task.
- No TLS — add Let's Encrypt or self-signed certs when exposing beyond LAN.
- SQLite is appropriate for 2–10 phones. Not designed for thousands of concurrent phones.
