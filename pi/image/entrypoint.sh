#!/usr/bin/env bash
# entrypoint.sh -- Docker entrypoint for Digits Pi image builder
#
# Provides two modes for sourcing Pi binaries:
#
#   Default (release mode):
#     Downloads pre-built binaries from the latest pi/v* GitHub release.
#     Produces clean production images without requiring the Go cross-compile
#     toolchain. Pin a specific release with RELEASE_TAG=pi/v<version>.
#
#     Example: RELEASE_TAG=pi/v1.21.0 ./pi/image/build-docker.sh --pcb
#
#     The firmware is sourced from its own release (FIRMWARE_TAG=fw/v<version>).
#     If FIRMWARE_TAG is not set, the latest fw/v* release is used.
#
#   Local build mode (BUILD_LOCAL=1):
#     Cross-compiles digitsd from the mounted repo.
#     Use this to test unreleased code changes.
#
#     Example: BUILD_LOCAL=1 ./pi/image/build-docker.sh --pcb
#
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

mkdir -p /digits/tools/build

# ── binary sourcing: release mode (default) vs local build ───────────────────
#
# Default: download pre-built binaries from the latest pi/v* GitHub release.
# Pin a specific tag with RELEASE_TAG=pi/v<version>.
# Set BUILD_LOCAL=1 to cross-compile from the local working tree instead.

if [[ -z "${BUILD_LOCAL:-}" ]]; then
    # Release mode: download pre-built binaries from GitHub Releases.

    # Resolve the release tag: use RELEASE_TAG if set, otherwise auto-detect
    # the latest pi/v* release from GitHub.
    if [[ -n "${RELEASE_TAG:-}" ]]; then
        PI_TAG="${RELEASE_TAG}"
        info "Release mode: downloading Pi binaries from GitHub release ${PI_TAG}..."

        # Verify the tag exists before proceeding.
        if ! gh release view "${PI_TAG}" --repo justinlindh/digits &>/dev/null; then
            die "GitHub release '${PI_TAG}' not found. Check RELEASE_TAG and try again."
        fi
    else
        info "Resolving latest Pi release from GitHub..."
        PI_TAG=$(gh release list --repo justinlindh/digits --limit 50 --json tagName \
            --jq '[.[].tagName | select(startswith("pi/"))] | first' 2>/dev/null || true)
        if [[ -z "$PI_TAG" ]]; then
            die "Could not determine latest pi/v* release tag from GitHub. Set RELEASE_TAG=pi/v<version> or BUILD_LOCAL=1."
        fi
        info "Release mode: downloading Pi binaries from latest release ${PI_TAG}..."
    fi

    # Resolve the version string from the tag (e.g. pi/v1.21.0 -> 1.21.0).
    PI_VERSION="${PI_TAG#pi/v}"
    # Export for build-image.sh so it writes the correct asset-version marker.
    export DIGITS_PI_VERSION="${PI_VERSION}"

    info "  Downloading digitsd-${PI_VERSION}-aarch64..."
    gh release download "${PI_TAG}" \
        --repo justinlindh/digits \
        --pattern "digitsd-${PI_VERSION}-aarch64" \
        --output /digits/tools/build/digitsd
    chmod +x /digits/tools/build/digitsd

    info "Pi binaries downloaded from ${PI_TAG}."
else
    # Local build mode: cross-compile from the mounted repo.

    # Cross-compile digitsd
    info "Cross-compiling digitsd for aarch64..."
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
    # Export for build-image.sh so it writes the correct asset-version marker.
    export DIGITS_PI_VERSION="${DIGITSD_VERSION}"
    info "Stamping digitsd: version=$DIGITSD_VERSION commit=$DIGITSD_COMMIT"
    GOOS=linux GOARCH=arm64 go build \
        -ldflags "-X github.com/justinlindh/digits/pi/digitsd/internal/version.Version=$DIGITSD_VERSION \
                  -X github.com/justinlindh/digits/pi/digitsd/internal/version.Commit=$DIGITSD_COMMIT" \
        -o /digits/tools/build/digitsd \
        ./cmd/digitsd/

    info "Binaries ready in tools/build/"
fi

# ── firmware: host-staged, release download, or error ────────────────────────
#
# An image without firmware is a regression we don't ship. Priority:
#   1. Host-staged ELF (make stage-firmware, or release mode put it there).
#   2. FIRMWARE_TAG env var: download from that specific fw/v* release.
#   3. Auto-detect: download from the latest fw/v* release on GitHub.
#   4. Die if none of the above succeed.

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
    if [[ -n "${FIRMWARE_TAG:-}" ]]; then
        # Explicit firmware release tag supplied.
        FW_TAG="${FIRMWARE_TAG}"
        if ! gh release view "${FW_TAG}" --repo justinlindh/digits &>/dev/null; then
            die "GitHub release '${FW_TAG}' not found. Check FIRMWARE_TAG and try again."
        fi
    else
        # Auto-detect the latest fw/v* release.
        info "Resolving latest Pico firmware release from GitHub..."
        FW_TAG=$(gh release list --repo justinlindh/digits --limit 50 --json tagName \
            --jq '[.[].tagName | select(startswith("fw/"))] | first' 2>/dev/null || true)
        if [[ -z "$FW_TAG" ]]; then
            die "Could not determine latest fw/v* release tag from GitHub. Run 'make stage-firmware' first or set FIRMWARE_TAG."
        fi
    fi

    FW_VERSION="${FW_TAG#fw/v}"
    info "Downloading Pico firmware ${FW_VERSION} from GitHub release ${FW_TAG}..."
    gh release download "${FW_TAG}" \
        --repo justinlindh/digits \
        --pattern "firmware-${FW_VERSION}.elf" \
        --output "$FW_ELF"
    printf '%s\n' "$FW_VERSION" > "$FW_VER_FILE"
    info "Firmware downloaded: tools/build/firmware.elf ($FW_VERSION)"
fi
[[ -f "$FW_ELF" ]] || die "Firmware ELF still missing at $FW_ELF after fetch"

# Run the image builder in a working directory, then copy output back
BUILD_WD="/build"
mkdir -p "$BUILD_WD"
cd "$BUILD_WD"
bash /digits/tools/build-image.sh ${BUILD_FLAGS[@]+"${BUILD_FLAGS[@]}"} "$SOURCE_IMAGE"

# Copy the output image to the mounted repo (accessible from host)
cp -v "$BUILD_WD"/digits-pi-*.img.gz /digits/

# Hand artifacts back to the host UID. The container runs as root for
# loop mounts and parted, so anything written under the bind-mounted
# /digits ends up root-owned. That breaks the next host-side make run
# (e.g. stage-firmware's cp into tools/build/). build-docker.sh passes
# HOST_UID/HOST_GID so we can fix it here.
if [[ -n "${HOST_UID:-}" && -n "${HOST_GID:-}" ]]; then
    info "Restoring host ownership of build artifacts (${HOST_UID}:${HOST_GID})..."
    chown -R "${HOST_UID}:${HOST_GID}" \
        /digits/tools/build \
        2>/dev/null || true
    chown "${HOST_UID}:${HOST_GID}" /digits/digits-pi-*.img.gz 2>/dev/null || true
fi
