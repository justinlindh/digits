#!/usr/bin/env bash
# pico-shell.sh: open an interactive UART terminal to the RP2040 on a
# Digits phone, from your dev machine.
#
# Stops digitsd on the phone, drops you into minicom over SSH, restarts
# digitsd when you exit minicom (Ctrl-A then X).
#
# Usage:
#   tools/pico-shell.sh <phone-ip>
#   tools/pico-shell.sh                    # uses PHONE_IP env var
#   PHONE_IP=192.168.2.229 tools/pico-shell.sh
#
# Default credentials are dev/digits (per the Digits dev image). Override
# with PHONE_USER / PHONE_PASS env vars if needed.
set -euo pipefail

PHONE_IP="${1:-${PHONE_IP:-}}"
PHONE_USER="${PHONE_USER:-dev}"
PHONE_PASS="${PHONE_PASS:-digits}"

if [[ -z "$PHONE_IP" ]]; then
    echo "Usage: $0 <phone-ip>" >&2
    echo "   or: PHONE_IP=<ip> $0" >&2
    exit 1
fi

if ! command -v sshpass >/dev/null 2>&1; then
    echo "ERROR: sshpass not installed (apt install sshpass)" >&2
    exit 1
fi

REMOTE_TOOL="${REMOTE_TOOL:-/usr/local/bin/digits-pico-monitor}"

exec sshpass -p "$PHONE_PASS" ssh -t \
    -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null \
    "${PHONE_USER}@${PHONE_IP}" \
    "sudo $REMOTE_TOOL"
