#!/usr/bin/env bash
# tools/build-firmware.sh — Build Pico firmware
# Usage: tools/build-firmware.sh <version>
# Outputs: artifacts/firmware.elf, artifacts/firmware.elf.sha256
# Requires: arm-none-eabi-gcc, cmake, Pico SDK (PICO_SDK_PATH)
set -euo pipefail

VERSION="${1:?Usage: build-firmware.sh <version>}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(dirname "$SCRIPT_DIR")"
OUT_DIR="${REPO_DIR}/artifacts"

mkdir -p "$OUT_DIR"

GIT_COMMIT="$(git -C "$REPO_DIR" rev-parse --short HEAD)"

# Same helper pattern as tools/build-pi.sh: write <file>.sha256 and echo the
# digest. Keep the two in sync if either changes.
sha_file() {
    sha256sum "$1" | awk '{print $1}' > "$1.sha256"
    echo "Built: $1"
    echo "SHA256: $(cat "$1.sha256")"
}

echo "=== Building Pico firmware v${VERSION} (commit ${GIT_COMMIT}) ==="

cd "${REPO_DIR}/firmware"
rm -rf build
mkdir build && cd build

cmake .. \
    -DDIGITS_VERSION="${VERSION}" \
    -DDIGITS_COMMIT="${GIT_COMMIT}"
make -j"$(nproc)"

ARTIFACT="firmware-${VERSION}.elf"

# Support either digits.elf or firmware.elf output name
if [ -f digits.elf ]; then
    cp digits.elf "${OUT_DIR}/${ARTIFACT}"
elif [ -f firmware.elf ]; then
    cp firmware.elf "${OUT_DIR}/${ARTIFACT}"
else
    echo "ERROR: Could not find firmware ELF output" >&2
    ls -la .
    exit 1
fi

sha_file "${OUT_DIR}/${ARTIFACT}"
