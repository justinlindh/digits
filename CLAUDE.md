# Digits

Private encrypted phone network built from gutted vintage desk phones. Three components talk to each other: firmware on an RP2040, a Go daemon on a Pi Zero 2 W, and a Go signaling server.

## Repo layout

```
firmware/       RP2040 Pico firmware (C, CMake, Pico SDK)
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
- **Cross-compile toolchain** (for digitsd): `sudo apt install gcc-aarch64-linux-gnu libasound2-dev:arm64 libopus-dev:arm64 libopusfile-dev:arm64 pkg-config`
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
make build          # cross-compiles to linux/arm64 (needs cross-compile toolchain above)
make build-local    # native build
make test
```

Firmware (from repo root):
```
PICO_SDK_PATH=/path/to/pico-sdk ./scripts/build.sh
./scripts/flash.sh  # copies UF2 to mounted Pico
```

## Commit conventions

Conventional commits enforced by commitlint. Scope is required (warning if empty, error if invalid).

Valid scopes: `pi`, `digitsd`, `firmware`, `server`, `image`, `docs`, `ci`

Examples:
```
fix(server): handle NULL line_id in device lookup
feat(digitsd): add volume service code
docs: update wiring notes
```

## Git workflow

Two remotes:
- **gitea** -- primary development remote (ssh://git@192.168.1.199:2222/dumbot/digits.git)
- **github** -- public mirror (git@github.com:justinlindh/digits.git)

Push to gitea by default. Push to github intentionally. PRs required to merge into main on GitHub; no direct pushes. Use the PR template at `.github/pull_request_template.md`.

## CI

Mirrored workflows on both Gitea (`.gitea/workflows/`) and GitHub (`.github/workflows/`):
- **commitlint** -- validates commit messages on PRs
- **server-ci** -- build, test, vet (triggers on `server/` changes)
- **fw-release / pi-release / server-release** -- tag-triggered release pipelines

## Style

- Go: standard project layout, raw SQL with `database/sql` (no ORM), errors returned not panicked
- Server web UI: htmx + Tailwind, templates in `internal/web/templates/`
- Firmware: C with Pico SDK conventions
- Never use em dashes in any written copy
- Never add Co-Authored-By trailers to commits
