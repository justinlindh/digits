.PHONY: help server server-test pi-build pi-test firmware firmware-local firmware-test fetch-tags stage-firmware image image-dev image-v2 image-v2-dev flash flash-v1 flash-v2 image-flash image-v2-flash clean

# Refresh tags from origin so version derivation in firmware and pi-build
# resolves to the latest published release, not whatever the local clone
# last fetched. Offline-tolerant: a failed fetch is silent and the build
# falls back to whatever's already in .git.
fetch-tags:
	@git fetch --tags --quiet 2>/dev/null || true

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

pi-build: fetch-tags ## Cross-compile digitsd for aarch64
	$(MAKE) -C pi/digitsd build

pi-test: ## Run digitsd tests (host architecture)
	$(MAKE) -C pi/digitsd test

# ── Firmware ─────────────────────────────────────────────────────────────────

firmware: fetch-tags ## Build Pico firmware (Docker, no host toolchain needed)
	$(MAKE) -C firmware build

firmware-local: ## Build Pico firmware on host (requires arm-none-eabi-gcc + Pico SDK)
	$(MAKE) -C firmware build-local

firmware-test: ## Run firmware host tests (native cmake + gcc, no Pico SDK needed)
	$(MAKE) -C firmware test

# Mirror firmware/Makefile's DIGITS_VERSION derivation so the .version file we
# stage matches the version string the firmware reports over UART after boot.
DIGITS_FW_VERSION ?= $(shell git describe --tags --always --dirty --match 'fw/v*' 2>/dev/null | sed 's|^fw/v||')

stage-firmware: firmware ## Build firmware and stage it at tools/build/firmware.elf for image bundling
	@mkdir -p tools/build
	@cp firmware/build/docker/digits.elf tools/build/firmware.elf
	@printf '%s\n' '$(DIGITS_FW_VERSION)' > tools/build/firmware.elf.version
	@echo "==> staged firmware $(DIGITS_FW_VERSION) -> tools/build/firmware.elf"

# ── Pi SD Card Image ─────────────────────────────────────────────────────────
#
# Default (make image / make image-v2): downloads pre-built binaries and
# firmware from the latest GitHub release. Requires Go (for `make embed`).
# To build from local source instead: BUILD_LOCAL=1 make image
# (run `git fetch --tags` first so version stamps are accurate).
#
# Dev targets (make image-dev / make image-v2-dev) imply BUILD_LOCAL=1 and
# stage firmware from the local build before invoking the container.

image: ## Build Pi SD card image for V1/prototype hardware (release artifacts by default)
	./pi/image/build-docker.sh

image-dev: stage-firmware ## Build V1/prototype image with SSH enabled (local build)
	BUILD_LOCAL=1 ./pi/image/build-docker.sh --dev

image-v2: ## Build Pi SD card image for V2 carrier board (release artifacts by default)
	./pi/image/build-docker.sh --pcb

image-v2-dev: stage-firmware ## Build V2 carrier board image with SSH enabled (local build)
	BUILD_LOCAL=1 ./pi/image/build-docker.sh --dev --pcb

# Default flash glob: newest of any variant. flash-v1 / flash-v2 narrow it.
# Override with IMAGE=<path> to flash a specific file regardless of glob.
IMAGE_GLOB ?= digits-pi-v*-*.img.gz

flash: ## Flash newest image (use flash-v1 / flash-v2 to pick, or IMAGE=<path>)
	@IMAGE="$(IMAGE)"; \
	if [ -z "$$IMAGE" ]; then \
		IMAGE=$$(ls -t $(IMAGE_GLOB) 2>/dev/null | head -1); \
	fi; \
	if [ -z "$$IMAGE" ]; then echo "No image found matching $(IMAGE_GLOB) -- build one first"; exit 1; fi; \
	if [ -n "$(SD)" ]; then \
		SD_DEV="$(SD)"; \
	else \
		SD_DEV=$$(lsblk -d -n -o NAME,SIZE,TRAN | awk '($$3 == "usb" || $$3 == "mmc") && $$2 ~ /G$$/ { size=$$2+0; if (size >= 4 && size <= 64) print "/dev/"$$1 }' | head -1); \
		if [ -z "$$SD_DEV" ]; then echo "No SD card detected. Specify manually: make flash SD=/dev/sdX"; exit 1; fi; \
	fi; \
	if [ ! -e "$$SD_DEV" ]; then echo "ERROR: $$SD_DEV does not exist. Is the card inserted?"; exit 1; fi; \
	if [ ! -b "$$SD_DEV" ]; then echo "ERROR: $$SD_DEV is not a block device (it is $$(stat -c %F "$$SD_DEV")). Refusing to flash. If a stale regular file is shadowing the device, remove it with 'sudo rm $$SD_DEV' and re-insert the card."; exit 1; fi; \
	echo "Flashing $$IMAGE -> $$SD_DEV"; \
	lsblk "$$SD_DEV"; \
	echo "WARNING: This will overwrite all data on $$SD_DEV."; \
	printf "Continue? [y/N] "; \
	read -r ans; \
	if [ "$$ans" != "y" ] && [ "$$ans" != "Y" ]; then echo "Aborted."; exit 1; fi; \
	sudo umount "$$SD_DEV"* 2>/dev/null || true; \
	gunzip -c "$$IMAGE" | sudo dd of="$$SD_DEV" bs=4M status=progress conv=fsync && \
	sync && echo "Flash complete. Safe to remove SD card."

flash-v1: IMAGE_GLOB = digits-pi-v1-*.img.gz
flash-v1: flash ## Flash newest V1/prototype image

flash-v2: IMAGE_GLOB = digits-pi-v2-*.img.gz
flash-v2: flash ## Flash newest V2 carrier board image

# Run build and flash as ordered sub-makes so `make -j` can't flash a stale or
# partial image: the image must finish building before flash-v1/flash-v2 reads
# the newest .img.gz. Listing them as plain prerequisites would let parallel
# make run them concurrently.
image-flash: ## Build V1 dev image and flash
	$(MAKE) image-dev
	$(MAKE) flash-v1

image-v2-flash: ## Build V2 dev image and flash
	$(MAKE) image-v2-dev
	$(MAKE) flash-v2

# ── Utilities ────────────────────────────────────────────────────────────────

test: server-test pi-test firmware-test ## Run all tests

clean: ## Clean build artifacts
	$(MAKE) -C server clean
	$(MAKE) -C pi/digitsd clean
	$(MAKE) -C firmware clean
	rm -rf tools/build/
