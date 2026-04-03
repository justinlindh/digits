#!/usr/bin/env bash
# tools/build-pi.sh — Cross-compile digitsd for aarch64
# Usage: tools/build-pi.sh <version>
# Outputs: artifacts/digitsd-aarch64, artifacts/digitsd-aarch64.sha256
set -euo pipefail

VERSION="${1:?Usage: build-pi.sh <version>}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(dirname "$SCRIPT_DIR")"
OUT_DIR="${REPO_DIR}/artifacts"

mkdir -p "$OUT_DIR"

GIT_COMMIT="$(git -C "$REPO_DIR" rev-parse --short HEAD)"

echo "=== Building digitsd aarch64 v${VERSION} (commit ${GIT_COMMIT}) ==="

cd "${REPO_DIR}/pi/digitsd"
export PKG_CONFIG_PATH="/usr/lib/aarch64-linux-gnu/pkgconfig"
CGO_ENABLED=1 CC=aarch64-linux-gnu-gcc GOOS=linux GOARCH=arm64 go build \
    -ldflags "-X github.com/justinlindh/digits/pi/digitsd/internal/version.Version=${VERSION} \
              -X github.com/justinlindh/digits/pi/digitsd/internal/version.Commit=${GIT_COMMIT}" \
    -o "${OUT_DIR}/digitsd-aarch64" \
    ./cmd/digitsd/

sha256sum "${OUT_DIR}/digitsd-aarch64" | awk '{print $1}' > "${OUT_DIR}/digitsd-aarch64.sha256"

echo "Built: ${OUT_DIR}/digitsd-aarch64"
echo "SHA256: $(cat "${OUT_DIR}/digitsd-aarch64.sha256")"
