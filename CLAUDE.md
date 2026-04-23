# Digits

Private encrypted phone network built from gutted vintage desk phones. Three components talk to each other: firmware on a Pico H, a Go daemon on a Pi Zero 2 W, and a Go signaling server.

## Hard rules

These override default behavior and apply to every output: chat, code, commits, PRs, issues, GitHub comments, in-app copy.

- **No em dashes anywhere.** Use a colon, period, comma, or restructure. Common slip: bullet hook patterns where an em dash sits between a bold label and its explanation feel like markdown structure; write `**Label.** Text` instead.
- **No Co-Authored-By trailers** on commits.
- **No Claude Code session links** (`claude.ai/code/session_*`) in commits, PRs, comments, or any committed content.
- **No "was removed" / "was here" comments** when deleting code; just delete it.
- **Never force push.** Push new commits on top of existing branches. No `--force`, no `--force-with-lease`, no amending pushed commits.
- **Reply to PR review comments** on GitHub (via `gh api`) at the same time as pushing the fix; acknowledge what was changed.

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
- **PostgreSQL**: server reads `DATABASE_URL`. Migrations run on startup (`server/internal/db/db.go`).
- **Docker** (for digitsd cross-compile): `make build` uses a container, no host toolchain needed.
- **Pico SDK** (for firmware): set `PICO_SDK_PATH` to your checkout. `scripts/build.sh` auto-detects common locations.

## Build and test

Server (from `server/`):
```
make build              # builds bin/signald
make run                # build + run
make test               # unit tests (fast, default)
make test-integration   # unit + integration; see server/TESTING.md
make e2e                # Playwright (needs running stack)
```

digitsd (from `pi/digitsd/`):
```
make build          # cross-compiles to linux/arm64 via Docker
make build-local    # native build (requires local cross-compile libs)
make test
```

Firmware (from repo root):
```
make firmware         # builds in Docker
make firmware-local   # builds on host (requires arm-none-eabi-gcc + Pico SDK)
./scripts/flash.sh    # copies UF2 to mounted Pico
```

Test tiers, build tag syntax, env vars, and CI workflow names live in `server/TESTING.md`.

## Local dev server

For UI work against a disposable local Postgres with a pre-seeded dialup-themed user, from `server/`:

```
make dev-up      # brings up user-db in Docker, seeds dev@digits.local + household, runs host-native signald (foreground)
make dev-down    # stops and wipes the DB container + volume
make dev-seed    # re-runs the idempotent seeder only
make dev-logs    # tails the DB container
```

Defaults in `.env.dev.example` (committed); override by copying to `.env.dev` (gitignored). Sign in via `http://localhost:<port>/auth/dev-session?email=dev@digits.local`. The seeded user has `theme='dialup'` and is preloaded with three lines, two linked households, and one pending invite, so every surface (`/`, `/links`, `/phones`, `/calls`, `/settings`) renders with real data.

When `DEV_MODE=true`, signald serves `/static/*` from disk (`internal/web/static/`), so CSS and JS edits show on reload. Template edits still require a restart.

## Linting

golangci-lint v2 with `standard` defaults (`.golangci.yml` at repo root). Run `golangci-lint run ./...` from each module directory before pushing Go changes; CI enforces errcheck that `go vet` does not. Pre-commit hook: `git config core.hooksPath .githooks`.

## Production deployment

Server runs on the GPU box via Docker Compose, project name `digits-prod`. From `server/`:

```
docker compose -f docker-compose.prod.yml --env-file .env.prod up -d      # start
docker compose -f docker-compose.prod.yml --env-file .env.prod down        # stop
docker compose -f docker-compose.prod.yml --env-file .env.prod logs -f     # tail logs
```

Always include `--env-file .env.prod`. A bare `docker compose up` creates a separate `server-*` set of containers disconnected from production data. Services auto-start on reboot.

## Conventions

**Commits.** Conventional commit format with required scope. PR titles must use this format (they become the squash commit message on merge); individual commits on feature branches don't need to. Valid scopes: `pi`, `digitsd`, `firmware`, `server`, `image`, `docs`, `ci`.

```
fix(server): handle NULL line_id in device lookup
feat(digitsd): add volume service code
docs: update wiring notes
```

**Git.** Remote: `github` (`git@github.com:justinlindh/digits.git`). PRs required to merge into main; no direct pushes. PR template at `.github/pull_request_template.md`.

**Style.** Go uses standard project layout, raw SQL with `database/sql` (no ORM), errors returned not panicked. Server web UI uses htmx + Tailwind, templates in `internal/web/templates/`. Firmware uses C with Pico SDK conventions.

## CI

GitHub Actions workflows in `.github/workflows/`:
- `server-ci.yml`: build, test, vet (triggers on `server/` changes); required for merge.
- `server-integration.yml`: integration tests (Postgres service containers); required for merge to main.
- `fw-release` / `pi-release` / `server-release`: tag-triggered release pipelines.
