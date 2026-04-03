#!/usr/bin/env bash
# partition-setup.sh — Shrink rootfs (p2) and create /data partition (p3)
#
# Usage: sudo ./partition-setup.sh <image.img>
#
# What it does:
#   1. Attaches the .img to a loop device
#   2. Runs e2fsck + resize2fs to shrink p2 to ~4GB
#   3. Uses parted to shrink p2 and create p3 (~2GB)
#   4. Formats p3 as journaled ext4, labeled "data"
#
# NOTE: Do NOT use raspi-config overlay — it has a Bookworm bug that makes
#       all partitions read-only.
#
# Requirements: losetup, parted, e2fsck, resize2fs, mkfs.ext4

set -euo pipefail

# ── helpers ──────────────────────────────────────────────────────────────────

die() { echo "ERROR: $*" >&2; exit 1; }
info() { echo "==> $*"; }

require_cmd() {
    for cmd in "$@"; do
        command -v "$cmd" &>/dev/null || die "Required command not found: $cmd"
    done
}

cleanup() {
    local rc=$?
    if [[ -n "${LOOP_DEV:-}" ]]; then
        info "Detaching loop device $LOOP_DEV"
        losetup -d "$LOOP_DEV" 2>/dev/null || true
        partprobe "$LOOP_DEV" 2>/dev/null || true
    fi
    exit $rc
}

# ── sanity checks ─────────────────────────────────────────────────────────────

[[ $EUID -eq 0 ]] || die "Must run as root (sudo $0 $*)"

require_cmd losetup parted e2fsck resize2fs mkfs.ext4 partprobe

IMG="${1:-}"
[[ -n "$IMG" ]] || die "Usage: $0 <image.img>"
[[ -f "$IMG" ]] || die "Image file not found: $IMG"

# ── attach loop device ────────────────────────────────────────────────────────

info "Attaching $IMG to loop device..."
LOOP_DEV=$(losetup --find --show --partscan "$IMG")
info "Loop device: $LOOP_DEV"

trap cleanup EXIT

# Give the kernel a moment to settle partition scan
sleep 1
partprobe "$LOOP_DEV" 2>/dev/null || true
sleep 1

P2="${LOOP_DEV}p2"
P3="${LOOP_DEV}p3"

[[ -b "$P2" ]] || die "Partition p2 not found at $P2"

# ── step 1: check and repair rootfs ──────────────────────────────────────────

info "Running e2fsck on $P2 (repair if needed)..."
e2fsck -f -p "$P2" || {
    # e2fsck exits non-zero if it made repairs — that's fine
    local ec=$?
    [[ $ec -le 2 ]] || die "e2fsck failed with exit code $ec"
}

# ── step 2: resize partition p2 to 4.5GB ─────────────────────────────────────

info "Reading current partition table..."
P2_START=$(parted -ms "$LOOP_DEV" unit s print | awk -F: '$1=="2"{print $2}' | tr -d 's')
[[ -n "$P2_START" ]] || die "Could not determine p2 start sector"
info "  p2 starts at sector $P2_START"

# Target: 4.5 GiB partition
P2_END_SECTOR=$(( P2_START + 9216000 - 1 ))
P2_END_BYTES=$(( P2_END_SECTOR * 512 ))

info "Resizing p2 to end at sector $P2_END_SECTOR ($(( P2_END_BYTES / 1024 / 1024 )) MiB from disk start)..."
parted -s "$LOOP_DEV" unit s resizepart 2 "${P2_END_SECTOR}s"

# Re-read partition table before filesystem resize
partprobe "$LOOP_DEV" 2>/dev/null || true
sleep 1

# ── step 3: resize ext4 filesystem to fill partition ─────────────────────────

info "Resizing filesystem on $P2 to fill partition..."
resize2fs "$P2"

# Re-read partition table
partprobe "$LOOP_DEV" 2>/dev/null || true
sleep 1

# ── step 4: create /data partition (p3) ──────────────────────────────────────

# Start p3 right after p2 (1 sector gap for alignment)
P3_START_SECTOR=$(( P2_END_SECTOR + 1 ))
# 2GiB = 4194304 sectors
P3_END_SECTOR=$(( P3_START_SECTOR + 4194304 - 1 ))

info "Creating p3 from sector $P3_START_SECTOR to $P3_END_SECTOR (~2GiB)..."
parted -s "$LOOP_DEV" unit s mkpart primary ext4 "${P3_START_SECTOR}s" "${P3_END_SECTOR}s"

# Re-read partition table
partprobe "$LOOP_DEV" 2>/dev/null || true
sleep 1

[[ -b "$P3" ]] || die "p3 not found at $P3 after creation"

# ── step 5: format p3 as journaled ext4, label "data" ────────────────────────

info "Formatting $P3 as ext4 (journaled, label=data)..."
mkfs.ext4 -L data -J size=16 -F "$P3"

# ── step 6: verify ────────────────────────────────────────────────────────────

info "Final partition layout:"
parted -s "$LOOP_DEV" unit GiB print

info "e2label check on p3:"
e2label "$P3"

info ""
info "Done! Partition layout:"
info "  p1 = /boot/firmware (FAT32, ro)"
info "  p2 = / (ext4, ~4GB, will be mounted ro)"
info "  p3 = /data (ext4, journaled, ~2GB, rw)"
info ""
info "Next: run init-data.sh to create /data directory structure,"
info "then apply rootfs-overlay files to configure fstab and systemd mounts."

# trap EXIT will call cleanup and detach loop device
