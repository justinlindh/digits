#!/usr/bin/env bash
# entrypoint.sh — Docker entrypoint for Digits Pi image builder
#
# Cross-compiles the Go binaries, then runs build-image.sh.
# The repo must be mounted at /digits and the base Pi OS image
# must be available inside the container.
set -euo pipefail

die()  { echo "ERROR: $*" >&2; exit 1; }
info() { echo "==> $*"; }

# Parse args: [--dev] <image-file>
DEV_FLAG=""
if [[ "${1:-}" == "--dev" ]]; then
    DEV_FLAG="--dev"
    shift
fi

SOURCE_IMAGE="${1:-}"
[[ -n "$SOURCE_IMAGE" ]] || die "Usage: entrypoint.sh [--dev] <raspios-lite.img|.img.xz>"
[[ -f "$SOURCE_IMAGE" ]] || die "Image not found: $SOURCE_IMAGE"

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
