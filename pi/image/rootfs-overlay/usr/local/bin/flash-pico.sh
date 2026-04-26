#!/usr/bin/env bash
# flash-pico.sh: Flash RP2040 via SWD from Pi Zero 2 W (V1 Pico H or V2 carrier).
# Usage: flash-pico.sh <firmware.elf>
#
# Stops digitsd (releases serial port), flashes via OpenOCD SWD,
# verifies PING/PONG, restarts digitsd.
#
# Flash recipe notes (learned the hard way during V2 bring-up):
# 1. FLASHSIZE override: OpenOCD 0.12 sometimes fails to read SFDP / JEDEC ID
#    from the W25Q16JV (returns 0x000000), which kills the auto-probe path
#    even when the chip is otherwise fine. Setting FLASHSIZE=0x200000 skips
#    that detection and uses the known size directly.
# 2. RESCUE-mode pre-pass: a virgin chip (or one that fell off SWD because
#    its boot2 stage looped) gets the cores stuck in reset, and SWD WAITs
#    forever. RESCUE mode uses DBGPWRUPREQ to force the bootrom to halt
#    before jumping to flash, which gives us a clean window to reflash.
#    We always do a RESCUE pass on flash failure, then retry.
set -euo pipefail

ELF="${1:?Usage: flash-pico.sh <firmware.elf>}"
SWD_CFG="${SWD_CFG:-/usr/local/share/digits/swd/digits-swd.cfg}"
OPENOCD="${OPENOCD:-/usr/bin/openocd}"
SERIAL_DEV="${SERIAL_DEV:-/dev/serial0}"
BAUD=115200
FLASHSIZE="${FLASHSIZE:-0x200000}"

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

flash_attempt() {
    sudo "$OPENOCD" \
        -c "set FLASHSIZE $FLASHSIZE" \
        -c "set USE_CORE 0" \
        -f "$SWD_CFG" \
        -f target/rp2040.cfg \
        -c "init; reset halt; program $ELF verify; reset run; exit"
}

rescue_pass() {
    echo "Running RESCUE pass to recover stuck bootrom state..."
    sudo "$OPENOCD" \
        -c "set RESCUE 1" \
        -f "$SWD_CFG" \
        -f target/rp2040.cfg \
        -c "init; exit" 2>&1 | tail -5 || true
}

# Try to soft-reboot the chip via the firmware's REBOOT command. When the
# current firmware supports it, the chip enters bootrom for ~1 ms, openocd
# can then halt the cores cleanly without needing a physical power cycle.
# Older firmware (or no firmware at all) just ignores the bytes; we still
# fall through to the RESCUE pre-pass below.
firmware_reboot() {
    if [ ! -c "$SERIAL_DEV" ]; then
        return
    fi
    echo "Sending REBOOT over $SERIAL_DEV to soft-reset the chip..."
    stty -F "$SERIAL_DEV" "$BAUD" raw -echo 2>/dev/null || true
    # Small flush attempt so any partial output the firmware was producing
    # doesn't get mixed in.
    timeout 0.2 cat "$SERIAL_DEV" >/dev/null 2>&1 || true
    printf "REBOOT\r\n" > "$SERIAL_DEV" 2>/dev/null || true
    # Wait for the watchdog to actually kick (50 ms uart flush + reset).
    sleep 0.3
}

# 2. Flash via OpenOCD with rebooted-chip + rescue-on-failure
echo "Flashing via SWD..."
firmware_reboot
if ! flash_attempt; then
    echo "First attempt failed. Trying RESCUE recovery and retry..."
    rescue_pass
    sleep 1
    if ! flash_attempt; then
        echo "ERROR: OpenOCD flash failed even after RESCUE." >&2
        echo "Restarting digitsd anyway..."
        if [ "${SKIP_SERVICE_CONTROL:-}" != "1" ]; then
            sudo systemctl start digitsd.service
        fi
        exit 1
    fi
fi

echo "Flash complete. Waiting for Pico to boot..."
sleep 2

# 3. Write PCB rev marker to flash so firmware can pick the right board profile.
# The byte at 0x101FF000 is read by the firmware at boot to choose V1 or V2.
# Without it, the firmware falls back to the V2 profile.
PCB_REV_FILE=/etc/digits-pcb-rev
PCB_REV_ADDR=0x101FF000

if [ -f "$PCB_REV_FILE" ]; then
    PCB_REV=$(tr -d '[:space:]' < "$PCB_REV_FILE")
    if [ -n "$PCB_REV" ]; then
        # Map ASCII char to hex value for openocd flash filld.
        # shellcheck disable=SC2016,SC2027,SC2086
        PCB_REV_HEX=$(printf "0x%02X" "'$PCB_REV")
        echo "Writing PCB rev marker: '$PCB_REV' ($PCB_REV_HEX) at $PCB_REV_ADDR"
        sudo "$OPENOCD" \
            -c "set FLASHSIZE $FLASHSIZE" \
            -c "set USE_CORE 0" \
            -f "$SWD_CFG" \
            -f target/rp2040.cfg \
            -c "init; reset halt; flash erase_address $PCB_REV_ADDR 0x1000; flash filld $PCB_REV_ADDR $PCB_REV_HEX 1; reset run; shutdown" \
            2>&1 | tail -5
        echo "Rev marker written. Waiting for Pico to boot..."
        sleep 2
    else
        echo "WARNING: $PCB_REV_FILE is empty; skipping rev marker write"
    fi
else
    echo "WARNING: $PCB_REV_FILE not found; skipping rev marker write"
fi

# 4. Verify PING/PONG (skip if called from digitsd: it holds the serial port)
if [ "${SKIP_SERVICE_CONTROL:-}" != "1" ]; then
    echo "Verifying UART communication..."
    stty -F "$SERIAL_DEV" "$BAUD" raw -echo
    printf "PING\r\n" > "$SERIAL_DEV"
    PONG=$(timeout 3 head -c 10 < "$SERIAL_DEV" || echo "TIMEOUT")
    if echo "$PONG" | grep -q "PONG"; then
        echo "VERIFY: PASS"
    else
        echo "VERIFY: FAIL, got: $PONG"
    fi
fi

# 5. Restart digitsd (skip if called from digitsd; systemd will restart it)
if [ "${SKIP_SERVICE_CONTROL:-}" != "1" ]; then
    echo "Starting digitsd..."
    sudo systemctl start digitsd.service
fi

echo "=== Flash complete ==="
