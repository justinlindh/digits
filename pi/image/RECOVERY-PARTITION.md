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

## ALSA Configuration

The recovery partition needs `/usr/share/alsa/` (the full ALSA config tree from the rootfs). Without it, `plughw:`, `sysdefault:`, and `alsactl` cannot resolve device names. Copy the entire `/usr/share/alsa/` directory during image build.

## Additional Binaries

Beyond the tool list above, the following are also required:

| Binary | Purpose |
|---|---|
| `/bin/amixer` | Set PCM volume after mixer state restore |
| `/bin/alsactl` | Restore codec mixer state from `/mixer.state` |

Copy from the rootfs. Both are dynamically linked against `libasound.so.2` (already present).

## Mixer State File

Copy the codec mixer state to `/mixer.state` on the recovery partition. Source: `pi/digitsd/internal/assets/embed/mixer/v2.state`. This file configures the TLV320AIC3104's internal routing (DAC to HP output, gain stages, power enables). Without it, the output path is muted.

## Build Script Checklist

When updating `tools/build-image.sh` (step 15b: populate recovery partition):

1. Cross-compile digitsd and copy to `/digits-recovery`
2. Symlink `/sbin/init` to `/digits-recovery`
3. Copy `libopus.so.0`, `libopusfile.so.0`, `libogg.so.0` to `/lib/`
4. Copy audio kernel modules to `/lib/modules/<kver>/kernel/` (decompress `.ko.xz` to `.ko`)
5. Copy and fix `modules.dep` (sed `s/.ko.xz/.ko/g`)
6. Copy recovery WAV files to `/tones/`
7. Copy `aplay`, `amixer`, `alsactl` to `/bin/`
8. Copy `/usr/share/alsa/` directory (ALSA config tree)
9. Copy `pi/digitsd/internal/assets/embed/mixer/v2.state` to `/mixer.state`

## What digitsd handles at runtime (PID 1 init)

When running as PID 1, digitsd's `recoveryInitSetup()` handles:

- Sets `PATH=/bin:/sbin:/usr/bin:/usr/sbin` and `LD_LIBRARY_PATH=/lib`
- Mounts `/proc`, `/sys`, `/tmp`, `/dev`, `/data`
- Enables GPCLK0 on GPIO4 for codec MCLK (12.288 MHz, via /dev/mem register writes with atomic stores for ARM64 ordering)
- Loads all kernel modules via insmod in dependency order (WiFi + I2C + audio)
- Triggers device tree uevent re-emission for codec binding
- Unblocks rfkill, waits for wlan0
- Starts hostapd (`Digits-Recovery` SSID) and dnsmasq (captive portal DNS)
- Re-toggles GPCLK0 after sound card registration
- Restores codec mixer state via `alsactl restore`
- Opens serial port at `/dev/ttyAMA0` (no udev, so `/dev/serial0` symlink does not exist)
- Plays audio via `aplay -D plughw:0,0` (blocking, per-clip subprocess)
- Starts recovery web UI on `:80` with captive portal detection handlers
- Runs voice menu event loop (off-hook: play menu, KEY:1 restart, KEY:2 factory reset)
- Zombie reaping (PID 1 responsibility)

## GPCLK0 (Codec Master Clock)

The TLV320AIC3104 needs a 12.288 MHz master clock on its MCLK pin, sourced from GPIO4 (GPCLK0). digitsd configures this via `/dev/mem` register writes to the BCM2835 clock manager.

Critical implementation detail: Go code accessing BCM2835 peripheral registers via mmap MUST use `sync/atomic.StoreUint32` and `atomic.LoadUint32`. Plain pointer dereference (`*(*uint32)(unsafe.Pointer(...)) = val`) compiles to unordered `STR` instructions on ARM64, which the CPU can reorder. The clock manager silently drops divider writes that arrive out of order (before the disable takes effect). This manifests as a zero divider and no audio output despite everything else appearing correct.

The GPCLK0 setup must run twice: once before module loading (so the codec sees MCLK when its driver probes), and once after the sound card registers (to ensure the clock is stable when playback begins).

This Go implementation replaces the Python `digits-enable-gpclk0` systemd service. The Python service can be removed from the image.

## Known Issues

- **WiFi intermittent failure:** `brcmfmac` sometimes takes longer than 15 seconds to initialize wlan0. If hostapd starts before wlan0 is ready, the AP fails. The init code waits up to 15 seconds but may need a longer timeout or retry.
- **Recovery flag cleanup:** Both "Try Again" paths (web UI and voice menu) must delete `/data/digits/recovery-mode` in addition to clearing the boot counter. Otherwise the device re-enters recovery on every boot.
