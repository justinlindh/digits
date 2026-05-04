#!/usr/bin/env bash
# build-docker.sh -- Build a Digits Pi SD card image using Docker
#
# Wraps the entire image build process in a privileged Docker container.
# Only requires Docker on your machine.
#
# Usage:
#   ./pi/image/build-docker.sh [--dev] [--pcb] [raspios-lite.img.xz]
#
# --pcb selects V2 carrier board (onboard TLV320AIC3104 codec, inverted hook).
# Without --pcb the build targets V1/prototype hardware (Codec Zero HAT).
#
# If no base image is provided, a known-good Raspberry Pi OS Lite image
# is downloaded automatically and cached in a Docker volume for reuse.
#
# Default: downloads pre-built binaries from the latest pi/v* GitHub release.
# No Go toolchain required. Pin a specific release with RELEASE_TAG:
#
#   RELEASE_TAG=pi/v1.21.0 ./pi/image/build-docker.sh --pcb
#
# Local build mode: set BUILD_LOCAL=1 to cross-compile from the local working
# tree instead of downloading release artifacts. Use this to test unreleased
# code changes.
#
#   BUILD_LOCAL=1 ./pi/image/build-docker.sh --pcb
#
# Optionally pin the firmware release with FIRMWARE_TAG=fw/v<version>.
# Without FIRMWARE_TAG, the latest fw/v* release is used.
#
# Output: digits-pi-YYYYMMDD.img.gz in the current directory.
set -euo pipefail

die()  { echo "ERROR: $*" >&2; exit 1; }
info() { echo "==> $*"; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
CACHE_VOLUME="digits-image-cache"

# Parse args
BUILD_FLAGS=()
SOURCE_IMAGE=""
for arg in "$@"; do
    case "$arg" in
        --dev|--pcb)
            BUILD_FLAGS+=("$arg")
            ;;
        --*)
            die "Unknown flag: $arg"
            ;;
        *)
            SOURCE_IMAGE="$arg"
            ;;
    esac
done

# Generate embedded assets on the host (avoids root-owned files from Docker).
# make embed populates rootfs overlay, tones, and mixer state regardless of
# build mode. In release mode, the container overwrites the Go binaries with
# downloaded release artifacts before build-image.sh uses them.
info "Generating embedded assets..."
make -C "$REPO_DIR/pi/digitsd" embed

# Build the Docker image
info "Building digits-image-builder Docker image..."
docker build -t digits-image-builder "$SCRIPT_DIR"

# Set up volume mounts. Pass the host UID/GID so entrypoint.sh can chown
# the artifacts it writes back to the bind-mounted repo (tools/build/,
# pi/digits-recovery/bin/, the output .img.gz). Without this, subsequent
# host-side builds hit Permission denied when overwriting them.
DOCKER_ARGS=(
    --rm --privileged
    -v "$REPO_DIR":/digits
    -v "$CACHE_VOLUME":/cache
    -w /digits
    -e "HOST_UID=$(id -u)"
    -e "HOST_GID=$(id -g)"
)

# Pass build mode and release tags into the container when set.
[[ -n "${BUILD_LOCAL:-}" ]]   && DOCKER_ARGS+=(-e "BUILD_LOCAL=${BUILD_LOCAL}")
[[ -n "${RELEASE_TAG:-}" ]]   && DOCKER_ARGS+=(-e "RELEASE_TAG=${RELEASE_TAG}")
[[ -n "${FIRMWARE_TAG:-}" ]]  && DOCKER_ARGS+=(-e "FIRMWARE_TAG=${FIRMWARE_TAG}")

# Release mode needs gh CLI auth inside the container. If a GITHUB_TOKEN is
# present in the environment, forward it; otherwise gh will use the host's
# stored credential via the mounted repo's config (no extra setup needed for
# public releases).
[[ -n "${GITHUB_TOKEN:-}" ]]  && DOCKER_ARGS+=(-e "GITHUB_TOKEN=${GITHUB_TOKEN}")

# If this is a git worktree, .git is a file pointing to the main repo's
# .git/worktrees/ directory. Mount the main repo's .git so the pointer
# resolves inside the container.
GIT_PATH="$REPO_DIR/.git"
if [[ -f "$GIT_PATH" ]]; then
    MAIN_GIT_DIR=$(sed -n 's/^gitdir: //p' "$GIT_PATH")
    # Resolve to the top-level .git directory (strip /worktrees/<name>)
    MAIN_GIT_DIR="${MAIN_GIT_DIR%/worktrees/*}"
    if [[ -d "$MAIN_GIT_DIR" ]]; then
        info "Worktree detected -- mounting main .git for container"
        DOCKER_ARGS+=(-v "$MAIN_GIT_DIR":"$MAIN_GIT_DIR":ro)
    fi
fi
ENTRYPOINT_ARGS=()
(( ${#BUILD_FLAGS[@]} > 0 )) && ENTRYPOINT_ARGS+=("${BUILD_FLAGS[@]}")

if [[ -n "$SOURCE_IMAGE" ]]; then
    # User provided a base image -- mount its directory
    if [[ "$SOURCE_IMAGE" != /* ]]; then
        SOURCE_IMAGE="$(pwd)/$SOURCE_IMAGE"
    fi
    [[ -f "$SOURCE_IMAGE" ]] || die "Image not found: $SOURCE_IMAGE"

    IMAGE_DIR="$(dirname "$SOURCE_IMAGE")"
    IMAGE_NAME="$(basename "$SOURCE_IMAGE")"
    DOCKER_ARGS+=(-v "$IMAGE_DIR":/images)
    ENTRYPOINT_ARGS+=("/images/$IMAGE_NAME")
fi
# If no SOURCE_IMAGE, entrypoint.sh downloads to /cache automatically

info "Starting image build (privileged container)..."
docker run "${DOCKER_ARGS[@]}" digits-image-builder "${ENTRYPOINT_ARGS[@]}"

info "Done! Output image is in the current directory."
