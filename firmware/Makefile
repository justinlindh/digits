BUILDER_IMG = digits-firmware-builder
REPO_ROOT   = $(shell git rev-parse --show-toplevel)

# Docker and local builds use separate CMake build directories so that
# switching between them doesn't poison the cache with stale toolchain
# and PICO_SDK_PATH values.
DOCKER_BUILD_DIR = build/docker

# Git describe runs on the host so worktrees resolve correctly. Inside the
# Docker container, .git is a pointer file to a gitdir outside the mount,
# and `git describe` would fail silently -- which would leave FIRMWARE_VERSION
# as "unknown". Compute it here and pass through.
DIGITS_VERSION ?= $(shell git describe --tags --always --dirty --match 'fw/v*' 2>/dev/null | sed 's|^fw/v||')
DIGITS_COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

.PHONY: build build-local builder test clean

builder:
	@docker build -q -t $(BUILDER_IMG) -f Dockerfile.builder .

build: builder
	docker run --rm \
		--user $(shell id -u):$(shell id -g) \
		-v $(REPO_ROOT):/src -w /src/firmware \
		-e DIGITS_VERSION=$(DIGITS_VERSION) \
		-e DIGITS_COMMIT=$(DIGITS_COMMIT) \
		$(BUILDER_IMG) \
		sh -c 'mkdir -p $(DOCKER_BUILD_DIR) && cd $(DOCKER_BUILD_DIR) && cmake -DDIGITS_VERSION=$$DIGITS_VERSION -DDIGITS_COMMIT=$$DIGITS_COMMIT ../.. && make -j$$(nproc)'

build-local:
	@$(REPO_ROOT)/scripts/build.sh

# Host-side unit tests for the pure-logic modules (hook, keypad, uart_proto,
# phone_fsm). Native build, no Pico SDK and no cross toolchain required.
test:
	@cmake -S test -B test/build >/dev/null
	@cmake --build test/build
	@./test/build/fw_tests

clean:
	rm -rf build test/build
