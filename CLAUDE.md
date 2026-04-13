# Digits

Private encrypted phone network built from gutted vintage desk phones. Three components talk to each other: firmware on a Pico H, a Go daemon on a Pi Zero 2 W, and a Go signaling server.

## Repo layout

```
firmware/       Pico H firmware (C, CMake, Pico SDK)
pi/digitsd/     Pi-side daemon (Go, cross-compiled to arm64)
pi/digits-setup/ First-boot setup tool (Go)
pi/image/       Pi OS image builder
server/         Signaling server + web UI (Go, htmx, Tailwind)
tools/          Build scripts for firmware, Pi binaries, and OS images
scripts/        Firmware build/flash helpers
docs/           Hardware notes, protocol specs, architecture
```

## Prerequisites

- **Go 1.26+**
- **PostgreSQL** -- the server reads `DATABASE_URL` to connect. Migrations run automatically on startup (inline in `internal/db/db.go`).
- **Docker** (for digitsd cross-compile): `make build` uses a Docker container, no host cross-compile toolchain needed
- **Pico SDK** (for firmware): set `PICO_SDK_PATH` to your checkout

## Build and test

Server (from `server/`):
```
make build          # builds bin/signald
make run            # build + run the server
make test           # go test ./...
make e2e            # Playwright tests (needs running server via make run)
go vet ./...
```

digitsd (from `pi/digitsd/`):
```
make build          # cross-compiles to linux/arm64 via Docker
make build-local    # native build (requires local cross-compile libs)
make test
```

Firmware (from repo root):
```
make firmware         # builds in Docker, no host toolchain needed
make firmware-local   # builds on host (requires arm-none-eabi-gcc + Pico SDK)
./scripts/flash.sh    # copies UF2 to mounted Pico
```

`make firmware-local` runs `./scripts/build.sh`, which auto-detects `PICO_SDK_PATH` from common locations (`/usr/share/pico-sdk`, `~/pico-sdk`, etc.). Only set `PICO_SDK_PATH` explicitly if the SDK is installed somewhere non-standard.

## Linting

golangci-lint v2 with `standard` defaults. Config is in `.golangci.yml` at repo root.

Run manually (from each module directory):
```
golangci-lint run ./...
```

Pre-commit hook (opt in once per checkout):
```
git config core.hooksPath .githooks
```

## Testing

The Go test suite is split into two tiers. Pick the right tier when you write a new test; do not mix.

**Unit tests** run in-process with no external services. They must not require Postgres, Redis, a real HTTP backend, a real file system beyond `t.TempDir`, or a network. `httptest.Server`, `httptest.ResponseRecorder`, and in-memory fakes are fine. Unit tests are the default: no build tag, no naming convention required.

**Integration tests** require at least one real external dependency (today: PostgreSQL). They are gated by a build tag so they don't compile into the default unit binary:

```go
//go:build integration

package foo
```

The blank line between the build tag and the `package` declaration is required by `go build` syntax. Integration test files should also use the `_integration_test.go` suffix for new files (e.g., `store_integration_test.go`). Existing files use less consistent names but all carry the build tag.

**End-to-end tests** are the Playwright suite in `server/internal/web/e2e/` driven by `make e2e` from `server/`. They spin up the full stack (signald + admind + Postgres via `docker compose`) and drive a real browser. They are outside the Go test runner and have their own invocation.

### Running tests locally

From `server/`:

```
go test ./...                                   # unit only (fast, default)
go test -tags=integration ./...                 # unit + integration (needs Postgres)
make e2e                                        # playwright e2e (needs running stack)
```

Integration tests require two environment variables pointing at live databases:

```
export TEST_DATABASE_URL="postgres://digits:digits@localhost:5432/digits_test?sslmode=disable"
export TEST_ADMIN_DATABASE_URL="postgres://digits:digits@localhost:5433/digits_admin_test?sslmode=disable"
```

If either variable is unset, the tests that require it call `t.Skip` and log the reason. That means `go test -tags=integration ./...` without a DSN is safe -- it just skips anything it can't run.

### CI

- **`.github/workflows/server-ci.yml`** -- the unit job. Runs on every push and every PR. Fast (seconds). Required for merge.
- **`.github/workflows/server-integration.yml`** -- the integration job. Runs on PRs targeting main and on pushes to main. Provisions Postgres service containers for both databases and runs `go test -tags=integration`. Required for merge to main.

The unit job must stay fast. If you find yourself adding a Postgres dependency to something that could be tested in-process, the right move is to restructure the code (extract a pure function, use an interface, inject a fake) rather than reach for the integration tag.

## Production deployment

The server runs on the GPU box via Docker Compose. All commands from `server/`:

```
docker compose -f docker-compose.prod.yml --env-file .env.prod up -d      # start
docker compose -f docker-compose.prod.yml --env-file .env.prod down        # stop
docker compose -f docker-compose.prod.yml --env-file .env.prod logs -f     # tail logs
```

The project name is `digits-prod` (set via `COMPOSE_PROJECT_NAME` in `.env.prod`). Do not omit `--env-file .env.prod` or use a bare `docker compose up` -- that creates a separate `server-*` set of containers and volumes, disconnected from production data.

Services auto-start on reboot (`restart: unless-stopped`, Docker enabled at boot).

## Commit conventions

Conventional commit format. Scope is required.

PR titles must use this format (they become the squash commit message on merge). Individual commits on feature branches don't need to.

Valid scopes: `pi`, `digitsd`, `firmware`, `server`, `image`, `docs`, `ci`

Examples:
```
fix(server): handle NULL line_id in device lookup
feat(digitsd): add volume service code
docs: update wiring notes
```

## Git workflow

Remote: **github** (git@github.com:justinlindh/digits.git)

PRs required to merge into main. No direct pushes. Use the PR template at `.github/pull_request_template.md`.

**Never force push.** Always push new commits on top of existing branches. No `--force`, no `--force-with-lease`, no amending pushed commits.

When addressing PR review comments, always reply to each comment on GitHub (via `gh api`) acknowledging the feedback and noting what was changed. Do this at the same time as pushing the fix.

## CI

GitHub Actions workflows (`.github/workflows/`):
- **server-ci** -- build, test, vet (triggers on `server/` changes)
- **fw-release / pi-release / server-release** -- tag-triggered release pipelines

## Style

- Go: standard project layout, raw SQL with `database/sql` (no ORM), errors returned not panicked
- Server web UI: htmx + Tailwind, templates in `internal/web/templates/`
- Firmware: C with Pico SDK conventions
- Never use em dashes in any written copy
- Never add Co-Authored-By trailers to commits
- Never include Claude Code session links (claude.ai/code/session_*) in commits, PRs, or any other content
- Don't leave "was removed" or "was here" comments when deleting code; just delete it
