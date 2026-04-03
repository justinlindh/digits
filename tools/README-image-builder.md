# Digits Pi -- SD Card Image Builder

Build a flashable SD card image for Digits Pi phones from a stock Raspberry Pi OS Lite image.

## Quick Start (Docker)

The easiest way to build. Only requires Docker on your machine.

```bash
# Build the image (downloads Pi OS automatically on first run)
./pi/image/build-docker.sh

# Flash to SD card
gunzip -c digits-pi-*.img.gz | sudo dd of=/dev/sdX bs=4M status=progress
```

The Docker container handles everything: downloading the base Pi OS image, cross-compiling the Go binaries, setting up QEMU for ARM64 chroot, partitioning, and packaging. No host dependencies needed beyond Docker.

The base Pi OS image is cached in a Docker volume (`digits-image-cache`) so it's only downloaded once. You can also provide your own:

```bash
./pi/image/build-docker.sh path/to/raspios-lite.img.xz
```

### Dev mode

Adds SSH access with user `dev` / password `digits` for debugging:

```bash
./pi/image/build-docker.sh --dev
```

## Manual Build (No Docker)

If you prefer to build without Docker, or need to debug the build process.

### Prerequisites

- **x86_64 Linux** (tested on Debian/Ubuntu)
- **Root access** (sudo) for loop devices, mounts, and chroot

```bash
# Build tools
sudo apt install qemu-user-static parted e2fsprogs gzip rsync

# Go cross-compilation (for digitsd)
sudo apt install gcc-aarch64-linux-gnu

# ARM64 libraries (for CGO dependencies)
sudo dpkg --add-architecture arm64
sudo apt install libasound2-dev:arm64 libopus-dev:arm64 libopusfile-dev:arm64
```

You also need Go 1.22+ installed.

### Build Steps

```bash
# 1. Cross-compile binaries
mkdir -p tools/build

cd pi/digitsd
PKG_CONFIG_PATH=/usr/lib/aarch64-linux-gnu/pkgconfig \
  CGO_ENABLED=1 CC=aarch64-linux-gnu-gcc GOOS=linux GOARCH=arm64 \
  go build -o ../../tools/build/digitsd ./cmd/digitsd/
cd ../..

cd pi/digits-setup
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -o ../../tools/build/digits-setup ./cmd/digits-setup/
cd ../..

# 2. Download Raspberry Pi OS Lite (Bookworm, 64-bit)
wget https://downloads.raspberrypi.com/raspios_lite_arm64/images/raspios_lite_arm64-2024-11-19/2024-11-19-raspios-bookworm-arm64-lite.img.xz

# 3. Build the image
sudo ./tools/build-image.sh 2024-11-19-raspios-bookworm-arm64-lite.img.xz

# 4. Flash to SD card
gunzip -c digits-pi-*.img.gz | sudo dd of=/dev/sdX bs=4M status=progress
```

## What the Script Does

1. **Decompresses** the source image (if `.img.xz`)
2. **Expands** the image to ~7 GiB for the /data partition
3. **Creates partitions** -- shrinks rootfs to ~4 GB, creates ~2 GB `/data` partition
4. **Mounts** all three partitions via loop device
5. **Chroot with QEMU** -- installs packages (`hostapd`, `dnsmasq`, `alsa-utils`, etc.)
6. **Purges bloat** -- removes ~900 MB of unnecessary packages (GPU, Bluetooth, dev tools)
7. **Creates `digits` user** and copies ARM64 binaries
8. **Applies rootfs overlay** -- systemd services, configs, helper scripts
9. **Copies tone WAV files** to `/data/digits/tones/`
10. **Configures boot** -- UART, I2C, read-only root, disable Bluetooth
11. **Enables services** -- first-boot, AP check, mixer restore, digitsd
12. **Shrinks and compresses** the final image

## Partition Layout

| Partition | Mount Point | Filesystem | Mode | Size |
|-----------|-------------|------------|------|------|
| p1 | /boot/firmware | vfat | read-only | ~512 MB |
| p2 | / | ext4 | read-only | ~4 GB |
| p3 | /data | ext4 (journaled) | read-write | ~2 GB |

## First Boot

On first power-on, the device will:

1. **Generate unique identity** -- hostname `digits-XXXX`, SSH keys, device ID
2. **Start AP mode** -- broadcasts `Digits-XXXX` Wi-Fi network
3. **Serve captive portal** -- user connects, configures home network + pairing code
4. **Reboot to normal mode** -- connects to configured Wi-Fi, pairs with server

Total setup time: ~2 minutes.

## Troubleshooting

### "qemu-aarch64 binfmt not registered"

```bash
sudo apt install qemu-user-static
sudo systemctl restart systemd-binfmt
```

### Build fails with mount errors

Ensure no stale loop devices from a previous failed build:

```bash
sudo losetup -D  # Detach all loop devices (use carefully!)
```

### Image won't boot

Check that the base image is **Raspberry Pi OS Lite Bookworm 64-bit** (arm64). The 32-bit image will not work.
