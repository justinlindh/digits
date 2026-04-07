.PHONY: help server server-test pi-build pi-test firmware image image-dev flash clean

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

flash: ## Flash the most recent image to SD card (SD=<device>, e.g. make flash SD=/dev/sdd)
	@if [ -z "$(SD)" ]; then echo "Usage: make flash SD=/dev/sdX"; exit 1; fi
	@IMAGE=$$(ls -t digits-pi-*.img.gz 2>/dev/null | head -1); \
	if [ -z "$$IMAGE" ]; then echo "No image found -- run 'make image-dev' first"; exit 1; fi; \
	echo "Flashing $$IMAGE → $(SD)"; \
	echo "WARNING: This will overwrite all data on $(SD). Press Ctrl-C to cancel."; \
	read -r -p "Continue? [y/N] " ans; \
	if [ "$$ans" != "y" ] && [ "$$ans" != "Y" ]; then echo "Aborted."; exit 1; fi; \
	gunzip -c "$$IMAGE" | sudo dd of=$(SD) bs=4M status=progress conv=fsync

# ── Utilities ────────────────────────────────────────────────────────────────

test: server-test pi-test ## Run all tests

clean: ## Clean build artifacts
	$(MAKE) -C server clean
	$(MAKE) -C pi/digitsd clean
	rm -rf tools/build/ firmware/build/
