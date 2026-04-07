#!/usr/bin/env bash
# init-data.sh — Create /data directory structure on the data partition
#
# Usage: sudo ./init-data.sh <data-partition-or-mountpoint>
#
# Can be run two ways:
#   1. Pass a block device: sudo ./init-data.sh /dev/sdX3
#      (script mounts it, creates dirs, unmounts)
#   2. Pass an already-mounted directory: sudo ./init-data.sh /mnt/pi-data
#      (script creates dirs in place)
#
# Creates:
#   /data/
#   ├── digits/           # config and tones (binary lives on read-only rootfs)
#   │   ├── config.json   # placeholder — filled by captive portal
#   │   └── tones/        # audio tone files
#   ├── wifi/             # wpa_supplicant.conf (written by setup portal)
#   ├── log/              # persistent logs (bind → /var/log)
#   ├── tmp/              # tmp files (bind → /tmp)
#   └── ssh/              # SSH host keys (bind → /etc/ssh)

set -euo pipefail

die()  { echo "ERROR: $*" >&2; exit 1; }
info() { echo "==> $*"; }

[[ $EUID -eq 0 ]] || die "Must run as root (sudo $0 $*)"

TARGET="${1:-}"
[[ -n "$TARGET" ]] || die "Usage: $0 <block-device|mount-point>"

MOUNTED_BY_US=false
MOUNT_POINT=""

# Determine if target is a block device or existing directory
if [[ -b "$TARGET" ]]; then
    MOUNT_POINT=$(mktemp -d /tmp/pi-data-XXXXXX)
    info "Mounting $TARGET → $MOUNT_POINT"
    mount "$TARGET" "$MOUNT_POINT"
    MOUNTED_BY_US=true
elif [[ -d "$TARGET" ]]; then
    MOUNT_POINT="$TARGET"
    info "Using existing mount point: $MOUNT_POINT"
else
    die "Target is neither a block device nor an existing directory: $TARGET"
fi

cleanup() {
    local rc=$?
    if $MOUNTED_BY_US && [[ -n "$MOUNT_POINT" ]]; then
        info "Unmounting $MOUNT_POINT"
        umount "$MOUNT_POINT" 2>/dev/null || true
        rmdir "$MOUNT_POINT" 2>/dev/null || true
    fi
    exit $rc
}
trap cleanup EXIT

# ── create directory structure ────────────────────────────────────────────────

info "Creating /data directory structure at $MOUNT_POINT..."

# digits/ — digitsd binary, config, tones, updater staging
install -d -m 755 -o 999 -g 992 "${MOUNT_POINT}/digits"
install -d -m 755 -o 999 -g 992 "${MOUNT_POINT}/digits/tones"

# wifi/ — wpa_supplicant.conf written by setup portal
install -d -m 750 -o root -g root "${MOUNT_POINT}/wifi"

# log/ — persistent logs (bind-mounted to /var/log)
install -d -m 755 -o root -g root "${MOUNT_POINT}/log"

# tmp/ — tmp files (bind-mounted to /tmp)
install -d -m 1777 -o root -g root "${MOUNT_POINT}/tmp"

# ssh/ — SSH host keys (bind-mounted to /etc/ssh)
install -d -m 755 -o root -g root "${MOUNT_POINT}/ssh"

# ── create placeholder config.json ───────────────────────────────────────────

CONFIG_JSON="${MOUNT_POINT}/digits/config.json"
if [[ ! -f "$CONFIG_JSON" ]]; then
    info "Creating placeholder config.json..."
    cat > "$CONFIG_JSON" << 'EOF'
{
  "server_url": "wss://app.digits.family/ws",
  "pairing_code": "",
  "wifi_ssid": "",
  "wifi_configured": false
}
EOF
    chmod 644 "$CONFIG_JSON"
fi

# ── summary ───────────────────────────────────────────────────────────────────

info ""
info "Created /data structure:"
find "$MOUNT_POINT" -maxdepth 3 | sed "s|${MOUNT_POINT}|/data|"

info ""
info "Done! Next steps:"
info "  1. Copy SSH host keys to /data/ssh/ (preserves device identity across reflash)"
info "  2. Copy tone files to /data/digits/tones/"
info "  3. The captive portal will write /data/digits/config.json on first setup"
info "  4. Apply rootfs-overlay files and reboot"
