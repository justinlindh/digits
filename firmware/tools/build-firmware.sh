#!/usr/bin/env bash
# tools/build-firmware.sh — Build Pico firmware for release
# Usage: tools/build-firmware.sh <version>
# Outputs: artifacts/firmware.elf, artifacts/firmware.elf.sha256
set -euo pipefail

VERSION="${1:?Usage: build-firmware.sh <version>}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
FIRMWARE_DIR="$(dirname "$SCRIPT_DIR")"
OUT_DIR="${FIRMWARE_DIR}/artifacts"

mkdir -p "$OUT_DIR"
mkdir -p "${FIRMWARE_DIR}/build"

echo "=== Building Pico firmware v${VERSION} ==="

cd "${FIRMWARE_DIR}/build"

# Configure with CMake if not already done
if [ ! -f "Makefile" ] && [ ! -f "build.ninja" ]; then
    CMAKE_ARGS="-DCMAKE_BUILD_TYPE=Release"
    # If PICO_SDK_PATH is not set, fetch from git
    if [ -z "${PICO_SDK_PATH:-}" ] && [ ! -d "${FIRMWARE_DIR}/build/_deps/pico-sdk-src" ]; then
        CMAKE_ARGS="${CMAKE_ARGS} -DPICO_SDK_FETCH_FROM_GIT=ON"
    fi
    cmake .. ${CMAKE_ARGS}
fi

# Build
cmake --build . --parallel

# Copy artifacts
cp digits.elf "${OUT_DIR}/firmware.elf"
sha256sum "${OUT_DIR}/firmware.elf" | awk '{print $1}' > "${OUT_DIR}/firmware.elf.sha256"

echo "Built: ${OUT_DIR}/firmware.elf"
echo "SHA256: $(cat "${OUT_DIR}/firmware.elf.sha256")"
