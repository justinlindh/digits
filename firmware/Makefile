BUILDER_IMG = digits-firmware-builder
REPO_ROOT   = $(shell git rev-parse --show-toplevel)

# Docker and local builds use separate CMake build directories so that
# switching between them doesn't poison the cache with stale toolchain
# and PICO_SDK_PATH values.
DOCKER_BUILD_DIR = build/docker

# Target hardware revision. 1 = v1 ElectroCookie prototype, 2 = v2 carrier PCB.
# Override on the command line: `make build HARDWARE_REV=2`.
HARDWARE_REV ?= 1

# Git describe runs on the host so worktrees resolve correctly. Inside the
# Docker container, .git is a pointer file to a gitdir outside the mount,
# and `git describe` would fail silently -- which would leave FIRMWARE_VERSION
# as "unknown". Compute it here and pass through.
DIGITS_VERSION ?= $(shell git describe --tags --always --dirty --match 'fw/v*' 2>/dev/null | sed 's|^fw/v||')
DIGITS_COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

.PHONY: build build-local builder clean

builder:
	@docker build -q -t $(BUILDER_IMG) -f Dockerfile.builder .

build: builder
	docker run --rm \
		--user $(shell id -u):$(shell id -g) \
		-v $(REPO_ROOT):/src -w /src/firmware \
		-e HARDWARE_REV=$(HARDWARE_REV) \
		-e DIGITS_VERSION=$(DIGITS_VERSION) \
		-e DIGITS_COMMIT=$(DIGITS_COMMIT) \
		$(BUILDER_IMG) \
		sh -c 'mkdir -p $(DOCKER_BUILD_DIR) && cd $(DOCKER_BUILD_DIR) && cmake -DHARDWARE_REV=$$HARDWARE_REV -DDIGITS_VERSION=$$DIGITS_VERSION -DDIGITS_COMMIT=$$DIGITS_COMMIT ../.. && make -j$$(nproc)'

build-local:
	@HARDWARE_REV=$(HARDWARE_REV) $(REPO_ROOT)/scripts/build.sh

clean:
	rm -rf build
