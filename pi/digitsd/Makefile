BINARY      = digitsd
PI_HOST    ?= digits@<pi-ip>
PI_DIR      = ~/digits/digitsd
BUILDER_IMG = digitsd-builder

VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)$(shell git diff --quiet 2>/dev/null || echo -dirty)
LDFLAGS  = -X github.com/justinlindh/digits/pi/digitsd/internal/version.Version=$(VERSION) \
           -X github.com/justinlindh/digits/pi/digitsd/internal/version.Commit=$(COMMIT)
REPO_ROOT   = $(shell git rev-parse --show-toplevel)

EMBED_DIR         = internal/assets/embed
OVERLAY_SRC       = $(REPO_ROOT)/pi/image/rootfs-overlay
TONES_SRC         = $(REPO_ROOT)/pi/tones
DIGITS_SETUP_SRC  = $(REPO_ROOT)/pi/digits-setup
DIGITS_SETUP_BIN  = $(EMBED_DIR)/rootfs/usr/local/bin/digits-setup

# Paths in rootfs-overlay that are only consumed at image-build time (kernel
# boot config and device tree fragments live on the FAT firmware partition;
# the *.fragment files are appended into existing system files by the image
# builder; ro-root/rw-root are toggled manually on-device). None of these
# should be extracted at runtime by digitsd.
OVERLAY_EXCLUDE = boot etc/fstab.fragment usr/local/bin/ro-root usr/local/bin/rw-root etc/sudoers.d/digits-updater

.PHONY: build build-local builder deploy run test clean embed clean-embed

embed: clean-embed
	@mkdir -p $(EMBED_DIR)/rootfs $(EMBED_DIR)/data
	@cp -a $(OVERLAY_SRC)/. $(EMBED_DIR)/rootfs/
	@for x in $(OVERLAY_EXCLUDE); do rm -rf $(EMBED_DIR)/rootfs/$$x; done
	@find $(EMBED_DIR)/rootfs -type d -empty -delete 2>/dev/null || true
	@cp -a $(TONES_SRC)/. $(EMBED_DIR)/data/tones/
	@mkdir -p $(dir $(DIGITS_SETUP_BIN))
	@echo "embed: cross-compiling digits-setup for arm64"
	@cd $(DIGITS_SETUP_SRC) && \
		GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
		go build -trimpath -ldflags="-s -w" \
		-o $(abspath $(DIGITS_SETUP_BIN)) ./cmd/digits-setup
	@count=$$(find $(EMBED_DIR) -type f | wc -l); \
	 echo "embed: prepared $$count files"; \
	 if [ "$$count" -eq 0 ]; then echo "embed: ERROR: staged tree is empty -- rootfs-overlay missing?" >&2; exit 1; fi

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
