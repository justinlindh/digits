.PHONY: help server server-test pi-build pi-test firmware image image-dev clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

# ── Server ───────────────────────────────────────────────────────────────────

server: ## Build the signaling server
	$(MAKE) -C server build

server-test: ## Run server tests
	$(MAKE) -C server test

server-run: ## Build and run the signaling server
	$(MAKE) -C server run

# ── Pi Daemon ────────────────────────────────────────────────────────────────

pi-build: ## Cross-compile digitsd for aarch64
	$(MAKE) -C pi/digitsd build

pi-test: ## Run digitsd tests (host architecture)
	$(MAKE) -C pi/digitsd test

# ── Firmware ─────────────────────────────────────────────────────────────────

firmware: ## Build Pico firmware (requires arm-none-eabi-gcc + Pico SDK)
	./scripts/build.sh

# ── Pi SD Card Image ─────────────────────────────────────────────────────────

image: ## Build flashable Pi SD card image (Docker)
	./pi/image/build-docker.sh

image-dev: ## Build Pi image with SSH enabled (Docker)
	./pi/image/build-docker.sh --dev

# ── Utilities ────────────────────────────────────────────────────────────────

test: server-test pi-test ## Run all tests

clean: ## Clean build artifacts
	$(MAKE) -C server clean
	$(MAKE) -C pi/digitsd clean
	rm -rf tools/build/ firmware/build/
