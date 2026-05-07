#!/usr/bin/env bash
# tools/build-pi.sh - Cross-compile Pi binaries for aarch64
# Usage: tools/build-pi.sh <version>
# Outputs (in artifacts/):
#   digitsd-<version>-aarch64            (and .sha256)
#   digits-panic-check-<version>-aarch64 (and .sha256)
set -euo pipefail

VERSION="${1:?Usage: build-pi.sh <version>}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(dirname "$SCRIPT_DIR")"
OUT_DIR="${REPO_DIR}/artifacts"

mkdir -p "$OUT_DIR"

GIT_COMMIT="$(git -C "$REPO_DIR" rev-parse --short HEAD)"

sha_file() {
    sha256sum "$1" | awk '{print $1}' > "$1.sha256"
    echo "Built: $1"
    echo "SHA256: $(cat "$1.sha256")"
}

# digitsd (CGO, requires cross-compiler)

echo "=== Building digitsd aarch64 v${VERSION} (commit ${GIT_COMMIT}) ==="

cd "${REPO_DIR}/pi/digitsd"

# Populate the embed tree so the //go:embed directive sees actual files
# rather than just the committed .gitkeep. Without this, the binary ships
# with an effectively empty assets FS and digitsd's asset extractor becomes
# a no-op, leaving stale files on disk after every OTA update. This also
# cross-compiles digits-setup into the embed tree.
make embed

export PKG_CONFIG_PATH="/usr/lib/aarch64-linux-gnu/pkgconfig"
CGO_ENABLED=1 CC=aarch64-linux-gnu-gcc GOOS=linux GOARCH=arm64 go build \
    -ldflags "-X github.com/justinlindh/digits/pi/digitsd/internal/version.Version=${VERSION} \
              -X github.com/justinlindh/digits/pi/digitsd/internal/version.Commit=${GIT_COMMIT}" \
    -o "${OUT_DIR}/digitsd-${VERSION}-aarch64" \
    ./cmd/digitsd/
sha_file "${OUT_DIR}/digitsd-${VERSION}-aarch64"

# digits-panic-check (pure Go)

echo "=== Building digits-panic-check aarch64 v${VERSION} (commit ${GIT_COMMIT}) ==="

cd "${REPO_DIR}/pi/digits-panic-check"
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
    -o "${OUT_DIR}/digits-panic-check-${VERSION}-aarch64" \
    .
sha_file "${OUT_DIR}/digits-panic-check-${VERSION}-aarch64"
