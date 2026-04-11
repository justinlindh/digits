#!/usr/bin/env bash
# flash-pico.sh — Flash RP2040 Pico via SWD from Pi Zero 2 W
# Usage: flash-pico.sh <firmware.elf>
#
# Stops digitsd (releases serial port), flashes via OpenOCD SWD,
# verifies PING/PONG, restarts digitsd.
set -euo pipefail

ELF="${1:?Usage: flash-pico.sh <firmware.elf>}"
SWD_CFG="${SWD_CFG:-/usr/local/share/digits/swd/digits-swd.cfg}"
OPENOCD="${OPENOCD:-/usr/bin/openocd}"
SERIAL_DEV="${SERIAL_DEV:-/dev/serial0}"
BAUD=115200

if [ ! -f "$ELF" ]; then
    echo "ERROR: firmware file not found: $ELF" >&2
    exit 1
fi

if [ ! -f "$SWD_CFG" ]; then
    echo "ERROR: SWD config not found: $SWD_CFG" >&2
    exit 1
fi

if [ ! -x "$OPENOCD" ]; then
    echo "ERROR: openocd not found at $OPENOCD" >&2
    exit 1
fi

echo "=== Digits Pico Flash ==="
echo "Firmware: $ELF"
echo "SWD config: $SWD_CFG"

# 1. Stop digitsd to release serial port (skip if called from digitsd itself)
if [ "${SKIP_SERVICE_CONTROL:-}" != "1" ]; then
    echo "Stopping digitsd..."
    sudo systemctl stop digitsd.service 2>/dev/null || true
    sleep 1
fi

# 2. Flash via OpenOCD
echo "Flashing via SWD..."
if ! sudo "$OPENOCD" \
    -f "$SWD_CFG" \
    -f target/rp2040.cfg \
    -c "rp2040.core0 configure -event reset-init {}" \
    -c "program $ELF verify" \
    -c "reset run" \
    -c "exit"; then
    echo "ERROR: OpenOCD flash failed" >&2
    echo "Restarting digitsd anyway..."
    sudo systemctl start digitsd.service
    exit 1
fi

echo "Flash complete. Waiting for Pico to boot..."
sleep 2

# 3. Verify PING/PONG (skip if called from digitsd — it holds the serial port)
if [ "${SKIP_SERVICE_CONTROL:-}" != "1" ]; then
    echo "Verifying UART communication..."
    stty -F "$SERIAL_DEV" "$BAUD" raw -echo
    printf "PING\r\n" > "$SERIAL_DEV"
    PONG=$(timeout 3 head -c 10 < "$SERIAL_DEV" || echo "TIMEOUT")
    if echo "$PONG" | grep -q "PONG"; then
        echo "VERIFY: PASS"
    else
        echo "VERIFY: FAIL — got: $PONG"
    fi
fi

# 4. Restart digitsd (skip if called from digitsd — systemd will restart it)
if [ "${SKIP_SERVICE_CONTROL:-}" != "1" ]; then
    echo "Starting digitsd..."
    sudo systemctl start digitsd.service
fi

echo "=== Flash complete ==="
