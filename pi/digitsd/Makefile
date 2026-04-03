BINARY   = digitsd
PI_HOST ?= digits@<pi-ip>
PI_DIR   = ~/digits/digitsd
GOOS     = linux
GOARCH   = arm64

.PHONY: build build-local deploy run test clean

build:
	CGO_ENABLED=1 CC=aarch64-linux-gnu-gcc GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -o bin/$(BINARY) ./cmd/digitsd

build-local:
	go build -o bin/$(BINARY) ./cmd/digitsd

deploy: build
	ssh $(PI_HOST) "mkdir -p $(PI_DIR)"
	scp bin/$(BINARY) $(PI_HOST):$(PI_DIR)/$(BINARY)

run: deploy
	ssh $(PI_HOST) "$(PI_DIR)/$(BINARY)"

test:
	go test ./...

clean:
	rm -rf bin/
