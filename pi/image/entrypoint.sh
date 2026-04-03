#!/usr/bin/env bash
# entrypoint.sh -- Docker entrypoint for Digits Pi image builder
#
# Cross-compiles the Go binaries, then runs build-image.sh.
# The repo must be mounted at /digits. If no base image is provided,
# downloads a known-good Raspberry Pi OS Lite image to /cache.
set -euo pipefail

die()  { echo "ERROR: $*" >&2; exit 1; }
info() { echo "==> $*"; }

# Known-good base image
BASE_IMAGE_URL="https://downloads.raspberrypi.com/raspios_lite_arm64/images/raspios_lite_arm64-2024-11-19/2024-11-19-raspios-bookworm-arm64-lite.img.xz"
BASE_IMAGE_NAME="2024-11-19-raspios-bookworm-arm64-lite.img.xz"
# Set to empty string to skip verification
BASE_IMAGE_SHA256=""
CACHE_DIR="/cache"

# Parse args: [--dev] [image-file]
DEV_FLAG=""
if [[ "${1:-}" == "--dev" ]]; then
    DEV_FLAG="--dev"
    shift
fi

SOURCE_IMAGE="${1:-}"

# If no image provided, download to cache
if [[ -z "$SOURCE_IMAGE" ]]; then
    mkdir -p "$CACHE_DIR"
    SOURCE_IMAGE="${CACHE_DIR}/${BASE_IMAGE_NAME}"

    if [[ -f "$SOURCE_IMAGE" ]]; then
        info "Using cached base image: $SOURCE_IMAGE"
    else
        info "Downloading Raspberry Pi OS Lite (Bookworm arm64)..."
        curl -L --progress-bar -o "$SOURCE_IMAGE" "$BASE_IMAGE_URL"
    fi

    # Verify integrity (if hash is configured)
    if [[ -n "$BASE_IMAGE_SHA256" ]]; then
        info "Verifying SHA256..."
        echo "$BASE_IMAGE_SHA256  $SOURCE_IMAGE" | sha256sum -c - || die "SHA256 mismatch -- delete $SOURCE_IMAGE and retry"
    fi
else
    [[ -f "$SOURCE_IMAGE" ]] || die "Image not found: $SOURCE_IMAGE"
fi

# Trust the mounted repo (owned by host user, not container root)
git config --global --add safe.directory /digits

# Cross-compile digitsd
info "Cross-compiling digitsd for aarch64..."
mkdir -p /digits/tools/build
cd /digits/pi/digitsd
export PKG_CONFIG_PATH="/usr/lib/aarch64-linux-gnu/pkgconfig"
export CC=aarch64-linux-gnu-gcc
export CGO_ENABLED=1
export CGO_CFLAGS="-I/usr/aarch64-linux-gnu/include -I/usr/include"
export CGO_LDFLAGS="-L/usr/lib/aarch64-linux-gnu"
GOOS=linux GOARCH=arm64 go build \
    -o /digits/tools/build/digitsd \
    ./cmd/digitsd/

# Cross-compile digits-setup (pure Go, no CGO needed)
info "Cross-compiling digits-setup for aarch64..."
cd /digits/pi/digits-setup
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
    -o /digits/tools/build/digits-setup \
    ./cmd/digits-setup/

info "Binaries ready in tools/build/"

# Run the image builder
cd /digits
exec bash tools/build-image.sh $DEV_FLAG "$SOURCE_IMAGE"
