#!/bin/sh
# boot-check.sh -- initramfs hook: increment boot counter, enter recovery if threshold reached.
#
# Installed to /etc/initramfs-tools/scripts/init-premount/boot-check
# Runs before rootfs is mounted.

PREREQ=""
prereqs() { echo "$PREREQ"; }
case "$1" in prereqs) prereqs; exit 0;; esac

BOOT_DEV="/dev/mmcblk0p1"
BOOT_MNT="/tmp/boot-check"
COUNTER_FILE="boot-counter"
THRESHOLD=3

RECOVERY_DEV="/dev/mmcblk0p3"
RECOVERY_MNT="/tmp/recovery"
RECOVERY_BIN="digits-recovery"

# Mount boot partition
mkdir -p "$BOOT_MNT"
mount -t vfat "$BOOT_DEV" "$BOOT_MNT" 2>/dev/null
if [ $? -ne 0 ]; then
    echo "boot-check: cannot mount boot partition, continuing normal boot"
    exit 0
fi

# Read and increment counter
COUNT=0
if [ -f "$BOOT_MNT/$COUNTER_FILE" ]; then
    COUNT=$(cat "$BOOT_MNT/$COUNTER_FILE" 2>/dev/null)
    # Ensure it's a number
    case "$COUNT" in
        ''|*[!0-9]*) COUNT=0 ;;
    esac
fi

COUNT=$((COUNT + 1))
echo "$COUNT" > "$BOOT_MNT/$COUNTER_FILE"
sync

echo "boot-check: boot attempt $COUNT (threshold=$THRESHOLD)"

if [ "$COUNT" -lt "$THRESHOLD" ]; then
    umount "$BOOT_MNT"
    exit 0
fi

# Threshold reached -- enter recovery mode
echo "boot-check: threshold reached, entering recovery mode"
umount "$BOOT_MNT"

mkdir -p "$RECOVERY_MNT"
mount -t ext4 -o ro "$RECOVERY_DEV" "$RECOVERY_MNT" 2>/dev/null
if [ $? -ne 0 ]; then
    echo "boot-check: cannot mount recovery partition, continuing normal boot"
    exit 0
fi

if [ ! -x "$RECOVERY_MNT/$RECOVERY_BIN" ]; then
    echo "boot-check: recovery binary not found, continuing normal boot"
    umount "$RECOVERY_MNT"
    exit 0
fi

# Set up minimal networking for AP mode
# The recovery binary handles AP setup itself using tools from the recovery partition
export PATH="$RECOVERY_MNT/bin:$PATH"
export BOOT_COUNTER_PATH="$BOOT_MNT/$COUNTER_FILE"
export RECOVERY_DIR="$RECOVERY_MNT"

# Re-mount boot so recovery binary can clear counter
mount -t vfat "$BOOT_DEV" "$BOOT_MNT" 2>/dev/null

# Hand off to recovery binary (does not return)
exec "$RECOVERY_MNT/$RECOVERY_BIN"
