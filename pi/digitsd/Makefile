BINARY   = digitsd
PI_HOST ?= digits@<pi-ip>
PI_DIR   = ~/digits/digitsd
GOOS     = linux
GOARCH   = arm64

VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS  = -X github.com/justinlindh/digits/pi/digitsd/internal/version.Version=$(VERSION) \
           -X github.com/justinlindh/digits/pi/digitsd/internal/version.Commit=$(COMMIT)

.PHONY: build build-local deploy run test clean

build:
	CGO_ENABLED=1 CC=aarch64-linux-gnu-gcc GOOS=$(GOOS) GOARCH=$(GOARCH) \
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
