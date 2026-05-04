# Recovery Partition Build Requirements

The recovery partition (p3) is a self-contained rootfs that runs `digitsd --mode=recovery` as PID 1. This document lists everything the image builder must place on it.

## Binary

`digitsd` (cross-compiled arm64, dynamically linked via CGO for ALSA) is placed at `/digits-recovery`. `/sbin/init` is symlinked to it. When digitsd detects PID == 1, it enters recovery mode automatically.

## Shared Libraries

These are required by digitsd beyond what the existing recovery tools (hostapd, dnsmasq, etc.) already need:

| Library | Needed by |
|---|---|
| `libopus.so.0` | Opus codec (linked by digitsd) |
| `libopusfile.so.0` | Opus file reading |
| `libogg.so.0` | Ogg container (opus dependency) |

Libraries already present for other tools: `libasound.so.2`, `libc.so.6`, `libm.so.6`, `ld-linux-aarch64.so.1`.

All libraries go in `/lib/` (flat, not in an arch subdirectory).

## Audio Kernel Modules

These must be at `/lib/modules/<kernel-version>/kernel/` with the same directory structure as the rootfs. They MUST be decompressed (`.ko`, not `.ko.xz`).

```
kernel/sound/core/snd.ko
kernel/sound/core/snd-timer.ko
kernel/sound/core/snd-pcm.ko
kernel/sound/core/snd-pcm-dmaengine.ko
kernel/sound/core/snd-compress.ko
kernel/sound/soc/snd-soc-core.ko
kernel/sound/soc/bcm/snd-soc-bcm2835-i2s.ko
kernel/sound/soc/codecs/snd-soc-tlv320aic3x.ko
kernel/sound/soc/codecs/snd-soc-tlv320aic3x-i2c.ko
kernel/sound/soc/generic/snd-soc-simple-card.ko
kernel/sound/soc/generic/snd-soc-simple-card-utils.ko
kernel/drivers/base/regmap/regmap-i2c.ko
```

Copy `modules.dep`, `modules.dep.bin`, `modules.alias`, and `modules.alias.bin` from the rootfs and replace `.ko.xz` references with `.ko` in `modules.dep`.

### Why decompressed and why on the partition

The kernel probes device tree nodes during early boot and calls `request_module()` to load matching drivers. If the module file is compressed with xz and the kernel's built-in decompressor is not available, the load fails silently. Additionally, loading modules manually via `modprobe` after boot does NOT re-trigger device tree binding. The modules must be findable by the kernel's auto-loader at initial probe time.

## Recovery Tone WAV Files

Place in `/tones/` on the recovery partition:

| File | Content |
|---|---|
| `recovery_menu.wav` | "Recovery mode. Press 1 to restart. Press 2 for factory reset." |
| `restarting.wav` | "Restarting." |
| `confirm_factory_reset.wav` | "Press 2 again to confirm factory reset." |
| `factory_reset_cancelled.wav` | "Factory reset cancelled." |
| `factory_reset_in_progress.wav` | "Factory reset in progress. Do not unplug the phone." |
| `restoring_system.wav` | "Restoring system." |
| `formatting_data.wav` | "Formatting data." |
| `reset_complete.wav` | "Factory reset complete. Restarting." |

All files: 44100 Hz, mono, S16_LE WAV. Source files are in `pi/digits-recovery/recovery_audio/`.

## Existing Tools (unchanged)

These are already copied by `build-image.sh` and remain required:

```
/bin/hostapd, /bin/dnsmasq, /bin/ip, /bin/zstd, /bin/dd
/bin/mkfs.ext4, /bin/mount, /bin/umount, /bin/tar, /bin/aplay
/bin/busybox (with /sbin/modprobe symlink)
```

## Build Script Checklist

When updating `tools/build-image.sh` (step 15b: populate recovery partition):

1. Cross-compile digitsd and copy to `/digits-recovery`
2. Symlink `/sbin/init` to `/digits-recovery`
3. Copy `libopus.so.0`, `libopusfile.so.0`, `libogg.so.0` to `/lib/`
4. Copy audio kernel modules to `/lib/modules/<kver>/kernel/` (decompress `.ko.xz` to `.ko`)
5. Copy and fix `modules.dep` (sed `s/.ko.xz/.ko/g`)
6. Copy recovery WAV files to `/tones/`
7. Copy `aplay` to `/bin/` (already in the tool list)

## What digitsd handles at runtime (PID 1 init)

When running as PID 1, digitsd's `recoveryInitSetup()` handles:

- Sets `PATH=/bin:/sbin:/usr/bin:/usr/sbin` and `LD_LIBRARY_PATH=/lib`
- Mounts `/proc`, `/sys`, `/tmp`, `/dev`, `/data`
- Loads `brcmfmac` via `/sbin/modprobe` for WiFi
- Unblocks rfkill, waits for wlan0
- Starts hostapd (`Digits-Recovery` SSID) and dnsmasq (captive portal DNS)
- Mounts rootfs (p2) read-only temporarily to bind-mount `/lib/modules` for manual modprobe fallback
- Opens serial port at `/dev/ttyAMA0` (no udev, so `/dev/serial0` symlink does not exist)
- Initializes ALSA playback, loads tones from `/tones/`
- Starts recovery web UI on `:80` with captive portal detection handlers
- Runs voice menu event loop (off-hook: play menu, KEY:1 restart, KEY:2 factory reset)
- Zombie reaping (PID 1 responsibility)

## Known Issues

- **WiFi intermittent failure:** `brcmfmac` sometimes takes longer than 15 seconds to initialize wlan0. If hostapd starts before wlan0 is ready, the AP fails. The init code waits up to 15 seconds but may need a longer timeout or retry.
- **Recovery flag cleanup:** Both "Try Again" paths (web UI and voice menu) must delete `/data/digits/recovery-mode` in addition to clearing the boot counter. Otherwise the device re-enters recovery on every boot.
