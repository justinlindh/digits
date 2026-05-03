#!/usr/bin/env bash
# bootsel-flash.sh: detect whether the V2 RP2040 is in BOOTSEL mode and, if
# so, flash /data/digits/firmware.elf via SWD on the spot. Designed to be run
# repeatedly while you fiddle with the U4-pin-1-to-GND paperclip bootstrap:
# each invocation is 3-4 seconds, gives a clear verdict, and finishes the
# flash automatically the moment SWD comes alive.
#
# Usage:
#   tools/bootsel-flash.sh <phone-ip>
#   PHONE_IP=192.168.2.229 tools/bootsel-flash.sh
set -euo pipefail

PHONE_IP="${1:-${PHONE_IP:-}}"
PHONE_USER="${PHONE_USER:-dev}"
PHONE_PASS="${PHONE_PASS:-digits}"

if [[ -z "$PHONE_IP" ]]; then
    echo "Usage: $0 <phone-ip>" >&2
    exit 1
fi

if ! command -v sshpass >/dev/null 2>&1; then
    echo "ERROR: sshpass not installed (apt install sshpass)" >&2
    exit 1
fi

REMOTE_SCRIPT='
set -e
sudo systemctl stop digitsd 2>/dev/null || true
sleep 0.2

# Quick SWD probe (3 fast attempts). If any catches, jump straight to flash.
caught=0
for i in 1 2 3; do
    if sudo timeout 2 openocd \
        -c "set FLASHSIZE 0x200000" -c "set USE_CORE 0" \
        -f /usr/local/share/digits/swd/digits-swd.cfg \
        -f target/rp2040.cfg \
        -c "init; exit" 2>&1 | grep -q DPIDR
    then
        caught=1
        break
    fi
done

if [ "$caught" = "1" ]; then
    echo
    echo "================================================================"
    echo "  *** SWD CAUGHT *** chip is reachable, flashing now"
    echo "================================================================"
    sudo timeout 60 openocd \
        -c "set FLASHSIZE 0x200000" -c "set USE_CORE 0" \
        -f /usr/local/share/digits/swd/digits-swd.cfg \
        -f target/rp2040.cfg \
        -c "init; reset halt; program /data/digits/firmware.elf verify; reset run; exit" 2>&1 | tail -10
    echo
    sudo systemctl start digitsd 2>/dev/null || true
    exit 0
fi

# SWD silent: figure out chip state via UART
echo "================================================================"
echo "  SWD did not enumerate. Diagnosing chip state via UART..."
echo "================================================================"
sudo python3 -c "
import os, termios, select, time
fd = os.open(\"/dev/serial0\", os.O_RDWR | os.O_NOCTTY)
n = termios.tcgetattr(fd); n[0]=0; n[1]=0; n[3]=0
n[2] = termios.CS8 | termios.CREAD | termios.CLOCAL
b = termios.B115200
termios.tcsetattr(fd, termios.TCSANOW, [n[0], n[1], n[2]|b, n[3], b, b, n[6]])
termios.tcflush(fd, termios.TCIOFLUSH)
time.sleep(0.2)
os.write(fd, b\"PING\r\n\"); time.sleep(0.3)
g = b\"\"
while select.select([fd],[],[],0)[0]: g += os.read(fd, 256)
print(f\"  PING returned: {g!r}\")
if b\"PONG\" in g:
    print(\"  --> chip is running FIRMWARE. Bootstrap did not take.\")
    print(\"      Try the U4-pin-1 to GND paperclip again, hold steady.\")
elif g == b\"PING\\r\\n\" or (b\"PING\" in g and b\"PONG\" not in g):
    print(\"  --> UART returned what we sent (crosstalk loopback).\")
    print(\"      Chip seems to be in BOOTSEL but SWD is not enumerating.\")
    print(\"      That is unusual. Try power-cycling and bootstrapping again.\")
elif g == b\"\":
    print(\"  --> No UART response at all. Chip may be in fault or partial\")
    print(\"      bootstrap. Try power-cycling and bootstrapping again.\")
else:
    print(f\"  --> Unexpected response. Investigate.\")
os.close(fd)
"
sudo systemctl start digitsd 2>/dev/null || true
'

exec sshpass -p "$PHONE_PASS" ssh \
    -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null \
    "${PHONE_USER}@${PHONE_IP}" "$REMOTE_SCRIPT"
