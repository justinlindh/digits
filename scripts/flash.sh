#!/usr/bin/env bash
# flash.sh — Flash Digits firmware to an RP2040
#
# Usage:
#   flash.sh                        Flash local UF2 build (BOOTSEL mount)
#   flash.sh --release <version>    Download + flash a GitHub release ELF
#   flash.sh [mount-point]          Flash local UF2 build to specific mount
#
# Examples:
#   flash.sh --release 1.0.1
#   flash.sh
#   flash.sh /media/user/RPI-RP2
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="justinlindh/digits"

# --- Release mode: download ELF from GitHub and flash via picotool ---
if [ "${1:-}" = "--release" ]; then
    VERSION="${2:?Usage: flash.sh --release <version>}"

    if ! command -v picotool &>/dev/null; then
        echo "ERROR: picotool is required for flashing release ELFs." >&2
        echo "Install it: https://github.com/raspberrypi/picotool" >&2
        exit 1
    fi

    if ! command -v gh &>/dev/null; then
        echo "ERROR: gh (GitHub CLI) is required for downloading releases." >&2
        exit 1
    fi

    TAG="fw/v${VERSION}"
    ELF_NAME="firmware-${VERSION}.elf"
    SHA_NAME="${ELF_NAME}.sha256"

    TMPDIR="$(mktemp -d)"
    trap 'rm -rf "$TMPDIR"' EXIT

    echo "Downloading $ELF_NAME from release $TAG..."
    gh release download "$TAG" \
        --repo "$REPO" \
        --pattern "$ELF_NAME" \
        --pattern "$SHA_NAME" \
        --dir "$TMPDIR"

    echo "Verifying checksum..."
    EXPECTED="$(cat "$TMPDIR/$SHA_NAME")"
    ACTUAL="$(sha256sum "$TMPDIR/$ELF_NAME" | cut -d' ' -f1)"
    if [ "$EXPECTED" != "$ACTUAL" ]; then
        echo "ERROR: Checksum mismatch!" >&2
        echo "  Expected: $EXPECTED" >&2
        echo "  Got:      $ACTUAL" >&2
        exit 1
    fi
    echo "  OK ($ACTUAL)"

    echo "Flashing via picotool..."
    picotool load -f -v -x "$TMPDIR/$ELF_NAME"
    echo "Flash complete."
    exit 0
fi

# --- Local mode: copy UF2 to BOOTSEL mount ---
UF2_FILE="$SCRIPT_DIR/../firmware/build/digits.uf2"

if [ ! -f "$UF2_FILE" ]; then
    echo "ERROR: UF2 file not found at $UF2_FILE" >&2
    echo "Run scripts/build.sh first." >&2
    exit 1
fi

# Find the RPI-RP2 mount point: explicit arg > auto-detect > fail
if [ -n "${1:-}" ]; then
    MOUNT_POINT="$1"
else
    # Common mount locations across distros and macOS
    CANDIDATES=(
        "/media/$USER/RPI-RP2"       # Ubuntu / Debian
        "/run/media/$USER/RPI-RP2"   # Arch / Fedora
        "/Volumes/RPI-RP2"           # macOS
    )
    MOUNT_POINT=""
    for candidate in "${CANDIDATES[@]}"; do
        if [ -d "$candidate" ]; then
            MOUNT_POINT="$candidate"
            break
        fi
    done

    # Fallback: search /media and /run/media for any RPI-RP2
    if [ -z "$MOUNT_POINT" ]; then
        MOUNT_POINT="$(find /media /run/media /Volumes 2>/dev/null -maxdepth 3 -type d -name "RPI-RP2" | head -1)"
    fi

    if [ -z "$MOUNT_POINT" ]; then
        echo "ERROR: RPI-RP2 mount not found." >&2
        echo "Hold BOOTSEL and plug in the Pico, then retry." >&2
        echo "Usage: $0 [mount-point]" >&2
        exit 1
    fi
fi

if [ ! -d "$MOUNT_POINT" ]; then
    echo "ERROR: Mount point not found: $MOUNT_POINT" >&2
    echo "Hold BOOTSEL and plug in the Pico, then retry." >&2
    exit 1
fi

echo "Flashing $UF2_FILE → $MOUNT_POINT ..."
cp "$UF2_FILE" "$MOUNT_POINT/"
sync
echo "Flash complete. Pico will reboot automatically."
