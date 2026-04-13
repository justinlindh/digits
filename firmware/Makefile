BUILDER_IMG = digits-firmware-builder
REPO_ROOT   = $(shell git rev-parse --show-toplevel)
BUILD_DIR   = build

.PHONY: build build-local builder clean

builder:
	@docker build -q -t $(BUILDER_IMG) -f Dockerfile.builder .

build: builder
	docker run --rm \
		--user $(shell id -u):$(shell id -g) \
		-v $(REPO_ROOT):/src -w /src/firmware \
		$(BUILDER_IMG) \
		sh -c 'mkdir -p $(BUILD_DIR) && cd $(BUILD_DIR) && cmake .. && make -j$$(nproc)'

build-local:
	@$(REPO_ROOT)/scripts/build.sh

clean:
	rm -rf $(BUILD_DIR)
