BUILDER_IMG = digits-firmware-builder
REPO_ROOT   = $(shell git rev-parse --show-toplevel)

# Docker and local builds use separate CMake build directories so that
# switching between them doesn't poison the cache with stale toolchain
# and PICO_SDK_PATH values.
DOCKER_BUILD_DIR = build/docker

# Target hardware revision. 1 = v1 ElectroCookie prototype, 2 = v2 carrier PCB.
# Override on the command line: `make build HARDWARE_REV=2`.
HARDWARE_REV ?= 1

.PHONY: build build-local builder clean

builder:
	@docker build -q -t $(BUILDER_IMG) -f Dockerfile.builder .

build: builder
	docker run --rm \
		--user $(shell id -u):$(shell id -g) \
		-v $(REPO_ROOT):/src -w /src/firmware \
		-e HARDWARE_REV=$(HARDWARE_REV) \
		$(BUILDER_IMG) \
		sh -c 'mkdir -p $(DOCKER_BUILD_DIR) && cd $(DOCKER_BUILD_DIR) && cmake -DHARDWARE_REV=$$HARDWARE_REV ../.. && make -j$$(nproc)'

build-local:
	@HARDWARE_REV=$(HARDWARE_REV) $(REPO_ROOT)/scripts/build.sh

clean:
	rm -rf build
