#!/usr/bin/env bash
# Interactive UART terminal to a Pico via the digitsd Unix socket.
# Usage: uart-term.sh <ip>
set -euo pipefail

if [ -z "${1:-}" ]; then
    echo "Usage: uart-term.sh <ip>" >&2
    exit 1
fi
HOST="dev@$1"
SOCK="/data/digits/uart.sock"

SCRIPT=$(cat <<'PYEOF'
import socket, sys, readline

sock_path = sys.argv[1]
print(f"Connected to {sock_path}")
print("Type UART commands (PING, STATE?, LED:ON, etc). Ctrl-C to quit.\n")

while True:
    try:
        cmd = input("uart> ").strip()
    except (EOFError, KeyboardInterrupt):
        print()
        break
    if not cmd:
        continue
    try:
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.connect(sock_path)
        s.settimeout(2)
        s.sendall((cmd + "\n").encode())
        resp = s.recv(4096).decode().strip()
        s.close()
        print(resp)
    except socket.timeout:
        print("(no response)")
    except Exception as e:
        print(f"error: {e}")
PYEOF
)

sshpass -p digits scp -q <(echo "$SCRIPT") "$HOST:/tmp/uart-term.py" 2>/dev/null \
    || sshpass -p digits ssh "$HOST" "cat > /tmp/uart-term.py" <<< "$SCRIPT"
sshpass -p digits ssh -t "$HOST" sudo python3 -u /tmp/uart-term.py "$SOCK"
