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

# Register qemu-aarch64 binfmt if not already present (needed for chroot)
if [[ ! -f /proc/sys/fs/binfmt_misc/qemu-aarch64 ]]; then
    info "Registering qemu-aarch64 binfmt..."
    # Mount binfmt_misc if needed
    if ! mountpoint -q /proc/sys/fs/binfmt_misc 2>/dev/null; then
        mount binfmt_misc -t binfmt_misc /proc/sys/fs/binfmt_misc 2>/dev/null || true
    fi
    # Register the interpreter
    if [[ -d /proc/sys/fs/binfmt_misc ]]; then
        echo ':qemu-aarch64:M::\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\xb7\x00:\xff\xff\xff\xff\xff\xff\xff\x00\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\xff:/usr/bin/qemu-aarch64-static:F' \
            > /proc/sys/fs/binfmt_misc/register 2>/dev/null || true
    fi
    # Verify
    [[ -f /proc/sys/fs/binfmt_misc/qemu-aarch64 ]] || die "Failed to register qemu-aarch64 binfmt. Is the container running with --privileged?"
fi

# Cross-compile digitsd
info "Cross-compiling digitsd for aarch64..."
mkdir -p /digits/tools/build
cd /digits/pi/digitsd

# Verify embedded assets exist (must be generated on host via 'make embed')
if [[ ! -d internal/assets/embed ]]; then
    die "Embed directory not found. Run 'make embed' in pi/digitsd/ before building the image."
fi

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

# Run the image builder in a working directory, then copy output back
BUILD_WD="/build"
mkdir -p "$BUILD_WD"
cd "$BUILD_WD"
bash /digits/tools/build-image.sh $DEV_FLAG "$SOURCE_IMAGE"

# Copy the output image to the mounted repo (accessible from host)
cp -v "$BUILD_WD"/digits-pi-*.img.gz /digits/
