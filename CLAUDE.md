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
PICO_SDK_PATH=/path/to/pico-sdk ./scripts/build.sh
./scripts/flash.sh  # copies UF2 to mounted Pico
```

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

Conventional commits enforced by commitlint. Scope is required (warning if empty, error if invalid). **ALL commits must follow this format, including merge commits.** Do not use bare `merge:` or `Merge branch` messages. Use `chore(<scope>): merge ...` instead.

Valid scopes: `pi`, `digitsd`, `firmware`, `server`, `image`, `docs`, `ci`

Examples:
```
fix(server): handle NULL line_id in device lookup
feat(digitsd): add volume service code
docs: update wiring notes
chore(server): merge main into feature branch
```

## Git workflow

Remote: **github** (git@github.com:justinlindh/digits.git)

PRs required to merge into main. No direct pushes. Use the PR template at `.github/pull_request_template.md`.

**Never force push.** Always push new commits on top of existing branches. No `--force`, no `--force-with-lease`, no amending pushed commits.

When addressing PR review comments, always reply to each comment on GitHub (via `gh api`) acknowledging the feedback and noting what was changed. Do this at the same time as pushing the fix.

## CI

GitHub Actions workflows (`.github/workflows/`):
- **commitlint** -- validates commit messages on PRs
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
