BINARY      = digitsd
PI_HOST    ?= digits@<pi-ip>
PI_DIR      = ~/digits/digitsd
BUILDER_IMG = digitsd-builder

VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS  = -X github.com/justinlindh/digits/pi/digitsd/internal/version.Version=$(VERSION) \
           -X github.com/justinlindh/digits/pi/digitsd/internal/version.Commit=$(COMMIT)
REPO_ROOT   = $(shell git rev-parse --show-toplevel)

.PHONY: build build-local builder deploy run test clean

builder:
	@docker build -q -t $(BUILDER_IMG) -f Dockerfile.builder .

build: builder
	@mkdir -p bin
	docker run --rm \
		-v $(REPO_ROOT):/src -w /src/pi/digitsd \
		-v digitsd-gomodcache:/go/pkg/mod \
		$(BUILDER_IMG) \
		go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/digitsd

build-local:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/digitsd

deploy: build
	ssh $(PI_HOST) "mkdir -p $(PI_DIR)"
	scp bin/$(BINARY) $(PI_HOST):$(PI_DIR)/$(BINARY)

run: deploy
	ssh $(PI_HOST) "$(PI_DIR)/$(BINARY)"

test:
	go test ./...

clean:
	rm -rf bin/
