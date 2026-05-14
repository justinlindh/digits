#!/bin/sh
# recovery-root.sh -- local-bottom initramfs hook: if recovery mode was flagged
# by boot-check, swap the normal rootfs (p2) for the recovery partition (p3)
# before switch_root. This ensures rootfs is never mounted during recovery,
# making it safe for dd to overwrite.
#
# Flow:
#   1. boot-check (init-premount) detects recovery mode, touches /run/digits-recovery-mode
#   2. initramfs mounts normal rootfs (p2) at $rootmnt
#   3. THIS SCRIPT (local-bottom) unmounts p2, mounts p3 at $rootmnt instead
#   4. initramfs does move_virtual_filesystems + switch_root into p3
#   5. digitsd runs as PID 1 from the recovery partition (--mode=recovery)
#
# Installed to /etc/initramfs-tools/scripts/local-bottom/recovery-root

PREREQ=""
prereqs() { echo "$PREREQ"; }
case "$1" in prereqs) prereqs; exit 0;; esac

RECOVERY_FLAG="/run/digits-recovery-mode"

[ -f "$RECOVERY_FLAG" ] || exit 0

echo "recovery-root: recovery mode detected, switching root to recovery partition"

RECOVERY_DEV="/dev/mmcblk0p3"

# Unmount the normal rootfs that was just mounted by mount_root
umount "${rootmnt}" 2>/dev/null || {
    echo "recovery-root: lazy unmount of rootfs"
    umount -l "${rootmnt}" 2>/dev/null || true
}

# Mount recovery partition as the new root (read-only -- the recovery binary
# writes only to tmpfs and the data partition, never to the recovery partition)
mount -t ext4 -o ro "${RECOVERY_DEV}" "${rootmnt}" || {
    echo "recovery-root: FATAL: could not mount recovery partition, falling back to rootfs"
    mount "${ROOT}" "${rootmnt}" 2>/dev/null || true
    exit 0
}

echo "recovery-root: recovery partition mounted at ${rootmnt}, switch_root will boot into it"
