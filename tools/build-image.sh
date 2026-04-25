#!/usr/bin/env bash
# build-image.sh — Build a flashable SD card image for Digits Pi phones
#
# Takes an official Raspberry Pi OS Lite (Bookworm, 64-bit) image and
# customizes it with Digits software, services, and configuration.
#
# Usage: sudo ./tools/build-image.sh [--dev] [--pcb] <raspios-lite.img|.img.xz>
#
# Prerequisites:
#   - x86_64 Linux host
#   - qemu-user-static (for arm64 chroot — apt-get ONLY)
#   - losetup, parted, e2fsck, resize2fs, mkfs.ext4
#   - Cross-compiled binaries in tools/build/ (digitsd, digits-setup)
#
# Design principle:
#   qemu-user chroot is ONLY used for apt-get operations. All other
#   system administration (user creation, service enabling, file ownership)
#   is done host-side to avoid qemu-aarch64 silently corrupting files
#   like /etc/shadow and /etc/group.
#
# Output: digits-pi-v{1,2}-YYYYMMDD.img.gz in the current directory
#         (v1 for Codec Zero HAT / prototype, v2 when --pcb is passed)
#
# See tools/README-image-builder.md for full documentation.

set -euo pipefail

# ── configuration ────────────────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
OVERLAY_DIR="${REPO_DIR}/pi/image/rootfs-overlay"
PARTITION_SETUP="${REPO_DIR}/pi/image/partition-setup.sh"
INIT_DATA="${REPO_DIR}/pi/image/init-data.sh"
BUILD_DIR="${SCRIPT_DIR}/build"
TONES_DIR="${REPO_DIR}/pi/tones"
DATE_STAMP=$(date +%Y%m%d)
# OUTPUT_NAME is finalized after --pcb is parsed so V1 and V2 builds land in
# distinct files (digits-pi-v1-DATE.img vs digits-pi-v2-DATE.img).
OUTPUT_NAME=""

# Packages to install via chroot (the ONLY thing qemu chroot is used for)
CHROOT_PACKAGES=(
    hostapd
    dnsmasq-base
    alsa-utils
    libopus0
    libopusfile0
    libsndfile1
    openocd
    i2c-tools
    minicom
)

# Packages to purge from the base Pi OS image (from pi-os-audit.md)
# These are removed inside chroot via apt-get purge
PURGE_PACKAGES=(
    # Development tools (~420 MB)
    gcc-12 g++-12 cpp-12 gcc cpp g++ build-essential
    binutils binutils-aarch64-linux-gnu binutils-common
    libgcc-12-dev libstdc++-12-dev libc6-dev libc6-dbg linux-libc-dev
    autoconf automake autotools-dev m4 make
    git git-man gdb strace
    man-db manpages manpages-dev groff-base
    libasan8 libtsan2 libubsan1 liblsan0 libitm1 libhwasan0 libgprofng0

    # Pi 5 kernel + all kernel headers (~153 MB)
    linux-image-6.12.47+rpt-rpi-2712 linux-image-rpi-2712
    linux-headers-6.12.47+rpt-common-rpi
    linux-headers-6.12.47+rpt-rpi-2712 linux-headers-rpi-2712
    linux-headers-6.12.47+rpt-rpi-v8 linux-headers-rpi-v8
    linux-kbuild-6.12.47+rpt raspberrypi-kernel-headers

    # Non-brcm wireless firmware (~127 MB)
    firmware-atheros firmware-mediatek firmware-libertas firmware-realtek

    # Bluetooth stack (~6 MB) — disabled via dtoverlay=disable-bt
    bluez bluez-firmware

    # Cellular modem stack (~12 MB) — no modem hardware
    modemmanager libmbim-glib4 libmbim-proxy libmbim-utils
    libqmi-glib5 libqmi-proxy libqmi-utils libqrtr-glib0

    # Mesa/GPU/LLVM/X11 (~220 MB) — headless device
    libllvm15 mesa-vulkan-drivers mesa-libgallium mesa-va-drivers mesa-vdpau-drivers
    libgl1-mesa-dri libglapi-mesa libglx-mesa0 libvulkan1 libglvnd0
    libdrm-amdgpu1 libdrm-radeon1 libva2 libva-drm2 libva-x11-2
    libvdpau1 libvdpau-va-gl1
    libz3-4 libboost-filesystem1.74.0 libboost-log1.74.0
    libboost-program-options1.74.0 libboost-regex1.74.0 libboost-thread1.74.0
    libx11-6 libx11-data xkb-data shared-mime-info
    libcairo2 libcairo-gobject2 libpango-1.0-0 libpangocairo-1.0-0 libpangoft2-1.0-0
    libharfbuzz0b libfreetype6 libfontconfig1 libfribidi0 fonts-dejavu-core
    librsvg2-2 librsvg2-common
    libgdk-pixbuf-2.0-0 libgdk-pixbuf2.0-bin libgdk-pixbuf2.0-common

    # Camera/V4L2 stack (~13 MB) — no camera
    libcamera0.5 libcamera-ipa libpisp1 libpisp-common
    rpicam-apps-core rpicam-apps-lite librpicam-app1
    v4l-utils libv4l-0 libv4lconvert0 libv4l2rds0

    # Video codecs/multimedia (~58 MB) — no media playback
    mkvtoolnix libmatroska7 libebml5
    libavcodec59 libavutil57 libswresample4
    libvpx7 libaom3 libdav1d6 librav1e0 libsvtav1enc1
    libx264-164 libx265-199 libheif1 libjxl0.7 libopenjp2-7
    libzvbi0 libzvbi-common

    # NFS/RPC (~3.4 MB)
    nfs-common rpcbind rpcsvc-proto libnfsidmap1 libtirpc-dev libtalloc2

    # Storage management (~9 MB) — no removable drives
    udisks2 libudisks2-0 ntfs-3g libntfs-3g89
    libmtp9 libmtp-common libmtp-runtime
    usb-modeswitch usb-modeswitch-data
    libparted2 libparted-fs-resize0 parted
    dosfstools exfatprogs fuse3 libfuse3-3

    # Locale/i18n bloat (~53 MB)
    iso-codes locales gnupg-l10n libglib2.0-data

    # Polkit/AppArmor/DKMS (~4 MB)
    polkitd polkitd-pkla policykit-1 pkexec
    libpolkit-agent-1-0 libpolkit-gobject-1-0
    apparmor libapparmor1 dkms

    # Pi-specific tools not needed on production device (~67 MB)
    rpi-eeprom raspi-firmware rpi-update
    rpi-keyboard-config rpi-keyboard-fw-update
    raspi-config raspi-gpio raspinfo userconf-pi read-edid flashrom

    # GPIO library (deprecated, not used by digitsd)
    pigpio pigpiod libpigpio1 libpigpio-dev libpigpiod-if1 libpigpiod-if2-1 libpigpiod-if-dev

    # Misc dev/debug tools
    triggerhappy htop ncdu iperf3 libiperf0 net-tools wget pastebinit
    cron cron-daemon-common ppp
)

# ── helpers ──────────────────────────────────────────────────────────────────

die()  { echo "ERROR: $*" >&2; exit 1; }
info() { echo "==> $*"; }
warn() { echo "WARNING: $*" >&2; }

# Dev mode: pass --dev to enable SSH + default user for debugging
# PCB mode: pass --pcb to target the V2 carrier board. Enables the onboard
# TLV320AIC3104 codec overlay and sets hook_inverted. Without --pcb, the image
# is built for V1/prototype hardware (Codec Zero HAT, non-inverted hook).
DEV_MODE=false
PCB_MODE=false
while [[ "${1:-}" == --* ]]; do
    case "$1" in
        --dev)
            DEV_MODE=true
            info "DEV MODE: SSH will be enabled with user 'dev' / password 'digits'"
            ;;
        --pcb)
            PCB_MODE=true
            info "PCB MODE: hook_inverted will be set in config.json"
            ;;
        *)
            die "Unknown flag: $1"
            ;;
    esac
    shift
done

if [[ "$PCB_MODE" == true ]]; then
    OUTPUT_NAME="digits-pi-v2-${DATE_STAMP}.img"
else
    OUTPUT_NAME="digits-pi-v1-${DATE_STAMP}.img"
fi

require_cmd() {
    for cmd in "$@"; do
        command -v "$cmd" &>/dev/null || die "Required command not found: $cmd"
    done
}

# ── cleanup ──────────────────────────────────────────────────────────────────

LOOP_DEV=""
ROOTFS_MNT=""
BOOT_MNT=""
RECOVERY_MNT=""
DATA_MNT=""
USING_KPARTX=false

cleanup() {
    local rc=$?
    info "Cleaning up..."

    # Kill any remaining chroot processes
    if [[ -n "${ROOTFS_MNT:-}" && -d "$ROOTFS_MNT" ]]; then
        for mp in dev/pts dev/shm dev proc sys; do
            umount "${ROOTFS_MNT}/${mp}" 2>/dev/null || true
        done
    fi

    # Unmount partitions
    [[ -n "${DATA_MNT:-}" ]]     && { umount "$DATA_MNT" 2>/dev/null || true; }
    [[ -n "${RECOVERY_MNT:-}" ]] && { umount "$RECOVERY_MNT" 2>/dev/null || true; }
    [[ -n "${BOOT_MNT:-}" ]]     && { umount "$BOOT_MNT" 2>/dev/null || true; }
    [[ -n "${ROOTFS_MNT:-}" ]]   && { umount "$ROOTFS_MNT" 2>/dev/null || true; }

    # Detach loop device
    if [[ -n "${LOOP_DEV:-}" ]]; then
        $USING_KPARTX && { kpartx -d "$LOOP_DEV" 2>/dev/null || true; }
        info "Detaching loop device $LOOP_DEV"
        losetup -d "$LOOP_DEV" 2>/dev/null || true
    fi

    # Remove temp mount points
    [[ -n "${ROOTFS_MNT:-}" ]]   && { rmdir "$ROOTFS_MNT" 2>/dev/null || true; }
    [[ -n "${BOOT_MNT:-}" ]]     && { rmdir "$BOOT_MNT" 2>/dev/null || true; }
    [[ -n "${RECOVERY_MNT:-}" ]] && { rmdir "$RECOVERY_MNT" 2>/dev/null || true; }
    [[ -n "${DATA_MNT:-}" ]]     && { rmdir "$DATA_MNT" 2>/dev/null || true; }

    if [[ $rc -ne 0 ]]; then
        warn "Build failed! Partial image may remain: ${OUTPUT_NAME}"
    fi
    exit $rc
}

# Ensure partition device nodes exist for a loop device.
# Sets P_PREFIX so callers use ${P_PREFIX}1, ${P_PREFIX}2, etc.
ensure_partitions() {
    local loop="$1"
    local loop_base
    loop_base=$(basename "$loop")

    partprobe "$loop" 2>/dev/null || true
    sleep 1

    if [[ -b "${loop}p1" ]]; then
        P_PREFIX="${loop}p"
        return
    fi

    if command -v kpartx &>/dev/null; then
        info "Using kpartx for partition device nodes..."
        kpartx -av "$loop"
        USING_KPARTX=true
        sleep 1
        P_PREFIX="/dev/mapper/${loop_base}p"
        [[ -b "${P_PREFIX}1" ]] || die "kpartx failed to create partition nodes"
    else
        die "Partition nodes not found and kpartx not available."
    fi
}

# Detach loop device and clean up kpartx mappings
detach_loop() {
    if [[ -n "${LOOP_DEV:-}" ]]; then
        $USING_KPARTX && { kpartx -d "$LOOP_DEV" 2>/dev/null || true; }
        losetup -d "$LOOP_DEV" 2>/dev/null || true
        LOOP_DEV=""
        USING_KPARTX=false
    fi
}

trap cleanup EXIT

# ── host-side helper: add user to passwd/shadow/group ────────────────────────

# Add a user directly to rootfs files — no chroot, no qemu.
# Usage: hostside_adduser <rootfs> <username> <uid> <gid> <home> <shell> <password_hash> <gecos>
hostside_adduser() {
    local rootfs="$1" user="$2" uid="$3" gid="$4" home="$5" shell="$6" hash="$7" gecos="${8:-}"

    # passwd
    if ! grep -q "^${user}:" "${rootfs}/etc/passwd"; then
        echo "${user}:x:${uid}:${gid}:${gecos}:${home}:${shell}" >> "${rootfs}/etc/passwd"
    fi

    # shadow
    if grep -q "^${user}:" "${rootfs}/etc/shadow"; then
        sed -i "s|^${user}:[^:]*:|${user}:${hash}:|" "${rootfs}/etc/shadow"
    else
        echo "${user}:${hash}:20000:0:99999:7:::" >> "${rootfs}/etc/shadow"
    fi

    # group (primary group)
    if ! grep -q "^${user}:" "${rootfs}/etc/group"; then
        echo "${user}:x:${gid}:" >> "${rootfs}/etc/group"
    fi

    # gshadow
    if [[ -f "${rootfs}/etc/gshadow" ]] && ! grep -q "^${user}:" "${rootfs}/etc/gshadow"; then
        echo "${user}:!::" >> "${rootfs}/etc/gshadow"
    fi

    # home directory
    mkdir -p "${rootfs}${home}"
    chown "${uid}:${gid}" "${rootfs}${home}"
}

# Add a user to supplementary groups (host-side file manipulation)
# Usage: hostside_add_to_groups <rootfs> <username> <group1> [group2] ...
hostside_add_to_groups() {
    local rootfs="$1" user="$2"
    shift 2

    for grp in "$@"; do
        if grep -q "^${grp}:" "${rootfs}/etc/group"; then
            # Check if user is already in the group
            if ! grep -q "^${grp}:.*\b${user}\b" "${rootfs}/etc/group"; then
                # Append user to group members list
                local current_members
                current_members=$(grep "^${grp}:" "${rootfs}/etc/group" | cut -d: -f4)
                if [[ -n "$current_members" ]]; then
                    sed -i "s|^${grp}:\(.*\)|${grp}:\1,${user}|" "${rootfs}/etc/group"
                else
                    sed -i "s|^${grp}:\([^:]*:[^:]*:\)$|${grp}:\1${user}|" "${rootfs}/etc/group"
                fi
            fi
        fi
    done
}

# ── host-side helper: enable/disable/mask systemd services ──────────────────

# Enable a systemd service by creating the symlink directly — no chroot.
# Usage: hostside_enable_service <rootfs> <unit-name> [<target>]
hostside_enable_service() {
    local rootfs="$1" unit="$2" target="${3:-multi-user.target}"
    local unit_file

    # Find the unit file
    if [[ -f "${rootfs}/etc/systemd/system/${unit}" ]]; then
        unit_file="/etc/systemd/system/${unit}"
    elif [[ -f "${rootfs}/lib/systemd/system/${unit}" ]]; then
        unit_file="/lib/systemd/system/${unit}"
    else
        warn "Unit file not found for ${unit} — skipping enable"
        return 1
    fi

    # Parse WantedBy from the unit file if target not specified explicitly
    if [[ "$target" == "multi-user.target" ]]; then
        local wanted_by
        wanted_by=$(grep -oP '(?<=WantedBy=)\S+' "${rootfs}${unit_file}" 2>/dev/null | head -1)
        if [[ -n "$wanted_by" ]]; then
            target="$wanted_by"
        fi
    fi

    local wants_dir="${rootfs}/etc/systemd/system/${target}.wants"
    mkdir -p "$wants_dir"
    ln -sf "$unit_file" "${wants_dir}/${unit}"
    info "  Enabled ${unit} → ${target}"
}

# Disable a systemd service by removing its symlink — no chroot.
# Usage: hostside_disable_service <rootfs> <unit-name>
hostside_disable_service() {
    local rootfs="$1" unit="$2"
    find "${rootfs}/etc/systemd/system" -name "$unit" -type l -delete 2>/dev/null || true
    info "  Disabled ${unit}"
}

# Mask a systemd service/socket by symlinking to /dev/null — no chroot.
# Usage: hostside_mask_service <rootfs> <unit-name>
hostside_mask_service() {
    local rootfs="$1" unit="$2"
    # Remove any existing symlinks first
    find "${rootfs}/etc/systemd/system" -name "$unit" -type l -delete 2>/dev/null || true
    ln -sf /dev/null "${rootfs}/etc/systemd/system/${unit}"
    info "  Masked ${unit}"
}

# ── sanity checks ────────────────────────────────────────────────────────────

[[ $EUID -eq 0 ]] || die "Must run as root (sudo $0 $*)"

require_cmd losetup parted e2fsck resize2fs mkfs.ext4 \
            qemu-aarch64-static gzip blkid rsync openssl zstd dtc

# Verify we're on x86_64
[[ "$(uname -m)" == "x86_64" ]] || die "This script must run on x86_64 Linux"

INPUT_IMG="${1:-}"
[[ -n "$INPUT_IMG" ]] || die "Usage: $0 [--dev] [--pcb] <raspios-lite.img|.img.xz>"
[[ -f "$INPUT_IMG" ]] || die "Input image not found: $INPUT_IMG"

# Verify pre-built binaries exist
[[ -f "${BUILD_DIR}/digitsd" ]]      || die "Missing ${BUILD_DIR}/digitsd -- run cross-compilation first (see README)"
[[ -f "${BUILD_DIR}/digits-setup" ]] || die "Missing ${BUILD_DIR}/digits-setup -- run cross-compilation first (see README)"
[[ -f "${REPO_DIR}/pi/digits-recovery/bin/digits-recovery" ]] || die "Missing pi/digits-recovery/bin/digits-recovery -- run pi/digits-recovery build first"

# Verify overlay directory exists
[[ -d "$OVERLAY_DIR" ]] || die "Overlay directory not found: $OVERLAY_DIR"

# Verify partition-setup.sh exists
[[ -f "$PARTITION_SETUP" ]] || die "partition-setup.sh not found: $PARTITION_SETUP"

# Verify init-data.sh exists
[[ -f "$INIT_DATA" ]] || die "init-data.sh not found: $INIT_DATA"

# Check qemu-user-static binfmt registration
if [[ ! -f /proc/sys/fs/binfmt_misc/qemu-aarch64 ]]; then
    warn "qemu-aarch64 binfmt not registered. Attempting registration..."
    if [[ -x /usr/sbin/update-binfmts ]]; then
        update-binfmts --enable qemu-aarch64 || die "Failed to enable qemu-aarch64 binfmt"
    else
        die "qemu-aarch64 binfmt not registered and update-binfmts not available. Install qemu-user-static."
    fi
fi

# ── step 1: prepare working copy ────────────────────────────────────────────

info "Preparing working image..."

if [[ "$INPUT_IMG" == *.xz ]]; then
    info "Decompressing .xz image..."
    xz -dk "$INPUT_IMG"
    WORK_IMG="${INPUT_IMG%.xz}"
elif [[ "$INPUT_IMG" == *.img ]]; then
    info "Copying image to ${OUTPUT_NAME}..."
    cp "$INPUT_IMG" "$OUTPUT_NAME"
    WORK_IMG="$OUTPUT_NAME"
else
    die "Unsupported image format. Expected .img or .img.xz"
fi

# If we decompressed, rename to output name
if [[ "$WORK_IMG" != "$OUTPUT_NAME" ]]; then
    mv "$WORK_IMG" "$OUTPUT_NAME"
    WORK_IMG="$OUTPUT_NAME"
fi

# Expand image to have room for recovery and data partitions
# partition-setup.sh shrinks rootfs to ~3.5GB, adds ~1.5GB for recovery, and ~2GB for data
# Pi OS Lite is ~2.7GB; we need ~8GB total
CURRENT_SIZE=$(stat -c %s "$WORK_IMG")
TARGET_SIZE=$((8 * 1024 * 1024 * 1024))  # 8 GiB
if (( CURRENT_SIZE < TARGET_SIZE )); then
    info "Expanding image to 8GiB to accommodate recovery and data partitions..."
    truncate -s "${TARGET_SIZE}" "$WORK_IMG"
fi

# ── step 2: create recovery and data partitions ──────────────────────────────

info "Running partition-setup.sh to create recovery and data partitions..."
bash "$PARTITION_SETUP" "$WORK_IMG"

# ── step 3: mount partitions ────────────────────────────────────────────────

info "Attaching image to loop device..."
LOOP_DEV=$(losetup --find --show --partscan "$WORK_IMG")
info "Loop device: $LOOP_DEV"

ensure_partitions "$LOOP_DEV"
P1="${P_PREFIX}1"
P2="${P_PREFIX}2"
P3="${P_PREFIX}3"
P4="${P_PREFIX}4"

[[ -b "$P1" ]] || die "Boot partition not found at $P1"
[[ -b "$P2" ]] || die "Root partition not found at $P2"
[[ -b "$P3" ]] || die "Recovery partition not found at $P3"
[[ -b "$P4" ]] || die "Data partition not found at $P4"

# Create mount points
ROOTFS_MNT=$(mktemp -d /tmp/digits-rootfs-XXXXXX)
BOOT_MNT="${ROOTFS_MNT}/boot/firmware"
RECOVERY_MNT=$(mktemp -d /tmp/digits-recovery-XXXXXX)
DATA_MNT="${ROOTFS_MNT}/data"

info "Mounting rootfs ($P2) → $ROOTFS_MNT"
mount "$P2" "$ROOTFS_MNT"

mkdir -p "$BOOT_MNT" "$DATA_MNT"

info "Mounting boot ($P1) → $BOOT_MNT"
mount "$P1" "$BOOT_MNT"

info "Mounting recovery ($P3) → $RECOVERY_MNT"
mount "$P3" "$RECOVERY_MNT"

info "Mounting data ($P4) → $DATA_MNT"
mount "$P4" "$DATA_MNT"

# ── step 4: prepare chroot (apt-get ONLY) ───────────────────────────────────

info "Preparing chroot environment (for apt-get operations only)..."

# Copy qemu-user-static into the chroot
cp "$(which qemu-aarch64-static)" "${ROOTFS_MNT}/usr/bin/"

# Mount necessary filesystems for chroot
mount -t proc proc "${ROOTFS_MNT}/proc"
mount -t sysfs sys "${ROOTFS_MNT}/sys"
mount --bind /dev "${ROOTFS_MNT}/dev"
mount --bind /dev/pts "${ROOTFS_MNT}/dev/pts"
mount --bind /dev/shm "${ROOTFS_MNT}/dev/shm" 2>/dev/null || true

# Prevent services from starting during chroot operations
cat > "${ROOTFS_MNT}/usr/sbin/policy-rc.d" << 'POLICY'
#!/bin/sh
exit 101
POLICY
chmod +x "${ROOTFS_MNT}/usr/sbin/policy-rc.d"

# Use host resolv.conf for DNS in chroot
cp "${ROOTFS_MNT}/etc/resolv.conf" "${ROOTFS_MNT}/etc/resolv.conf.bak" 2>/dev/null || true
cp /etc/resolv.conf "${ROOTFS_MNT}/etc/resolv.conf"

# ── step 5: purge unnecessary packages (chroot — apt-get only) ──────────────

info "Purging unnecessary packages from base image..."
# Join array into space-separated string for apt-get
PURGE_LIST="${PURGE_PACKAGES[*]}"
chroot "$ROOTFS_MNT" /bin/bash -c "
    DEBIAN_FRONTEND=noninteractive apt-get purge -y --auto-remove ${PURGE_LIST} 2>/dev/null || true
    DEBIAN_FRONTEND=noninteractive apt-get autoremove -y 2>/dev/null || true
    apt-get clean
    rm -f /etc/ssh/sshd_config.d/rename_user.conf
"

# ── step 6: install required packages (chroot — apt-get only) ───────────────

info "Installing packages in chroot: ${CHROOT_PACKAGES[*]}..."
INSTALL_LIST="${CHROOT_PACKAGES[*]}"
chroot "$ROOTFS_MNT" /bin/bash -c "
    apt-get update -qq
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ${INSTALL_LIST}
    apt-get clean
    rm -rf /var/lib/apt/lists/*
"

# ── step 7: tear down chroot ────────────────────────────────────────────────
# All remaining work is host-side. Tear down chroot immediately to avoid
# accidentally running anything else under qemu.

info "Tearing down chroot (all remaining work is host-side)..."

for mp in dev/pts dev/shm dev proc sys; do
    umount "${ROOTFS_MNT}/${mp}" 2>/dev/null || true
done

rm -f "${ROOTFS_MNT}/usr/sbin/policy-rc.d"
rm -f "${ROOTFS_MNT}/usr/bin/qemu-aarch64-static"

# Restore resolv.conf (will be replaced with NM symlink later)
if [[ -f "${ROOTFS_MNT}/etc/resolv.conf.bak" ]]; then
    mv "${ROOTFS_MNT}/etc/resolv.conf.bak" "${ROOTFS_MNT}/etc/resolv.conf"
fi

# ── step 8: strip non-brcm firmware blobs (host-side) ───────────────────────

info "Stripping unused firmware blobs (keeping brcm for Zero 2 W)..."

if [[ -d "${ROOTFS_MNT}/lib/firmware" ]]; then
    find "${ROOTFS_MNT}/lib/firmware/" -mindepth 1 -maxdepth 1 \
        ! -name 'brcm' \
        ! -name 'raspberrypi' \
        ! -name 'regulatory.db' \
        ! -name 'regulatory.db.p7s' \
        -exec rm -rf {} +

    # Strip BT firmware from brcm (BT is disabled via dtoverlay=disable-bt)
    find "${ROOTFS_MNT}/lib/firmware/brcm" -name "BCM*.hcd" -delete 2>/dev/null || true
fi

# ── step 9: locale purge — keep only en_US (host-side) ──────────────────────

info "Purging non-English locale data..."

# Strip locale directories (keep en, en_US, C only)
if [[ -d "${ROOTFS_MNT}/usr/share/locale" ]]; then
    find "${ROOTFS_MNT}/usr/share/locale" -mindepth 1 -maxdepth 1 -type d \
        ! -name 'en' ! -name 'en_US' ! -name 'C' \
        -exec rm -rf {} +
fi

# Strip compiled locales
if [[ -d "${ROOTFS_MNT}/usr/lib/locale" ]]; then
    find "${ROOTFS_MNT}/usr/lib/locale" -mindepth 1 -maxdepth 1 \
        ! -name 'C.utf8' ! -name 'en_US.UTF-8' ! -name 'C' \
        -exec rm -rf {} + 2>/dev/null || true
fi

# ── step 10: remove docs, man pages, headers (host-side) ────────────────────

info "Removing documentation, man pages, and include files..."

rm -rf "${ROOTFS_MNT}/usr/share/doc/"
rm -rf "${ROOTFS_MNT}/usr/share/man/"
rm -rf "${ROOTFS_MNT}/usr/share/info/"
rm -rf "${ROOTFS_MNT}/usr/share/groff/"
rm -rf "${ROOTFS_MNT}/usr/include/"

# Remove old/deprecated kernel source and headers
rm -rf "${ROOTFS_MNT}/usr/src/linux-headers-"*
rm -rf "${ROOTFS_MNT}/usr/src/linux-kbuild-"*
rm -rf "${ROOTFS_MNT}/usr/src/wm8960-soundcard-"*

# ── step 11: create digits system user (host-side) ──────────────────────────

info "Creating digits system user (host-side, no qemu)..."

hostside_adduser "$ROOTFS_MNT" "digits" 999 992 "/home/digits" "/usr/sbin/nologin" "*" ""

# Add digits to audio and i2c groups
hostside_add_to_groups "$ROOTFS_MNT" "digits" audio i2c

# ── step 12: copy binaries (host-side) ──────────────────────────────────────

info "Copying Digits binaries..."

install -m 755 "${BUILD_DIR}/digitsd" "${ROOTFS_MNT}/usr/local/bin/digitsd"
install -m 755 "${BUILD_DIR}/digits-setup" "${ROOTFS_MNT}/usr/local/bin/digits-setup"


# ── step 13: copy rootfs overlay (host-side) ────────────────────────────────

info "Copying rootfs overlay..."
rsync -a --no-owner --no-group "$OVERLAY_DIR/" "$ROOTFS_MNT/"

# Make scripts executable
chmod +x "${ROOTFS_MNT}/usr/local/bin/"* 2>/dev/null || true

# ── step 13a: compile device-tree overlays (host-side) ──────────────────────
# The FAT boot firmware loads compiled .dtbo binaries only; .dts sources in
# /boot/firmware/overlays/ are ignored by the loader.

info "Compiling device-tree overlays..."
for dts in "${BOOT_MNT}/overlays/"digits-*.dts; do
    [[ -f "$dts" ]] || continue
    dtbo="${dts%.dts}.dtbo"
    info "  $(basename "$dts") -> $(basename "$dtbo")"
    dtc -@ -q -I dts -O dtb -o "$dtbo" "$dts"
    rm -f "$dts"
done

# ── step 14: copy tone files (host-side) ────────────────────────────────────

if [[ -d "$TONES_DIR" ]] && compgen -G "$TONES_DIR/*.wav" > /dev/null 2>&1; then
    info "Copying tone WAV files..."
    mkdir -p "${DATA_MNT}/digits/tones"
    rsync -a --include="*.wav" --include="*/" --exclude="*" "$TONES_DIR/" "${DATA_MNT}/digits/tones/"
else
    warn "No tone WAV files found in $TONES_DIR — skipping"
fi

# ── step 14a: copy Pico firmware to /data (host-side) ────────────────────────

FW_ELF="${BUILD_DIR}/firmware.elf"
if [[ -f "$FW_ELF" ]]; then
    info "Copying Pico firmware to /data/digits/firmware.elf..."
    cp "$FW_ELF" "${DATA_MNT}/digits/firmware.elf"
    chown 999:992 "${DATA_MNT}/digits/firmware.elf"
    chmod 644 "${DATA_MNT}/digits/firmware.elf"
else
    warn "No firmware.elf found in tools/build/ -- Pico will need OTA flash after first boot"
fi

# ── step 14b: copy mixer state to /data (host-side) ─────────────────────────

MIXER_STATE="${REPO_DIR}/pi/digits_mixer.state"
if [[ -f "$MIXER_STATE" ]]; then
    info "Copying mixer state to /data..."
    cp "$MIXER_STATE" "${DATA_MNT}/digits_mixer.state"
else
    warn "No mixer state file found at $MIXER_STATE — audio may not work on first boot"
fi

# ── step 15: initialize /data partition (host-side) ─────────────────────────

info "Initializing /data directory structure..."
if [[ "$PCB_MODE" == true ]]; then
    bash "$INIT_DATA" --pcb "$DATA_MNT"
else
    bash "$INIT_DATA" "$DATA_MNT"
fi

# ── step 15b: populate recovery partition (host-side) ───────────────────────

info "Populating recovery partition..."

# NOTE: rootfs snapshot is deferred until after all rootfs modifications
# are complete (services enabled, config applied, etc.). It is taken in
# the final steps before unmount, with the recovery partition temporarily
# re-mounted. See "step 23b: create rootfs snapshot" below.

# Clean /data before creating skeleton (first-boot must run fresh after reset)
# Keep config.json (has server_url), remove device-specific state
rm -f "${DATA_MNT}/.initialized"
rm -f "${DATA_MNT}/log/digits-first-boot.log"
rm -f "${DATA_MNT}/digits/device-id"
rm -f "${DATA_MNT}/digits/config.json.bak"
rm -f "${DATA_MNT}/digits/recovery-mode"
rm -f "${DATA_MNT}/wifi-configured"
rm -rf "${DATA_MNT}/wifi/"*
rm -rf "${DATA_MNT}/log/journal/"*
rm -rf "${DATA_MNT}/ssh/"*

# Create compressed data skeleton archive
info "  Creating data skeleton archive..."
tar cf - -C "$DATA_MNT" . | zstd -T0 -o "${RECOVERY_MNT}/data-skeleton.tar.zst"

# Copy recovery binary
info "  Copying recovery binary..."
RECOVERY_BIN="${REPO_DIR}/pi/digits-recovery/bin/digits-recovery"
[[ -f "$RECOVERY_BIN" ]] || die "Recovery binary not found: $RECOVERY_BIN (run pi/digits-recovery build first)"
install -m 755 "$RECOVERY_BIN" "${RECOVERY_MNT}/digits-recovery"

# Create mini-rootfs directory structure on recovery partition.
# After switch_root, this partition IS the root filesystem, so it needs
# mount points for virtual filesystems and a valid /sbin/init.
info "  Creating recovery partition rootfs structure..."
mkdir -p "${RECOVERY_MNT}"/{dev,proc,sys,tmp,run,data,sbin,lib,bin}

# Create /sbin/init symlink -- this is what switch_root execs as PID 1
ln -sf /digits-recovery "${RECOVERY_MNT}/sbin/init"

# Install initramfs hooks
info "  Installing initramfs hooks..."
BOOT_CHECK_SRC="${REPO_DIR}/pi/image/initramfs/boot-check.sh"
RECOVERY_ROOT_SRC="${REPO_DIR}/pi/image/initramfs/recovery-root.sh"
[[ -f "$BOOT_CHECK_SRC" ]]   || die "boot-check.sh not found: $BOOT_CHECK_SRC"
[[ -f "$RECOVERY_ROOT_SRC" ]] || die "recovery-root.sh not found: $RECOVERY_ROOT_SRC"
mkdir -p "${ROOTFS_MNT}/etc/initramfs-tools/scripts/init-premount"
mkdir -p "${ROOTFS_MNT}/etc/initramfs-tools/scripts/local-bottom"
install -m 755 "$BOOT_CHECK_SRC"   "${ROOTFS_MNT}/etc/initramfs-tools/scripts/init-premount/boot-check"
install -m 755 "$RECOVERY_ROOT_SRC" "${ROOTFS_MNT}/etc/initramfs-tools/scripts/local-bottom/recovery-root"

# Set up chroot for tool path resolution, library copying, and initramfs rebuild
cp "$(which qemu-aarch64-static)" "${ROOTFS_MNT}/usr/bin/"
mount -t proc proc "${ROOTFS_MNT}/proc"
mount -t sysfs sys "${ROOTFS_MNT}/sys"
mount --bind /dev "${ROOTFS_MNT}/dev"
mount --bind /dev/pts "${ROOTFS_MNT}/dev/pts"
mount --bind /dev/shm "${ROOTFS_MNT}/dev/shm" 2>/dev/null || true

# Copy required tools from rootfs into recovery partition bin/
info "  Copying required tools to recovery/bin/..."
for tool in hostapd ip dnsmasq zstd dd mkfs.ext4 mount umount tar; do
    # Use readlink -f inside chroot to resolve symlinks to the real binary
    TOOL_PATH=$(chroot "$ROOTFS_MNT" readlink -f "$(chroot "$ROOTFS_MNT" which "$tool" 2>/dev/null)" 2>/dev/null || true)
    if [[ -z "$TOOL_PATH" ]]; then
        warn "  Tool not found in rootfs: $tool -- skipping"
        continue
    fi
    if [[ -f "${ROOTFS_MNT}${TOOL_PATH}" ]]; then
        install -m 755 "${ROOTFS_MNT}${TOOL_PATH}" "${RECOVERY_MNT}/bin/${tool}"
        info "  Copied $tool (${TOOL_PATH})"
    else
        warn "  Tool path ${TOOL_PATH} not found on rootfs for: $tool -- skipping"
    fi
done

# Copy shared libraries needed by dynamically linked tools.
# The recovery partition is a self-contained rootfs, so all libs must be present.
info "  Copying shared libraries for recovery tools..."
NEEDED_LIBS=$(mktemp)
for tool_bin in "${RECOVERY_MNT}/bin/"*; do
    tool_name=$(basename "$tool_bin")
    # Find the original path in rootfs for ldd
    ORIG_PATH=$(chroot "$ROOTFS_MNT" which "$tool_name" 2>/dev/null || true)
    if [[ -n "$ORIG_PATH" ]]; then
        chroot "$ROOTFS_MNT" ldd "$ORIG_PATH" 2>/dev/null | \
            grep "=>" | awk '{print $3}' >> "$NEEDED_LIBS" || true
    fi
done

# Copy unique libraries
sort -u "$NEEDED_LIBS" | while read -r lib; do
    if [[ -n "$lib" && -f "${ROOTFS_MNT}${lib}" ]]; then
        cp -L "${ROOTFS_MNT}${lib}" "${RECOVERY_MNT}/lib/" 2>/dev/null || true
    fi
done
rm -f "$NEEDED_LIBS"

# Copy the dynamic linker (ELF interpreter) -- path is hardcoded in binaries
for ld_path in /lib/ld-linux-aarch64.so.1 /lib/aarch64-linux-gnu/ld-linux-aarch64.so.1; do
    if [[ -e "${ROOTFS_MNT}${ld_path}" ]]; then
        cp -L "${ROOTFS_MNT}${ld_path}" "${RECOVERY_MNT}/lib/ld-linux-aarch64.so.1"
        info "  Copied dynamic linker from ${ld_path}"
        break
    fi
done

# Safety symlink for multiarch interpreter path
mkdir -p "${RECOVERY_MNT}/lib/aarch64-linux-gnu"
ln -sf /lib/ld-linux-aarch64.so.1 "${RECOVERY_MNT}/lib/aarch64-linux-gnu/ld-linux-aarch64.so.1"

# Copy WiFi firmware for the Pi Zero 2 W.
# The chip is BCM43430 but firmware files use both 43430 and 43436 names
# (43430 files are often symlinks to 43436 variants). Copy both sets.
info "  Copying WiFi firmware..."
if [[ -d "${ROOTFS_MNT}/lib/firmware/brcm" ]]; then
    mkdir -p "${RECOVERY_MNT}/lib/firmware/brcm"
    # Copy 43436 firmware (real files)
    cp -a "${ROOTFS_MNT}/lib/firmware/brcm/brcmfmac43436"* "${RECOVERY_MNT}/lib/firmware/brcm/" 2>/dev/null || true
    cp -a "${ROOTFS_MNT}/lib/firmware/brcm/brcmfmac43436s"* "${RECOVERY_MNT}/lib/firmware/brcm/" 2>/dev/null || true
    # Copy 43430 firmware (resolving symlinks to real files)
    for f in "${ROOTFS_MNT}/lib/firmware/brcm/brcmfmac43430"*; do
        [[ -e "$f" ]] || continue
        NAME=$(basename "$f")
        if [[ -L "$f" ]]; then
            # Resolve symlink, copy the target with the 43430 name
            TARGET=$(readlink -f "$f" 2>/dev/null || true)
            if [[ -n "$TARGET" && -f "$TARGET" ]]; then
                cp "$TARGET" "${RECOVERY_MNT}/lib/firmware/brcm/${NAME}"
            fi
        else
            cp "$f" "${RECOVERY_MNT}/lib/firmware/brcm/${NAME}"
        fi
    done
fi

# Copy busybox and create shell/modprobe symlinks.
# Recovery partition has no init system -- busybox provides /bin/sh (for
# scripts), /sbin/modprobe (kernel calls this via request_module), and
# /sbin/insmod as fallback.
info "  Installing busybox and symlinks..."
install -m 755 "${ROOTFS_MNT}/bin/busybox" "${RECOVERY_MNT}/bin/busybox"
# libresolv is busybox's only extra dependency beyond libc
for lib in libresolv.so.2; do
    LIBPATH=$(chroot "$ROOTFS_MNT" readlink -f "/usr/lib/aarch64-linux-gnu/${lib}" 2>/dev/null || true)
    if [[ -n "$LIBPATH" && -f "${ROOTFS_MNT}${LIBPATH}" ]]; then
        cp -L "${ROOTFS_MNT}${LIBPATH}" "${RECOVERY_MNT}/lib/${lib}"
    fi
done
for tool in sh insmod modprobe; do
    ln -sf /bin/busybox "${RECOVERY_MNT}/sbin/${tool}"
done

# Copy WiFi kernel modules (decompressed) and create modules.dep.
# The recovery binary loads brcmfmac via modprobe; the kernel's
# request_module("brcmfmac-wcc") also needs modprobe infrastructure.
info "  Copying WiFi kernel modules..."
KVER=$(ls "${ROOTFS_MNT}/lib/modules/" | grep rpi-v8 | head -1)
KDIR="${ROOTFS_MNT}/lib/modules/${KVER}"
RECOVERY_KDIR="${RECOVERY_MNT}/lib/modules/${KVER}"
mkdir -p "$RECOVERY_KDIR"
for mod_path in \
    kernel/net/rfkill/rfkill.ko.xz \
    kernel/net/wireless/cfg80211.ko.xz \
    kernel/drivers/net/wireless/broadcom/brcm80211/brcmutil/brcmutil.ko.xz \
    kernel/drivers/net/wireless/broadcom/brcm80211/brcmfmac/brcmfmac.ko.xz \
    kernel/drivers/net/wireless/broadcom/brcm80211/brcmfmac/bca/brcmfmac-bca.ko.xz \
    kernel/drivers/net/wireless/broadcom/brcm80211/brcmfmac/wcc/brcmfmac-wcc.ko.xz; do
    NAME=$(basename "${mod_path%.xz}")
    if [[ -f "${KDIR}/${mod_path}" ]]; then
        xz -dk -c "${KDIR}/${mod_path}" > "${RECOVERY_KDIR}/${NAME}"
        info "    ${NAME}"
    else
        warn "    Module not found: ${mod_path}"
    fi
done

cat > "${RECOVERY_KDIR}/modules.dep" << 'MODDEP'
brcmfmac-wcc.ko: brcmfmac.ko brcmutil.ko cfg80211.ko rfkill.ko
brcmfmac-bca.ko: brcmfmac.ko brcmutil.ko cfg80211.ko rfkill.ko
brcmfmac.ko: brcmutil.ko cfg80211.ko rfkill.ko
cfg80211.ko: rfkill.ko
brcmutil.ko:
rfkill.ko:
MODDEP

cat > "${RECOVERY_KDIR}/modules.alias" << 'MODALIAS'
alias brcmfmac-wcc brcmfmac-wcc
alias brcmfmac-bca brcmfmac-bca
alias brcmfmac_wcc brcmfmac-wcc
alias brcmfmac_bca brcmfmac-bca
MODALIAS

touch "${RECOVERY_KDIR}/modules.dep.bin" \
      "${RECOVERY_KDIR}/modules.alias.bin" \
      "${RECOVERY_KDIR}/modules.softdep" \
      "${RECOVERY_KDIR}/modules.symbols"

# Create minimal /etc for dnsmasq (needs passwd for user= directive)
# and glibc (needs nsswitch.conf for name resolution).
info "  Creating minimal /etc..."
mkdir -p "${RECOVERY_MNT}/etc"
echo "digits" > "${RECOVERY_MNT}/etc/hostname"
echo "root:x:0:0:root:/root:/bin/sh" > "${RECOVERY_MNT}/etc/passwd"
echo "root:x:0:" > "${RECOVERY_MNT}/etc/group"
printf "passwd: files\ngroup: files\nhosts: files dns\n" > "${RECOVERY_MNT}/etc/nsswitch.conf"

# Symlink /var/run -> /run (tmpfs from initramfs)
mkdir -p "${RECOVERY_MNT}/var"
ln -sf /run "${RECOVERY_MNT}/var/run"

info "  Rebuilding initramfs (chroot)..."
chroot "$ROOTFS_MNT" /bin/bash -c "update-initramfs -u"

for mp in dev/pts dev/shm dev proc sys; do
    umount "${ROOTFS_MNT}/${mp}" 2>/dev/null || true
done
rm -f "${ROOTFS_MNT}/usr/bin/qemu-aarch64-static"

info "  Unmounting recovery partition..."
umount "$RECOVERY_MNT"
RECOVERY_MNT=""

# ── step 16: configure boot (host-side) ─────────────────────────────────────

info "Configuring boot parameters..."

CONFIG_TXT="${BOOT_MNT}/config.txt"

info "Configuring config.txt for Digits..."

# Remove any existing Digits lines from previous builds (idempotent)
sed -i '/# Digits:/,+1d' "$CONFIG_TXT"

# Ensure [all] section exists
if ! grep -q '^\[all\]' "$CONFIG_TXT"; then
    printf '\n[all]\n' >> "$CONFIG_TXT"
fi

# Remove any existing Digits config after [all]
sed -i '/^\[all\]$/,$ { /^over_voltage=/d; /^enable_uart=/d; /^dtoverlay=disable-bt/d; /^dtoverlay=digits-codec/d; /^dtparam=audio=/d; }' "$CONFIG_TXT"

# Append Digits config
cat >> "$CONFIG_TXT" << 'DIGITS_CONFIG'
over_voltage=2
enable_uart=1
dtoverlay=disable-bt
DIGITS_CONFIG

# V2 carrier board has an onboard TLV320AIC3104 codec that needs an explicit
# overlay. V1/prototype uses the Codec Zero HAT which auto-loads via HAT EEPROM,
# so the digits-codec overlay must stay off.
if [[ "$PCB_MODE" == true ]]; then
    echo "dtoverlay=digits-codec" >> "$CONFIG_TXT"
fi

# Set audio off (headless, saves resources)
sed -i 's/^dtparam=audio=on/dtparam=audio=off/' "$CONFIG_TXT"

# Enable I2C and I2S (needed for audio codec)
sed -i 's/^#dtparam=i2c_arm=on/dtparam=i2c_arm=on/' "$CONFIG_TXT"
sed -i 's/^#dtparam=i2s=on/dtparam=i2s=on/' "$CONFIG_TXT"

# ── step 17: apply read-only root cmdline (host-side) ───────────────────────

info "Configuring read-only root filesystem..."

CMDLINE="${BOOT_MNT}/cmdline.txt"
if [[ -f "$CMDLINE" ]]; then
    # Remove Raspbian firstboot init — its initramfs resize script matches
    # "firstboot" in cmdline and expands p2 to fill the disk, destroying p3.
    # Our digits-first-boot.service handles identity/SSH setup instead.
    sed -i 's| init=/usr/lib/raspberrypi-sys-mods/firstboot||' "$CMDLINE"
    # Remove quiet (no splash screen, want to see boot messages)
    sed -i 's/ quiet//g' "$CMDLINE"
    # Remove splash
    sed -i 's/ splash//g' "$CMDLINE"
    sed -i 's/ plymouth.ignore-serial-consoles//g' "$CMDLINE"
    # Remove fsck.repair=yes (incompatible with ro root)
    sed -i 's/fsck\.repair=yes //g' "$CMDLINE"
    # Remove serial console (conflicts with disable-bt UART for Pico)
    sed -i 's/console=serial0,[0-9]* //g' "$CMDLINE"
    # Add fsck.mode=skip
    if ! grep -q 'fsck.mode=skip' "$CMDLINE"; then
        sed -i 's/rootwait/fsck.mode=skip rootwait/' "$CMDLINE"
    fi
    # Add 'ro' after 'rootwait' if not already present
    if ! grep -q '\bro\b' "$CMDLINE"; then
        sed -i 's/rootwait/rootwait ro/' "$CMDLINE"
    fi
fi

# ── step 18: configure fstab (host-side) ────────────────────────────────────

info "Configuring /etc/fstab with correct PARTUUIDs..."

PARTUUID_P1=$(blkid -s PARTUUID -o value "$P1") || die "Could not determine PARTUUID for $P1"
PARTUUID_P2=$(blkid -s PARTUUID -o value "$P2") || die "Could not determine PARTUUID for $P2"
PARTUUID_P3=$(blkid -s PARTUUID -o value "$P3") || die "Could not determine PARTUUID for $P3"
PARTUUID_P4=$(blkid -s PARTUUID -o value "$P4") || die "Could not determine PARTUUID for $P4"

info "  P1 PARTUUID: $PARTUUID_P1"
info "  P2 PARTUUID: $PARTUUID_P2"
info "  P3 PARTUUID: $PARTUUID_P3"
info "  P4 PARTUUID: $PARTUUID_P4"

# Create mount point for recovery partition
mkdir -p "${ROOTFS_MNT}/recovery"

cat > "${ROOTFS_MNT}/etc/fstab" << EOF
# /etc/fstab — Digits Pi (generated by build-image.sh)
#
# Read-only root + writable /data partition with bind mounts.

# Boot partition (read-only)
PARTUUID=${PARTUUID_P1}  /boot/firmware  vfat  defaults,ro,noatime  0  2

# Root filesystem (read-only)
PARTUUID=${PARTUUID_P2}  /  ext4  defaults,ro,noatime  0  1

# Recovery partition (read-only, no automount -- initramfs switch_root
# mounts this directly as rootfs during recovery mode)
PARTUUID=${PARTUUID_P3}  /recovery       ext4    defaults,ro,noatime,noauto  0  0

# Writable data partition (journaled)
PARTUUID=${PARTUUID_P4}  /data           ext4    defaults,noatime     0    2

# Bind mounts from /data
/data/log          /var/log        none    bind                 0  0
/data/tmp          /tmp            none    bind                 0  0
/data/digits       /home/digits    none    bind                 0  0
EOF

# ── step 19: set hostname (host-side) ───────────────────────────────────────

info "Setting default hostname to 'digits'..."
echo "digits" > "${ROOTFS_MNT}/etc/hostname"
sed -i 's/^127\.0\.1\.1.*/127.0.1.1\tdigits/' "${ROOTFS_MNT}/etc/hosts" || \
    echo "127.0.1.1	digits" >> "${ROOTFS_MNT}/etc/hosts"

# ── step 20: enable/disable/mask systemd services (host-side) ───────────────

info "Configuring systemd services (host-side symlinks)..."

# Disable services that break on ro root or are unnecessary
hostside_disable_service "$ROOTFS_MNT" "dphys-swapfile.service"
hostside_mask_service    "$ROOTFS_MNT" "dphys-swapfile.service"
hostside_disable_service "$ROOTFS_MNT" "hciuart.service"
hostside_mask_service    "$ROOTFS_MNT" "hciuart.service"
hostside_disable_service "$ROOTFS_MNT" "bluetooth.service"
hostside_mask_service    "$ROOTFS_MNT" "bluetooth.service"
hostside_disable_service "$ROOTFS_MNT" "systemd-networkd.service"
hostside_disable_service "$ROOTFS_MNT" "systemd-networkd-wait-online.service"
hostside_disable_service "$ROOTFS_MNT" "hostapd.service"
hostside_disable_service "$ROOTFS_MNT" "dnsmasq.service"

# Mask systemd-rfkill — reads stale state file on ro root and re-blocks WiFi
hostside_mask_service "$ROOTFS_MNT" "systemd-rfkill.service"
hostside_mask_service "$ROOTFS_MNT" "systemd-rfkill.socket"

# Mask packaging maintenance services that fail on a ro root: they try to
# write to /var/cache and /var/lib paths we keep read-only. They are not
# needed on a Digits device.
hostside_mask_service "$ROOTFS_MNT" "dpkg-db-backup.service"
hostside_mask_service "$ROOTFS_MNT" "dpkg-db-backup.timer"
hostside_mask_service "$ROOTFS_MNT" "logrotate.service"
hostside_mask_service "$ROOTFS_MNT" "logrotate.timer"

# Enable Digits services
hostside_enable_service "$ROOTFS_MNT" "digits-first-boot.service"
hostside_enable_service "$ROOTFS_MNT" "digits-ap-check.service"
hostside_enable_service "$ROOTFS_MNT" "digits-mixer.service"
hostside_enable_service "$ROOTFS_MNT" "digitsd.service"

# ── step 21: verify critical system files (host-side) ───────────────────────

info "Verifying critical system files..."

# Verify /etc/passwd has root entry
grep -q '^root:' "${ROOTFS_MNT}/etc/passwd" || die "/etc/passwd is missing root entry"

# Verify /etc/shadow is not empty and has root
[[ -s "${ROOTFS_MNT}/etc/shadow" ]] || die "/etc/shadow is empty"
grep -q '^root:' "${ROOTFS_MNT}/etc/shadow" || die "/etc/shadow is missing root entry"

# Verify /etc/group
if [[ ! -s "${ROOTFS_MNT}/etc/group" ]]; then
    warn "/etc/group is empty — restoring from reference file"
    if [[ -f "${SCRIPT_DIR}/reference-files/etc-group" ]]; then
        cp "${SCRIPT_DIR}/reference-files/etc-group" "${ROOTFS_MNT}/etc/group"
    else
        die "/etc/group is empty and no reference file found."
    fi
fi
info "  /etc/passwd: $(wc -l < "${ROOTFS_MNT}/etc/passwd") entries"
info "  /etc/shadow: $(wc -l < "${ROOTFS_MNT}/etc/shadow") entries"
info "  /etc/group:  $(wc -l < "${ROOTFS_MNT}/etc/group") entries"

# Verify digits user was created
grep -q '^digits:' "${ROOTFS_MNT}/etc/passwd" || die "digits user not found in /etc/passwd"
grep -q '^digits:' "${ROOTFS_MNT}/etc/shadow" || die "digits user not found in /etc/shadow"

# ── step 22: dev mode — enable SSH + create dev user (host-side) ─────────────

if [[ "$DEV_MODE" == true ]]; then
    info "DEV MODE: Enabling SSH and creating dev user (host-side)..."

    # Enable SSH service (host-side symlink)
    hostside_enable_service "$ROOTFS_MNT" "ssh.service"

    # Hash password on host — no qemu
    DEV_HASH=$(openssl passwd -6 'digits')

    # Create dev user entirely host-side
    hostside_adduser "$ROOTFS_MNT" "dev" 1001 1001 "/home/dev" "/bin/bash" "$DEV_HASH" ""

    # Add to supplementary groups
    hostside_add_to_groups "$ROOTFS_MNT" "dev" sudo audio video dialout gpio i2c spi

    # Allow passwordless sudo for dev user
    echo 'dev ALL=(ALL) NOPASSWD:ALL' > "${ROOTFS_MNT}/etc/sudoers.d/dev-nopasswd"
    chmod 440 "${ROOTFS_MNT}/etc/sudoers.d/dev-nopasswd"

    # Enable password authentication for SSH (Bookworm defaults to disabled)
    mkdir -p "${ROOTFS_MNT}/etc/ssh/sshd_config.d"
    cat > "${ROOTFS_MNT}/etc/ssh/sshd_config.d/digits-dev.conf" << 'SSHDEV'
PasswordAuthentication yes
SSHDEV

    # Create SSH host keys directory on /data (writable)
    mkdir -p "${DATA_MNT}/ssh"

    # Write a tmpfiles rule so SSH host keys live on /data
    mkdir -p "${ROOTFS_MNT}/etc/tmpfiles.d"
    cat > "${ROOTFS_MNT}/etc/tmpfiles.d/digits-ssh.conf" << 'SSHCONF'
L /etc/ssh/ssh_host_rsa_key - - - - /data/ssh/ssh_host_rsa_key
L /etc/ssh/ssh_host_ed25519_key - - - - /data/ssh/ssh_host_ed25519_key
SSHCONF

    # Pre-generate SSH host keys on the HOST (not in chroot)
    ssh-keygen -t rsa -b 4096 -f "${DATA_MNT}/ssh/ssh_host_rsa_key" -N '' -q
    ssh-keygen -t ed25519 -f "${DATA_MNT}/ssh/ssh_host_ed25519_key" -N '' -q

    info "DEV MODE: SSH enabled — user 'dev', password 'digits', passwordless sudo"
fi

# ── step 23: final cleanup (host-side) ──────────────────────────────────────

info "Final cleanup..."

# Point resolv.conf at NM runtime file
rm -f "${ROOTFS_MNT}/etc/resolv.conf"
ln -s /run/NetworkManager/resolv.conf "${ROOTFS_MNT}/etc/resolv.conf"

# Clear machine-id (regenerated on first boot)
truncate -s 0 "${ROOTFS_MNT}/etc/machine-id" 2>/dev/null || true
rm -f "${ROOTFS_MNT}/var/lib/dbus/machine-id" 2>/dev/null || true

# Create timesyncd state directory (needed for read-only root — systemd's
# StateDirectory=timesync can't create it when / is mounted ro)
TIMESYNC_UID=$(awk -F: '/^systemd-timesync:/{print $3}' "${ROOTFS_MNT}/etc/passwd")
TIMESYNC_GID=$(awk -F: '/^systemd-timesync:/{print $4}' "${ROOTFS_MNT}/etc/passwd")
if [[ -n "$TIMESYNC_UID" ]]; then
    install -d -m 755 -o "$TIMESYNC_UID" -g "$TIMESYNC_GID" "${ROOTFS_MNT}/var/lib/systemd/timesync"
    info "  Created /var/lib/systemd/timesync (uid=$TIMESYNC_UID)"
else
    info "  WARNING: systemd-timesync user not found, skipping timesync dir"
fi

# Set rfkill state file to unblocked
echo -n '0' > "${ROOTFS_MNT}/var/lib/systemd/rfkill/platform-3f300000.mmcnr:wlan" 2>/dev/null || true

# Set NetworkManager state to wifi enabled
cat > "${ROOTFS_MNT}/var/lib/NetworkManager/NetworkManager.state" << 'NMSTATE'
[main]
NetworkingEnabled=true
WirelessEnabled=true
WWANEnabled=true
NMSTATE

# Clean up /data artifacts left from build (first-boot must run fresh)
info "Cleaning build artifacts from /data partition..."
rm -f "${DATA_MNT}/.initialized" 2>/dev/null || true
rm -f "${DATA_MNT}/log/digits-first-boot.log" 2>/dev/null || true
rm -f "${DATA_MNT}/digits/device-id" 2>/dev/null || true
rm -rf "${DATA_MNT}/log/journal/"* 2>/dev/null || true

# ── step 23b: create rootfs snapshot (deferred) ─────────────────────────────
# The snapshot must be taken AFTER all rootfs modifications (services enabled,
# config applied, dev mode, etc.) so factory reset restores a fully configured
# system. We re-mount the recovery partition temporarily to write the image.

info "Creating rootfs snapshot..."

RECOVERY_MNT_SNAP=$(mktemp -d /tmp/digits-recovery-snap-XXXXXX)
mount "$P3" "$RECOVERY_MNT_SNAP"

info "  Freezing rootfs for consistent snapshot..."
sync
fsfreeze --freeze "$ROOTFS_MNT"

info "  Creating compressed rootfs snapshot (this may take a while)..."
dd if="$P2" bs=4M | zstd -T0 -o "${RECOVERY_MNT_SNAP}/rootfs.img.zst"

fsfreeze --unfreeze "$ROOTFS_MNT"

info "  Unmounting recovery partition..."
umount "$RECOVERY_MNT_SNAP"
rmdir "$RECOVERY_MNT_SNAP"

# ── step 24: unmount everything ──────────────────────────────────────────────

info "Unmounting partitions..."

sync

umount "$DATA_MNT"
DATA_MNT=""
umount "$BOOT_MNT"
BOOT_MNT=""
umount "$ROOTFS_MNT"

# Detach loop device
info "Detaching loop device..."
detach_loop

# Clean up mount point dirs
rmdir "$ROOTFS_MNT" 2>/dev/null || true
ROOTFS_MNT=""

# ── step 25: shrink image ───────────────────────────────────────────────────

info "Shrinking image..."

# Re-attach for shrinking
LOOP_DEV=$(losetup --find --show --partscan "$WORK_IMG")
ensure_partitions "$LOOP_DEV"

# Get the end of the last partition (in bytes)
LAST_SECTOR=$(parted -ms "$LOOP_DEV" unit s print | tail -1 | cut -d: -f3 | tr -d 's')
IMAGE_END_BYTES=$(( (LAST_SECTOR + 1) * 512 ))

# Add 1MiB padding for safety
IMAGE_END_BYTES=$(( IMAGE_END_BYTES + 1048576 ))

detach_loop

info "Truncating image to $(( IMAGE_END_BYTES / 1024 / 1024 )) MiB..."
truncate -s "$IMAGE_END_BYTES" "$WORK_IMG"

# ── step 26: compress ───────────────────────────────────────────────────────

info "Compressing image with gzip..."
gzip -f "$WORK_IMG"

FINAL_OUTPUT="${OUTPUT_NAME}.gz"
FINAL_SIZE=$(stat -c %s "$FINAL_OUTPUT")

info ""
info "════════════════════════════════════════════════════════════════"
info "  Build complete!"
info "  Output: ${FINAL_OUTPUT}"
info "  Size:   $(( FINAL_SIZE / 1024 / 1024 )) MiB"
info ""
info "  Flash with:"
info "    gunzip -c ${FINAL_OUTPUT} | sudo dd of=/dev/sdX bs=4M status=progress"
info "  Or use Raspberry Pi Imager (supports .img.gz directly)"
info "════════════════════════════════════════════════════════════════"
