BINARY      = digitsd
PI_HOST    ?= digits@<pi-ip>
PI_DIR      = ~/digits/digitsd
BUILDER_IMG = digitsd-builder

VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)$(shell git diff --quiet 2>/dev/null || echo -dirty)
LDFLAGS  = -X github.com/justinlindh/digits/pi/digitsd/internal/version.Version=$(VERSION) \
           -X github.com/justinlindh/digits/pi/digitsd/internal/version.Commit=$(COMMIT)
REPO_ROOT   = $(shell git rev-parse --show-toplevel)

EMBED_DIR   = internal/assets/embed
OVERLAY_SRC = $(REPO_ROOT)/pi/image/rootfs-overlay
TONES_SRC   = $(REPO_ROOT)/pi/tones

# Files from rootfs-overlay that are only used by the image builder (not deployed via embed)
OVERLAY_EXCLUDE = boot/firmware/cmdline-ro.txt.fragment etc/fstab.fragment usr/local/bin/ro-root usr/local/bin/rw-root

.PHONY: build build-local builder deploy run test clean embed clean-embed

embed: clean-embed
	@mkdir -p $(EMBED_DIR)/rootfs $(EMBED_DIR)/data
	@cp -a $(OVERLAY_SRC)/. $(EMBED_DIR)/rootfs/
	@for f in $(OVERLAY_EXCLUDE); do rm -f $(EMBED_DIR)/rootfs/$$f; done
	@find $(EMBED_DIR)/rootfs -type d -empty -delete 2>/dev/null || true
	@cp -a $(TONES_SRC)/. $(EMBED_DIR)/data/tones/
	@echo "embed: prepared $$(find $(EMBED_DIR) -type f | wc -l) files"

clean-embed:
	@rm -rf $(EMBED_DIR)

builder:
	@docker build -q -t $(BUILDER_IMG) -f Dockerfile.builder .

build: builder embed
	@mkdir -p bin
	docker run --rm \
		-v $(REPO_ROOT):/src -w /src/pi/digitsd \
		-v digitsd-gomodcache:/go/pkg/mod \
		$(BUILDER_IMG) \
		go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/digitsd

build-local: embed
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/digitsd

deploy: build
	ssh $(PI_HOST) "mkdir -p $(PI_DIR)"
	scp bin/$(BINARY) $(PI_HOST):$(PI_DIR)/$(BINARY)

run: deploy
	ssh $(PI_HOST) "$(PI_DIR)/$(BINARY)"

test:
	go test ./...

clean: clean-embed
	rm -rf bin/
