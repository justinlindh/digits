#!/usr/bin/env bash
# build-docker.sh — Build a Digits Pi SD card image using Docker
#
# This wraps the entire image build process in a privileged Docker
# container so you don't need to install qemu, parted, cross-compilers,
# or any other dependencies on your host machine.
#
# Prerequisites: Docker
#
# Usage:
#   ./pi/image/build-docker.sh [--dev] <raspios-lite.img.xz>
#
# The base image file must be in the current directory or an absolute path.
# Output: digits-pi-YYYYMMDD.img.gz in the current directory.
set -euo pipefail

die()  { echo "ERROR: $*" >&2; exit 1; }
info() { echo "==> $*"; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Parse args
DEV_FLAG=""
ARGS=()
for arg in "$@"; do
    if [[ "$arg" == "--dev" ]]; then
        DEV_FLAG="--dev"
    else
        ARGS+=("$arg")
    fi
done

SOURCE_IMAGE="${ARGS[0]:-}"
[[ -n "$SOURCE_IMAGE" ]] || die "Usage: $0 [--dev] <raspios-lite.img|.img.xz>"

# Resolve to absolute path
if [[ "$SOURCE_IMAGE" != /* ]]; then
    SOURCE_IMAGE="$(pwd)/$SOURCE_IMAGE"
fi
[[ -f "$SOURCE_IMAGE" ]] || die "Image not found: $SOURCE_IMAGE"

IMAGE_DIR="$(dirname "$SOURCE_IMAGE")"
IMAGE_NAME="$(basename "$SOURCE_IMAGE")"

# Build the Docker image
info "Building digits-image-builder Docker image..."
docker build -t digits-image-builder "$SCRIPT_DIR"

# Run the build
info "Starting image build (privileged container)..."
docker run --rm --privileged \
    -v "$REPO_DIR":/digits \
    -v "$IMAGE_DIR":/images \
    -w /digits \
    digits-image-builder \
    $DEV_FLAG "/images/$IMAGE_NAME"

info "Done! Output image is in the current directory."
