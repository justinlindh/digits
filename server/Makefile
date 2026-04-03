.PHONY: build build-dev run test e2e clean deploy css

# Version resolution: VERSION file > git describe > "dev"
VERSION ?= $(shell cat VERSION 2>/dev/null || git describe --tags --match 'server/v*' --abbrev=0 2>/dev/null | sed 's|^server/v||' || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS  = -X github.com/justinlindh/digits/server/internal/version.Version=$(VERSION) \
           -X github.com/justinlindh/digits/server/internal/version.Commit=$(COMMIT)

TAILWINDCSS ?= $(shell command -v tailwindcss 2>/dev/null || echo /tmp/tailwindcss-cli-linux-x64)

css:
	$(TAILWINDCSS) --input internal/web/static/input.css --output internal/web/static/tailwind.css --content 'internal/web/templates/*.html' --minify
	$(TAILWINDCSS) --input internal/admin/static/input.css --output internal/admin/static/tailwind.css --content 'internal/admin/templates/*.html' --minify

build:
	go build -ldflags '$(LDFLAGS)' -o bin/signald ./cmd/signald/

build-dev:
	go build -o bin/signald ./cmd/signald/

run: build
	./bin/signald

test:
	go test ./...

# E2E tests (Playwright) — requires a running dev server at BASE_URL (default: http://localhost:8080)
# Auth options:
#   make e2e E2E_SESSION_COOKIE=<cookie>   inject a known session cookie
#   make e2e E2E_MAGIC_TOKEN=<token>       redeem a magic-link token
# Without auth env vars, public-page tests run and auth-required tests skip gracefully.
e2e:
	cd e2e && npm install --silent
	cd e2e && npx playwright install --with-deps chromium
	cd e2e && npx playwright test

clean:
	rm -rf bin/

.PHONY: css

print-version:
	@echo $(VERSION)

# Deploy to production server (set DEPLOY_HOST)
deploy:
	ssh $(DEPLOY_HOST) 'cd ~/digits/server && git pull && docker compose -p digits-prod -f docker-compose.prod.yml --env-file .env.prod build signald && docker compose -p digits-prod -f docker-compose.prod.yml --env-file .env.prod up -d signald'
