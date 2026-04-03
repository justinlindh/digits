#!/usr/bin/env bash
# build.sh — CMake build helper for Digits firmware
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
FIRMWARE_DIR="$SCRIPT_DIR/../firmware"
BUILD_DIR="$FIRMWARE_DIR/build"

# Auto-find PICO_SDK_PATH if not set
if [ -z "${PICO_SDK_PATH:-}" ]; then
    for candidate in \
        "$HOME/pico-sdk" \
        "$HOME/src/pico-sdk" \
        "/usr/share/pico-sdk" \
        "/opt/pico-sdk"; do
        if [ -d "$candidate" ] && [ -f "$candidate/pico_sdk_init.cmake" ]; then
            export PICO_SDK_PATH="$candidate"
            echo "Auto-detected PICO_SDK_PATH=$PICO_SDK_PATH"
            break
        fi
    done
fi

if [ -z "${PICO_SDK_PATH:-}" ]; then
    echo "ERROR: PICO_SDK_PATH not set and could not be auto-detected." >&2
    echo "Set PICO_SDK_PATH to your pico-sdk checkout." >&2
    exit 1
fi

echo "Building Digits firmware..."
echo "  PICO_SDK_PATH=$PICO_SDK_PATH"
echo "  Build dir: $BUILD_DIR"

mkdir -p "$BUILD_DIR"
cd "$BUILD_DIR"
cmake .. -DPICO_SDK_PATH="$PICO_SDK_PATH"
make -j"$(nproc)"

echo ""
echo "Build complete. UF2 output: $BUILD_DIR/digits.uf2"
