#!/bin/sh
# boot-check.sh -- initramfs hook: increment boot counter, enter recovery if threshold reached.
#
# The boot counter lives on the data partition (/data/digits/boot-counter) rather
# than the boot partition. The data partition is journaled ext4 and already writable,
# avoiding risky remounts of the FAT32 boot partition at runtime.
#
# If the data partition cannot be mounted, we enter recovery mode as a safety
# fallback -- a corrupt data partition likely means the device needs a reset.
#
# Installed to /etc/initramfs-tools/scripts/init-premount/boot-check
# Runs before rootfs is mounted.

PREREQ=""
prereqs() { echo "$PREREQ"; }
case "$1" in prereqs) prereqs; exit 0;; esac

DATA_DEV="/dev/mmcblk0p4"
DATA_MNT="/tmp/data-check"
COUNTER_FILE="digits/boot-counter"
THRESHOLD=3

RECOVERY_FLAG="/run/digits-recovery-mode"

# Mount data partition
mkdir -p "$DATA_MNT"
mount -t ext4 "$DATA_DEV" "$DATA_MNT" 2>/dev/null
if [ $? -ne 0 ]; then
    echo "boot-check: cannot mount data partition, flagging for recovery mode"
    touch "$RECOVERY_FLAG"
    exit 0
fi

# Read and increment counter
COUNT=0
if [ -f "$DATA_MNT/$COUNTER_FILE" ]; then
    COUNT=$(cat "$DATA_MNT/$COUNTER_FILE" 2>/dev/null)
    case "$COUNT" in
        ''|*[!0-9]*) COUNT=0 ;;
    esac
fi

COUNT=$((COUNT + 1))
echo "$COUNT" > "$DATA_MNT/$COUNTER_FILE"
sync

echo "boot-check: boot attempt $COUNT (threshold=$THRESHOLD)"

if [ "$COUNT" -lt "$THRESHOLD" ]; then
    umount "$DATA_MNT"
    exit 0
fi

# Threshold reached -- flag for recovery mode
echo "boot-check: threshold reached, flagging for recovery mode"
touch "$RECOVERY_FLAG"
umount "$DATA_MNT"
exit 0
