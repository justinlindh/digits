#!/usr/bin/env bash
# flash.sh — Copy UF2 firmware to RP2040 in BOOTSEL mode
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
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
