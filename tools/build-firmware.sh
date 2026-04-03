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

echo "=== Building Pico firmware v${VERSION} (commit ${GIT_COMMIT}) ==="

cd "${REPO_DIR}/firmware"
rm -rf build
mkdir build && cd build

cmake .. \
    -DDIGITS_VERSION="${VERSION}" \
    -DDIGITS_COMMIT="${GIT_COMMIT}"
make -j"$(nproc)"

# Support either digits.elf or firmware.elf output name
if [ -f digits.elf ]; then
    cp digits.elf "${OUT_DIR}/firmware.elf"
elif [ -f firmware.elf ]; then
    cp firmware.elf "${OUT_DIR}/firmware.elf"
else
    echo "ERROR: Could not find firmware ELF output" >&2
    ls -la .
    exit 1
fi

sha256sum "${OUT_DIR}/firmware.elf" | awk '{print $1}' > "${OUT_DIR}/firmware.elf.sha256"

echo "Built: ${OUT_DIR}/firmware.elf"
echo "SHA256: $(cat "${OUT_DIR}/firmware.elf.sha256")"
