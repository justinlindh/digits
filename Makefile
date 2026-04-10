.PHONY: help server server-test pi-build pi-test firmware image image-dev flash image-flash clean

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

flash: ## Flash the most recent image to SD card (auto-detects or SD=/dev/sdX)
	@IMAGE=$$(ls -t digits-pi-*.img.gz 2>/dev/null | head -1); \
	if [ -z "$$IMAGE" ]; then echo "No image found -- run 'make image-dev' first"; exit 1; fi; \
	if [ -n "$(SD)" ]; then \
		SD_DEV="$(SD)"; \
	else \
		SD_DEV=$$(lsblk -d -n -o NAME,SIZE,TRAN | awk '/usb/ && /[0-9]+\.?[0-9]*G/ { dev="/dev/"$$1; size=$$2+0; if (size >= 4 && size <= 64) print dev }' | head -1); \
		if [ -z "$$SD_DEV" ]; then echo "No SD card detected. Specify manually: make flash SD=/dev/sdX"; exit 1; fi; \
	fi; \
	echo "Flashing $$IMAGE -> $$SD_DEV"; \
	lsblk "$$SD_DEV"; \
	echo "WARNING: This will overwrite all data on $$SD_DEV."; \
	read -r -p "Continue? [y/N] " ans; \
	if [ "$$ans" != "y" ] && [ "$$ans" != "Y" ]; then echo "Aborted."; exit 1; fi; \
	sudo umount "$$SD_DEV"* 2>/dev/null || true; \
	gunzip -c "$$IMAGE" | sudo dd of="$$SD_DEV" bs=4M status=progress conv=fsync && \
	sync && echo "Flash complete. Safe to remove SD card."

image-flash: image-dev flash ## Build dev image and flash in one step

# ── Utilities ────────────────────────────────────────────────────────────────

test: server-test pi-test ## Run all tests

clean: ## Clean build artifacts
	$(MAKE) -C server clean
	$(MAKE) -C pi/digitsd clean
	rm -rf tools/build/ firmware/build/
