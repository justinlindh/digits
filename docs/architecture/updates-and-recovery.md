# Updates and Recovery

## Overview

`digitsd` is the single update vector for the device. All deployable files -- tones, shell scripts, systemd units, sudoers config, SWD config -- are embedded directly in the binary using Go's `embed` package. When a new version of `digitsd` is deployed via OTA, it carries the latest versions of all those files and installs them on startup.

This design means there is no separate update agent, no package manager, and no dependency on external servers beyond the initial binary delivery. The binary is self-contained.

Pico firmware is handled separately and is flashed through the web UI, not through the embedded asset system.

## What Gets Updated

The following file categories are embedded in `digitsd` and written to the device on version change:

- **Tones** -- `.wav` files for dial tone, ringback, busy, and other audio cues. Written to `/data/digits/tones/`.
- **Shell scripts** -- `flash-pico.sh`, `digits-ap-check`, and other helpers. Written to rootfs paths.
- **systemd units** -- service and timer files for `digitsd` and related services.
- **sudoers config** -- grants `digitsd` the specific `sudo` permissions it needs without a full root shell.
- **SWD config** -- OpenOCD configuration for Pico firmware flashing over SWD.

Pico firmware (`.uf2`) is not embedded in `digitsd`. It is uploaded separately via the web UI and flashed by `digitsd` using `flash-pico.sh`.

## How Updates Work

On every startup, `digitsd` compares its compiled-in asset version to a version marker on the device:

```
/data/digits/asset-version
```

The version marker contains a short string (e.g. a commit hash or build timestamp) written by the previous update run.

**If the versions differ** (or the marker is missing), `digitsd`:

1. Remounts the rootfs read-write
2. Writes rootfs files (scripts, systemd units, sudoers, SWD config) to their target paths
3. Remounts the rootfs read-only
4. Writes data partition files (tones, other mutable assets) to `/data/digits/`
5. Writes the new version string to `/data/digits/asset-version`

**If the versions match**, extraction is skipped entirely. Startup proceeds immediately without touching the filesystem.

This check-and-skip approach keeps normal startup fast and avoids unnecessary writes to the flash storage.

## Factory Reset

A factory reset restores the device to its out-of-box state: rootfs is repopulated from the compressed factory image stored on the recovery partition, the data partition is wiped, and the device reboots into first-boot setup.

### How to Trigger

Two paths:

- **Web UI:** The "Factory Reset" button on the phone detail page in the signald admin panel.
- **Service code:** Dial `*#00000#` on the phone keypad.

### What Happens

1. The device reboots into recovery mode (see below).
2. The user connects to the "Digits" Wi-Fi AP broadcast by the recovery binary.
3. A confirmation web page is served at `192.168.4.1`.
4. The user selects "Factory Reset" to proceed, or "Try Again" to abort.

The reset itself (rootfs restore + data wipe) runs only after the user confirms on the recovery page.

## Recovery Mode

Recovery mode is a minimal environment that runs before the main rootfs is mounted. It is used both for factory reset and for recovering from boot failures.

### Triggers

Recovery mode activates in three situations:

- **Boot failure:** The initramfs maintains a boot counter on the data partition. If the counter reaches 3 consecutive failed boots, recovery mode starts automatically. If the data partition cannot be mounted at all, recovery mode starts as a safety fallback.
- **Factory reset request:** When a factory reset is initiated (via web UI or service code), `digitsd` sets the boot counter to the threshold value and reboots. The initramfs sees the counter at threshold and enters recovery mode.
- **Numpad panic button:** Hold the keypad's `*` key while the phone boots. The `digits-panic-check` early-boot service reads the keypad matrix from the Pico over UART and, if `*` is held, writes `/data/digits/recovery-mode` and reboots. The initramfs sees the persistent flag and enters recovery mode. This is the user-facing escape hatch when the device is unreachable over the network or the web UI.

### Boot Counter

The counter lives on the data partition (`/data/digits/boot-counter`), which is journaled ext4 and already writable. On each boot attempt the initramfs mounts the data partition, increments the counter, and continues. If `digitsd` starts successfully, it resets the counter to zero. If the system hangs or crashes before the reset, the next boot attempt sees the elevated counter.

### Recovery Binary

The recovery binary starts a Wi-Fi access point and serves a small web UI:

- **SSID:** Digits-Recovery
- **Address:** `192.168.4.1`
- **Options presented:**
  - **Try Again** -- clears the boot counter, reboots normally. Useful after a transient failure.
  - **Factory Reset** -- restores rootfs from the factory image on the recovery partition, wipes `/data`, reboots into first-boot setup.

The recovery binary and factory images live on the recovery partition and are never modified by OTA updates.

## Partition Layout

| Partition | Label    | Filesystem | Size     | Mount             | Purpose                                                      |
|-----------|----------|------------|----------|-------------------|--------------------------------------------------------------|
| p1        | boot     | FAT32      | ~512 MB  | `/boot/firmware`  | Kernel, initramfs, `config.txt`                              |
| p2        | rootfs   | ext4       | ~3.5 GB  | `/` (read-only)   | Main OS                                                      |
| p3        | recovery | ext4       | ~1.5 GB  | Not mounted       | Factory images + recovery binary                             |
| p4        | data     | ext4       | ~10 GB   | `/data` (read-write) | All mutable state                                         |

The rootfs is mounted read-only at runtime. `digitsd` remounts it read-write only during asset extraction, then immediately remounts it read-only again.

The recovery partition is never mounted during normal operation. The initramfs mounts it directly when recovery mode is triggered.

## Hardware Watchdog

The BCM2835 hardware watchdog is enabled via `dtoverlay=watchdog` in `config.txt`. `digitsd` pets the watchdog every 5 seconds. If `digitsd` hangs or crashes without the watchdog being stroked, the hardware resets the board automatically.

This catches scenarios that the boot counter alone cannot: processes that start successfully but then lock up mid-run.

The watchdog feeds and the boot counter reset are independent mechanisms that together handle both startup failures and runtime hangs.

## For Developers

### Adding New Embedded Assets

1. Add the file to `pi/image/rootfs-overlay/` (for rootfs targets) or `pi/tones/` (for audio files).
2. Run `make embed` from `pi/digitsd/` to regenerate the embed directory.
3. Update the asset version string so the new files are extracted on the next device startup.

The `embed/` directory inside `pi/digitsd/` is a build artifact and is gitignored. It is generated from the source-of-truth locations (`pi/image/rootfs-overlay/`, `pi/tones/`) at build time.

### Recovery Partition Contents

The factory images and recovery binary are baked into the OS image at image build time (see `pi/image/`). They are never touched by OTA updates. To update the factory baseline, rebuild the OS image and reflash the device.

### Testing Recovery Mode

To test recovery mode without waiting for three actual boot failures:

```bash
# Set the boot counter to the threshold manually
echo "3" > /data/digits/boot-counter
sudo reboot
```

The initramfs will see the counter at threshold and enter recovery mode on the next boot.
