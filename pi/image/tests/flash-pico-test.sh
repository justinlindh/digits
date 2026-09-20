#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
SCRIPT="$SCRIPT_DIR/../rootfs-overlay/usr/local/bin/flash-pico.sh"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

mkdir -p "$TMP/bin"
touch "$TMP/firmware.elf" "$TMP/swd.cfg"

cat >"$TMP/bin/sudo" <<'EOF'
#!/usr/bin/env bash
exec "$@"
EOF
cat >"$TMP/bin/systemctl" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$FAKE_SYSTEMCTL_LOG"
EOF
cat >"$TMP/bin/openocd" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
cat >"$TMP/bin/stty" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
cat >"$TMP/bin/timeout" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "${FAKE_UART_RESPONSE:-SILENT}"
EOF
cat >"$TMP/bin/sleep" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "$TMP/bin/"*

export PATH="$TMP/bin:$PATH"
export FAKE_SYSTEMCTL_LOG="$TMP/systemctl.log"
export OPENOCD="$TMP/bin/openocd"
export SWD_CFG="$TMP/swd.cfg"
export SERIAL_DEV="$TMP/serial"

if "$SCRIPT" "$TMP/firmware.elf" >"$TMP/output" 2>&1; then
    echo "expected UART verification failure to exit nonzero" >&2
    exit 1
fi

grep -q '^stop digitsd.service$' "$FAKE_SYSTEMCTL_LOG"
grep -q '^start digitsd.service$' "$FAKE_SYSTEMCTL_LOG"
grep -q 'VERIFY: FAIL' "$TMP/output"

echo "flash-pico fake-hardware failure cleanup: PASS"

: >"$FAKE_SYSTEMCTL_LOG"
export FAKE_UART_RESPONSE=PONG
"$SCRIPT" "$TMP/firmware.elf" >"$TMP/success-output" 2>&1
grep -q '^stop digitsd.service$' "$FAKE_SYSTEMCTL_LOG"
grep -q '^start digitsd.service$' "$FAKE_SYSTEMCTL_LOG"
grep -q 'VERIFY: PASS' "$TMP/success-output"
grep -q '=== Flash complete ===' "$TMP/success-output"

echo "flash-pico fake-hardware success cleanup: PASS"