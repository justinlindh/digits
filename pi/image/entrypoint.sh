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

# Parse args: [--dev] [--pcb] [image-file]
BUILD_FLAGS=()
while [[ "${1:-}" == --* ]]; do
    case "$1" in
        --dev|--pcb)
            BUILD_FLAGS+=("$1")
            ;;
        *)
            die "Unknown flag: $1"
            ;;
    esac
    shift
done

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

# Trust the mounted repo (owned by host user, not container root).
# In a git worktree, .git is a file pointing to the main repo's
# .git/worktrees/ directory. Trust both paths so git works either way.
git config --global --add safe.directory /digits
if [[ -f /digits/.git ]]; then
    MAIN_GIT_DIR=$(sed -n 's/^gitdir: //p' /digits/.git)
    MAIN_GIT_DIR="${MAIN_GIT_DIR%/worktrees/*}"
    git config --global --add safe.directory "$(dirname "$MAIN_GIT_DIR")" 2>/dev/null || true
fi

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

# Stamp pi_version / pi_commit so devices report a real version instead
# of "dev". Tags were refreshed host-side by make fetch-tags before this
# container ran; if no pi/v* tag exists, fall back to "dev".
DIGITSD_VERSION=$(git -C /digits describe --tags --dirty --match 'pi/v*' 2>/dev/null | sed 's|^pi/v||')
DIGITSD_VERSION=${DIGITSD_VERSION:-dev}
DIGITSD_COMMIT=$(git -C /digits rev-parse --short HEAD 2>/dev/null || echo unknown)
info "Stamping digitsd: version=$DIGITSD_VERSION commit=$DIGITSD_COMMIT"
GOOS=linux GOARCH=arm64 go build \
    -ldflags "-X github.com/justinlindh/digits/pi/digitsd/internal/version.Version=$DIGITSD_VERSION \
              -X github.com/justinlindh/digits/pi/digitsd/internal/version.Commit=$DIGITSD_COMMIT" \
    -o /digits/tools/build/digitsd \
    ./cmd/digitsd/

# digits-setup is cross-compiled by `make embed` on the host (see
# pi/digitsd/Makefile) and lands on the rootfs via build-image.sh's
# embed/rootfs/ rsync. Nothing to do here.

# Cross-compile digits-recovery (pure Go, no CGO needed). build-image.sh reads
# this from pi/digits-recovery/bin/ to match the local Makefile output path.
info "Cross-compiling digits-recovery for aarch64..."
cd /digits/pi/digits-recovery
mkdir -p bin
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/digits-recovery .

info "Binaries ready in tools/build/"

# An image without firmware is a regression we don't ship: prefer the
# host-staged ELF (make stage-firmware), fall back to GitHub release,
# die if neither is available.
FW_ELF=/digits/tools/build/firmware.elf
FW_VER_FILE=/digits/tools/build/firmware.elf.version
if [[ -f "$FW_ELF" ]]; then
    if [[ -f "$FW_VER_FILE" ]]; then
        info "Using host-staged Pico firmware ($(tr -d '[:space:]' < "$FW_VER_FILE"))"
    else
        info "Using host-staged Pico firmware (no version file)"
    fi
else
    info "Downloading latest Pico firmware from GitHub releases..."
    FW_API_URL="https://api.github.com/repos/justinlindh/digits/releases"
    FW_TAG=$(curl -sf "$FW_API_URL" | python3 -c "
import json, sys
for r in json.load(sys.stdin):
    if r['tag_name'].startswith('fw/'):
        print(r['tag_name']); break
" 2>/dev/null || true)
    if [[ -z "$FW_TAG" ]]; then
        die "Could not determine latest fw/v* release tag from GitHub. Run 'make stage-firmware' first or check network."
    fi
    FW_VERSION="${FW_TAG#fw/v}"
    FW_DOWNLOAD_URL="https://github.com/justinlindh/digits/releases/download/${FW_TAG}/firmware-${FW_VERSION}.elf"
    info "  Downloading firmware ${FW_VERSION}..."
    if ! curl -sfL -o "$FW_ELF" "$FW_DOWNLOAD_URL"; then
        die "Failed to download firmware from $FW_DOWNLOAD_URL"
    fi
    printf '%s\n' "$FW_VERSION" > "$FW_VER_FILE"
    info "  Firmware downloaded: tools/build/firmware.elf ($FW_VERSION)"
fi
[[ -f "$FW_ELF" ]] || die "Firmware ELF still missing at $FW_ELF after fetch"

# Run the image builder in a working directory, then copy output back
BUILD_WD="/build"
mkdir -p "$BUILD_WD"
cd "$BUILD_WD"
bash /digits/tools/build-image.sh ${BUILD_FLAGS[@]+"${BUILD_FLAGS[@]}"} "$SOURCE_IMAGE"

# Copy the output image to the mounted repo (accessible from host)
cp -v "$BUILD_WD"/digits-pi-*.img.gz /digits/
