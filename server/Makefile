.PHONY: build build-autodeploy build-dev run test test-integration test-db-up test-db-down test-db-reset e2e e2e-ci clean deploy dev-up dev-down dev-seed dev-logs lint vet

# Version resolution: VERSION file > git describe > "dev"
VERSION ?= $(shell cat VERSION 2>/dev/null || git describe --tags --match 'server/v*' --abbrev=0 2>/dev/null | sed 's|^server/v||' || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS  = -X github.com/justinlindh/digits/server/internal/version.Version=$(VERSION) \
           -X github.com/justinlindh/digits/server/internal/version.Commit=$(COMMIT)

TAILWINDCSS ?= $(shell command -v tailwindcss 2>/dev/null || echo /tmp/tailwindcss-cli-linux-x64)

build:
	go build -ldflags '$(LDFLAGS)' -o bin/signald ./cmd/signald/

build-autodeploy:
	go build -ldflags '$(LDFLAGS)' -o bin/autodeploy ./cmd/autodeploy/

build-dev:
	go build -o bin/signald ./cmd/signald/

run: build
	./bin/signald

test:
	go test ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

# Integration tests against a local ephemeral Postgres (the `test-db` compose profile).
# `test-db-reset` drops any leftover test state before running, matching the fresh-container
# isolation that CI gets from ephemeral service containers.
TEST_DATABASE_URL ?= postgres://digits:digits@localhost:5432/digits_test?sslmode=disable

test-db-up:
	docker compose --profile test up -d test-db
	@echo "Waiting for test-db to accept connections..."
	@for i in $$(seq 1 30); do \
		docker exec server-test-db-1 pg_isready -U digits > /dev/null 2>&1 && break; \
		sleep 1; \
	done
	@docker exec server-test-db-1 pg_isready -U digits > /dev/null 2>&1 || { echo "test-db failed to start"; exit 1; }

test-db-down:
	docker compose --profile test down

test-db-reset:
	@docker exec server-test-db-1 psql -U digits -d digits_test -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public; GRANT ALL ON SCHEMA public TO digits;" > /dev/null

test-integration: test-db-up test-db-reset
	TEST_DATABASE_URL=$(TEST_DATABASE_URL) \
	go test -tags=integration -race -count=1 -p 1 ./...

# E2E tests (Playwright) — requires a running dev server at BASE_URL (default: http://localhost:8080)
# Auth options:
#   make e2e E2E_SESSION_COOKIE=<cookie>   inject a known session cookie
#   make e2e E2E_MAGIC_TOKEN=<token>       redeem a magic-link token
# Without auth env vars, public-page tests run and auth-required tests skip gracefully.
e2e:
	cd e2e && npm install --silent
	cd e2e && npx playwright install --with-deps chromium
	cd e2e && npx playwright test

# E2E CI — full lifecycle: build, start server, run Playwright, clean up.
# Requires a running PostgreSQL with DATABASE_URL set (defaults to local dev DB).
e2e-ci:
	$(MAKE) build
	DEV_MODE=true TEST_FAKE_UPDATES=1 SIGNALD_ADDR=:8080 BASE_URL=http://localhost:8080 DATABASE_URL=$(DATABASE_URL) ./bin/signald & SERVER_PID=$$!; \
	trap "kill $$SERVER_PID 2>/dev/null" EXIT; \
	for i in $$(seq 1 30); do \
		curl -sf http://localhost:8080/healthz > /dev/null 2>&1 && break; \
		sleep 1; \
	done; \
	curl -sf http://localhost:8080/healthz > /dev/null 2>&1 || { echo "Server failed to start"; exit 1; }; \
	cd e2e && npm install --silent && \
	npx playwright install --with-deps chromium && \
	npx playwright test

# ── Local dev environment ────────────────────────────────────────────────
# Brings up a disposable Postgres in Docker, seeds a dialup-themed user,
# and runs host-native signald against it. See server/README.md for details.
#
# Config lives in server/.env.dev (copy .env.dev.example to .env.dev to
# customize). Each dev target sources .env.dev if it exists, otherwise
# falls back to .env.dev.example defaults.

DEV_ENV_FILE   ?= .env.dev
DEV_DB_SERVICE ?= user-db
DEV_COMPOSE     = docker compose -p digits-dev -f docker-compose.yml

# Shell snippet (no trailing semicolon) that sources .env.dev or .env.dev.example
# into the current shell and exports all assigned vars.
DEV_ENV_SRC = set -a; if [ -f $(DEV_ENV_FILE) ]; then . ./$(DEV_ENV_FILE); else . ./.env.dev.example; fi; set +a

dev-up:
	@if [ ! -f $(DEV_ENV_FILE) ]; then \
		echo "note: $(DEV_ENV_FILE) not found, using defaults from .env.dev.example"; \
	fi
	$(DEV_ENV_SRC); \
	$(DEV_COMPOSE) up -d $(DEV_DB_SERVICE); \
	echo "waiting for $(DEV_DB_SERVICE) to be healthy..."; \
	for i in $$(seq 1 30); do \
		status=$$($(DEV_COMPOSE) ps --format '{{.Health}}' $(DEV_DB_SERVICE) 2>/dev/null | head -n1); \
		[ "$$status" = "healthy" ] && break; \
		sleep 1; \
	done; \
	[ "$$status" = "healthy" ] || { echo "$(DEV_DB_SERVICE) failed to become healthy"; exit 1; }; \
	echo "seeding dev user..."; \
	go run ./cmd/devseed/; \
	echo ""; \
	echo "starting signald on $$SIGNALD_ADDR (Ctrl+C to stop; run 'make dev-down' to clean up)"; \
	go build -o bin/signald ./cmd/signald/; \
	./bin/signald

dev-seed:
	$(DEV_ENV_SRC); \
	go run ./cmd/devseed/

dev-down:
	$(DEV_ENV_SRC); \
	$(DEV_COMPOSE) down -v

dev-logs:
	$(DEV_COMPOSE) logs -f $(DEV_DB_SERVICE)

clean:
	rm -rf bin/

print-version:
	@echo $(VERSION)

# Deploy locally via Docker Compose (fetches tags for accurate version)
deploy:
	git fetch --tags
	BUILD_VERSION=$$(git describe --tags --match 'server/v*' --always | sed 's|^server/v||') \
	BUILD_COMMIT=$$(git rev-parse --short HEAD) \
	docker compose -f docker-compose.prod.yml --env-file .env.prod up -d --build signald
