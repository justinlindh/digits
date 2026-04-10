#!/usr/bin/env bash
# partition-setup.sh -- Shrink rootfs (p2) and create recovery (p3) and data (p4) partitions
#
# Usage: sudo ./partition-setup.sh <image.img>
#
# What it does:
#   1. Attaches the .img to a loop device
#   2. Runs e2fsck + resize2fs to shrink p2 to ~3.5GB
#   3. Uses parted to shrink p2 and create p3 (~1.5GB recovery) and p4 (remaining data)
#   4. Formats p3 as ext4, labeled "recovery"
#   5. Formats p4 as journaled ext4, labeled "data"
#
# NOTE: Do NOT use raspi-config overlay -- it has a Bookworm bug that makes
#       all partitions read-only.
#
# Requirements: losetup, parted, e2fsck, resize2fs, mkfs.ext4

set -euo pipefail

# -- helpers ------------------------------------------------------------------

die() { echo "ERROR: $*" >&2; exit 1; }
info() { echo "==> $*"; }

require_cmd() {
    for cmd in "$@"; do
        command -v "$cmd" &>/dev/null || die "Required command not found: $cmd"
    done
}

USING_KPARTX=false

cleanup() {
    local rc=$?
    if [[ -n "${LOOP_DEV:-}" ]]; then
        if $USING_KPARTX; then
            kpartx -d "$LOOP_DEV" 2>/dev/null || true
        fi
        info "Detaching loop device $LOOP_DEV"
        losetup -d "$LOOP_DEV" 2>/dev/null || true
    fi
    exit $rc
}

# Ensure partition device nodes exist for a loop device.
# Uses /dev/loopXpN if available, falls back to kpartx (/dev/mapper/loopXpN).
ensure_partitions() {
    local loop="$1"
    local loop_base
    loop_base=$(basename "$loop")

    # Try native partition nodes first
    partprobe "$loop" 2>/dev/null || true
    sleep 1

    if [[ -b "${loop}p1" ]]; then
        # Native nodes exist
        P_PREFIX="${loop}p"
        return
    fi

    # Fall back to kpartx
    if command -v kpartx &>/dev/null; then
        info "Using kpartx for partition device nodes..."
        kpartx -av "$loop"
        USING_KPARTX=true
        sleep 1
        P_PREFIX="/dev/mapper/${loop_base}p"
        [[ -b "${P_PREFIX}1" ]] || die "kpartx failed to create partition nodes"
    else
        die "Partition nodes not found and kpartx not available. Install kpartx."
    fi
}

# -- sanity checks ------------------------------------------------------------

[[ $EUID -eq 0 ]] || die "Must run as root (sudo $0 $*)"

require_cmd losetup parted e2fsck resize2fs mkfs.ext4

IMG="${1:-}"
[[ -n "$IMG" ]] || die "Usage: $0 <image.img>"
[[ -f "$IMG" ]] || die "Image file not found: $IMG"

# -- attach loop device -------------------------------------------------------

info "Attaching $IMG to loop device..."
LOOP_DEV=$(losetup --find --show --partscan "$IMG")
info "Loop device: $LOOP_DEV"

trap cleanup EXIT

ensure_partitions "$LOOP_DEV"
P2="${P_PREFIX}2"
P3="${P_PREFIX}3"
P4="${P_PREFIX}4"

[[ -b "$P2" ]] || die "Partition p2 not found at $P2"

# -- step 1: check and repair rootfs -----------------------------------------

info "Running e2fsck on $P2 (repair if needed)..."
e2fsck -f -p "$P2" || {
    # e2fsck exits non-zero if it made repairs -- that's fine
    ec=$?
    [[ $ec -le 2 ]] || die "e2fsck failed with exit code $ec"
}

# -- step 2: resize partition p2 to 3.5GB ------------------------------------

info "Reading current partition table..."
P2_START=$(parted -ms "$LOOP_DEV" unit s print | awk -F: '$1=="2"{print $2}' | tr -d 's')
[[ -n "$P2_START" ]] || die "Could not determine p2 start sector"
info "  p2 starts at sector $P2_START"

# Target: 3.5 GiB partition
P2_END_SECTOR=$(( P2_START + 7168000 - 1 ))
P2_END_BYTES=$(( P2_END_SECTOR * 512 ))

info "Resizing p2 to end at sector $P2_END_SECTOR ($(( P2_END_BYTES / 1024 / 1024 )) MiB from disk start)..."
parted -s "$LOOP_DEV" unit s resizepart 2 "${P2_END_SECTOR}s"

# Refresh partition mappings after resize
if $USING_KPARTX; then
    kpartx -u "$LOOP_DEV" 2>/dev/null || true
else
    partprobe "$LOOP_DEV" 2>/dev/null || true
fi
sleep 1

# -- step 3: resize ext4 filesystem to fill partition -------------------------

info "Resizing filesystem on $P2 to fill partition..."
resize2fs "$P2"

# -- step 4: create recovery partition (p3) -----------------------------------

# Start p3 right after p2 (1 sector gap for alignment)
P3_START_SECTOR=$(( P2_END_SECTOR + 1 ))
# 1.5GiB = 3145728 sectors
P3_END_SECTOR=$(( P3_START_SECTOR + 3145728 - 1 ))

info "Creating p3 from sector $P3_START_SECTOR to $P3_END_SECTOR (~1.5GiB recovery)..."
parted -s "$LOOP_DEV" unit s mkpart primary ext4 "${P3_START_SECTOR}s" "${P3_END_SECTOR}s"

# Refresh partition mappings after creating p3
if $USING_KPARTX; then
    kpartx -u "$LOOP_DEV" 2>/dev/null || true
else
    partprobe "$LOOP_DEV" 2>/dev/null || true
fi
sleep 1

[[ -b "$P3" ]] || die "p3 not found at $P3 after creation"

# -- step 5: format p3 as ext4, label "recovery" -----------------------------

info "Formatting $P3 as ext4 (label=recovery)..."
mkfs.ext4 -L recovery -F "$P3"

# -- step 6: create data partition (p4) ---------------------------------------

# Start p4 right after p3 (1 sector gap for alignment)
P4_START_SECTOR=$(( P3_END_SECTOR + 1 ))

info "Creating p4 from sector $P4_START_SECTOR to end of disk (data)..."
parted -s "$LOOP_DEV" unit s mkpart primary ext4 "${P4_START_SECTOR}s" 100%

# Refresh partition mappings after creating p4
if $USING_KPARTX; then
    kpartx -u "$LOOP_DEV" 2>/dev/null || true
else
    partprobe "$LOOP_DEV" 2>/dev/null || true
fi
sleep 1

[[ -b "$P4" ]] || die "p4 not found at $P4 after creation"

# -- step 7: format p4 as journaled ext4, label "data" -----------------------

info "Formatting $P4 as ext4 (journaled, label=data)..."
mkfs.ext4 -L data -J size=16 -F "$P4"

# -- step 8: verify ----------------------------------------------------------

info "Final partition layout:"
parted -s "$LOOP_DEV" unit GiB print

info "e2label check on p3 (recovery):"
e2label "$P3"

info "e2label check on p4 (data):"
e2label "$P4"

info ""
info "Done! Partition layout:"
info "  p1 = /boot/firmware (FAT32, ro)"
info "  p2 = / (ext4, ~3.5GB, will be mounted ro)"
info "  p3 = /recovery (ext4, ~1.5GB)"
info "  p4 = /data (ext4, journaled, rw)"

# trap EXIT will call cleanup and detach loop device
