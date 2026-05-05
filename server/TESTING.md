# Testing

The Go test suite is split into three tiers. Pick the right tier when you write a new test; do not mix.

## Unit tests

In-process, no external services. Must not require Postgres, Redis, a real HTTP backend, a real file system beyond `t.TempDir`, or a network. `httptest.Server`, `httptest.ResponseRecorder`, and in-memory fakes are fine.

Default tier. No build tag, no naming convention required.

## Integration tests

Require at least one real external dependency (today: PostgreSQL). Gated by a build tag so they don't compile into the default unit binary:

```go
//go:build integration

package foo
```

The blank line between the build tag and the `package` declaration is required by `go build` syntax. New integration files should use the `_integration_test.go` suffix (e.g., `store_integration_test.go`). Existing files use less consistent names but all carry the build tag.

## End-to-end tests

Playwright suite in `internal/web/e2e/` driven by `make e2e`. Spins up the full stack (signald + Postgres via `docker compose`) and drives a real browser. Outside the Go test runner.

## Running tests locally

From `server/`:

```
go test ./...                # unit only (fast, default)
make test-integration        # unit + integration (starts test-db, resets, runs)
make e2e                     # playwright e2e (needs running stack)
```

`make test-integration` is the preferred local workflow: it spins up an ephemeral Postgres (the `test-db` service in `docker-compose.yml`, profile `test`), drops any leftover schemas, and runs the full integration suite with `TEST_DATABASE_URL` already set. Individual targets (`test-db-up`, `test-db-down`, `test-db-reset`) are available if you want to manage the container separately.

Manual invocation also works:

```
go test -tags=integration ./...
export TEST_DATABASE_URL="postgres://digits:digits@localhost:5432/digits_test?sslmode=disable"
```

If the variable is unset, tests that require it call `t.Skip` and log the reason. Running `go test -tags=integration ./...` without a DSN is safe: it skips anything it can't run.

## CI

- `.github/workflows/server-ci.yml`: unit job. Runs on every push and PR. Required for merge.
- `.github/workflows/server-integration.yml`: integration job. Runs on PRs targeting main and pushes to main. Provisions Postgres service containers and runs `go test -tags=integration`. Required for merge to main.

The unit job must stay fast. If you find yourself adding a Postgres dependency to something that could be tested in-process, the right move is to restructure the code (extract a pure function, use an interface, inject a fake) rather than reach for the integration tag.
