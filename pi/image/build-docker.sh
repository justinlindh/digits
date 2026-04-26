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

# Generate embedded assets on the host (avoids root-owned files from Docker)
info "Generating embedded assets..."
make -C "$REPO_DIR/pi/digitsd" embed

# Build the Docker image
info "Building digits-image-builder Docker image..."
docker build -t digits-image-builder "$SCRIPT_DIR"

# Set up volume mounts
DOCKER_ARGS=(
    --rm --privileged
    -v "$REPO_DIR":/digits
    -v "$CACHE_VOLUME":/cache
    -w /digits
)

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
