# Digits Pi — SD Card Image Builder

Build a flashable SD card image for Digits Pi phones from a stock Raspberry Pi OS Lite image.

## Prerequisites

### Host System

- **x86_64 Linux** (tested on Debian/Ubuntu)
- **Root access** (sudo) for loop devices, mounts, and chroot

### Required Packages

```bash
sudo apt install qemu-user-static parted e2fsprogs gzip rsync
```

This installs:
- `qemu-user-static` — ARM64 emulation for chroot
- `parted` — partition manipulation
- `e2fsprogs` — ext4 tools (e2fsck, resize2fs, mkfs.ext4)
- `gzip` — image compression
- `rsync` — overlay file copying

### Go Cross-Compilation

You need Go installed to cross-compile the Digits binaries for ARM64.

## Build Steps

### 1. Cross-compile binaries

From the repo root:

```bash
cd pi/digitsd && GOOS=linux GOARCH=arm64 go build -o ../../tools/build/digitsd ./cmd/digitsd/
cd ../../
cd pi/digits-setup && GOOS=linux GOARCH=arm64 go build -o ../../tools/build/digits-setup ./cmd/digits-setup/
cd ../../
```

This places the ARM64 binaries in `tools/build/`.

### 2. Download Raspberry Pi OS Lite

Download the official **Raspberry Pi OS Lite (Bookworm, 64-bit)** image:

```bash
wget https://downloads.raspberrypi.com/raspios_lite_arm64/images/raspios_lite_arm64-2024-11-19/2024-11-19-raspios-bookworm-arm64-lite.img.xz
```

(Check https://www.raspberrypi.com/software/operating-systems/ for the latest version.)

### 3. Build the image

```bash
sudo ./tools/build-image.sh 2024-11-19-raspios-bookworm-arm64-lite.img.xz
```

The script accepts `.img` or `.img.xz` files.

### 4. Flash

The output is `digits-pi-YYYYMMDD.img.gz`. Flash it to an SD card:

```bash
# Using dd
gunzip -c digits-pi-20260328.img.gz | sudo dd of=/dev/sdX bs=4M status=progress

# Or use Raspberry Pi Imager (supports .img.gz natively)
```

## What the Script Does

1. **Decompresses** the source image (if `.img.xz`)
2. **Expands** the image to ~7 GiB for the /data partition
3. **Runs `partition-setup.sh`** — shrinks rootfs to ~4 GB, creates ~2 GB `/data` partition (p3)
4. **Mounts** all three partitions (boot, rootfs, data) via loop device
5. **Sets up chroot** with qemu-user-static for ARM64 emulation
6. **Installs packages**: `hostapd`, `dnsmasq`, `alsa-utils`
7. **Creates `digits` user** (system user, nologin shell)
8. **Copies binaries**: `digitsd` and `digits-setup` → `/usr/local/bin/`
9. **Copies rootfs overlay**: systemd services, hostapd config, dnsmasq config, helper scripts
10. **Copies tone WAV files** to `/data/digits/tones/`
11. **Initializes /data** directory structure (via `init-data.sh`)
12. **Configures boot**:
    - `dtoverlay=disable-bt` (frees UART for Pico serial)
    - `gpu_mem=16` (headless, minimal GPU)
    - `enable_uart=1`
13. **Applies read-only root**:
    - Adds `ro` to kernel cmdline
    - Removes `fsck.repair=yes`
    - Writes `/etc/fstab` with correct PARTUUIDs and bind mounts
14. **Disables** swap (`dphys-swapfile`) and Bluetooth (`hciuart`, `bluetooth`)
15. **Enables** Digits systemd services (first-boot, AP check, mount units)
16. **Sets hostname** to `digits` (randomized on first boot by `digits-first-boot.service`)
17. **Shrinks** image to remove trailing free space
18. **Compresses** with gzip

## Partition Layout

| Partition | Mount Point | Filesystem | Mode | Size |
|-----------|-------------|------------|------|------|
| p1 | /boot/firmware | vfat | read-only | ~512 MB |
| p2 | / | ext4 | read-only | ~4 GB |
| p3 | /data | ext4 (journaled) | read-write | ~2 GB |

## First Boot

On first power-on, the device will:

1. **Generate unique identity** — hostname `digits-XXXX`, SSH keys, device ID (from Pi serial)
2. **Start AP mode** — broadcasts `Digits-XXXX` Wi-Fi network
3. **Serve captive portal** — user connects to Wi-Fi, configures home network + pairing code
4. **Reboot to normal mode** — connects to configured Wi-Fi, pairs with server

Total setup time: ~2 minutes.

## Directory Structure

```
tools/
├── build-image.sh          # This script
├── README-image-builder.md # This file
└── build/                  # Cross-compiled binaries (gitignored)
    ├── digitsd             # ARM64 phone daemon
    └── digits-setup        # ARM64 captive portal server

pi/image/
├── partition-setup.sh      # Creates /data partition
├── init-data.sh            # Initializes /data directory structure
└── rootfs-overlay/         # Files copied into the image rootfs
    ├── boot/firmware/      # Boot config fragments
    ├── etc/                # System configs, systemd units
    └── usr/local/bin/      # Helper scripts
```

## Troubleshooting

### "qemu-aarch64 binfmt not registered"

```bash
sudo apt install qemu-user-static
sudo systemctl restart systemd-binfmt
```

### "Missing digitsd / digits-setup"

Cross-compile first:

```bash
cd pi/digitsd && GOOS=linux GOARCH=arm64 go build -o ../../tools/build/digitsd ./cmd/digitsd/
cd pi/digits-setup && GOOS=linux GOARCH=arm64 go build -o ../../tools/build/digits-setup ./cmd/digits-setup/
```

### Build fails with mount errors

Ensure no stale loop devices from a previous failed build:

```bash
sudo losetup -D  # Detach all loop devices (use carefully!)
```

### Image won't boot

Check that the base image is **Raspberry Pi OS Lite Bookworm 64-bit** (arm64). The 32-bit image will not work with the ARM64 binaries.
