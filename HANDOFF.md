# Onboarding Feedback: Session Handoff

**Branch:** `feat/onboarding-feedback`
**PR:** https://github.com/justinlindh/digits/pull/432 (draft)
**Worktree:** `/home/justin/src/digits/.worktrees/onboarding-feedback`
**Test device:** 192.168.2.28 (V2, paired as 2457890)

## What was built

### Firmware (`firmware/src/`)
- New LED modes: `LED_MODE_FAST_BLINK`, `LED_MODE_DOUBLE_PULSE`, `LED_MODE_HEARTBEAT`
- Persisted device phase byte in flash (offset 0x1FF001): SETUP(0x00/0xFF), UNPAIRED(0x01), PAIRED(0x02)
- `STATE:SET:SETUP/UNPAIRED/PAIRED` UART commands to write phase
- `LED:LOCK/UNLOCK` to prevent FSM from overriding LED
- `s_pi_connected` flag: FSM doesn't touch LED until first Pi command arrives
- Boot LED pattern from phase byte (visible immediately on power-on)

### phonekit (`pi/phonekit/`)
- New pure-Go module: serial port (termios), audio (aplay subprocess), Phone type
- 16 unit tests passing
- Not used in recovery (digitsd handles its own serial/audio there)

### digitsd normal mode changes (`pi/digitsd/cmd/digitsd/main.go`)
- `STATE:SET:*` at phase transitions (paired, factory reset, re-pair, Wi-Fi setup)
- Codec marker file write (`/data/digits/codec-device`)
- Go GPCLK0 enable (replaces Python systemd service)
- `--mode=recovery` flag and PID-1 auto-detection

### digitsd recovery mode (`pi/digitsd/cmd/digitsd/recovery.go`)
- Runs as PID 1 on recovery partition
- Full init: mount, GPCLK0, insmod (WiFi + I2C + audio), uevent triggers, AP, alsactl restore
- Voice menu: off-hook plays recovery_menu.wav, KEY:1=restart, KEY:2=factory reset (double confirm)
- Web UI on :80 with captive portal redirects and /debug endpoint
- Factory reset: zstd decompress rootfs, mkfs.ext4 data, extract skeleton
- GPCLK0 uses `sync/atomic.StoreUint32` for ARM64 memory ordering (the key bug fix)

### digits-setup changes (`pi/digits-setup/`)
- Wi-Fi verification flow (save to backup, test NM connectivity, commit on success)
- /api/status endpoint for failure banner
- phonekit integration for LED during verification
- Frontend: spinner, error banner, status polling
- NOT YET TESTED ON DEVICE

### Image build
- `tools/build-image.sh`: aplay added to recovery tool list
- `pi/image/RECOVERY-PARTITION.md`: full build requirements documented
- `pi/image/rootfs-overlay/usr/local/bin/flash-pico.sh`: preserves phase byte on rev marker write

### Voice clips
- Recovery: 8 WAVs in `pi/digits-recovery/recovery_audio/` (embedded in old binary, on partition at `/tones/`)
- Setup: 2 WAVs in `pi/digits-setup/internal/portal/audio/`

## What's tested and working on hardware

| Feature | Status |
|---|---|
| Firmware LED patterns (all 3) | Tested, confirmed visually |
| Phase persistence across power cycle | Tested |
| LED:LOCK prevents FSM override | Tested |
| Recovery: AP + web UI + captive portal | Tested |
| Recovery: serial (HOOK/KEY events) | Tested |
| Recovery: audio playback (aplay plughw:0,0) | Tested |
| Recovery: voice menu with keypad | Partially (audio plays, need to verify KEY:1 restart) |
| Recovery: factory reset via keypad | Accidentally tested, worked |
| Go GPCLK0 in normal mode (ExecStartPre, replaces Python) | Tested, dial tone works on cold boot |
| Go GPCLK0 in recovery mode (PID 1, root) | Tested, audio works |

## What still needs testing

| Task | Notes |
|---|---|
| Recovery: KEY:1 restart via voice menu | Audio now works; need to verify keypad still functions |
| Recovery: captive portal auto-popup on Android | Redirect handlers added, not tested |
| Wi-Fi verification flow (digits-setup) | Code complete, never deployed to device |
| digitsd STATE:SET:PAIRED on real pairing | Was tested partially; needs clean re-test |
| digitsd STATE:SET:SETUP on *#SETUP# | Not tested |
| Full end-to-end onboarding (factory reset -> pair) | Not tested |
| phonekit on real device | Only unit tests so far |
| Triple power-cycle recovery trigger | Timing documented (wait for LED flash ~8s), not reliable enough for users |

## Key technical gotchas discovered

1. **ARM64 memory ordering:** Go `unsafe.Pointer` dereference to mmap'd peripheral registers produces unordered STR instructions. BCM2835 clock manager silently drops divider writes that arrive out of order. Fix: `atomic.StoreUint32`/`atomic.LoadUint32`.

2. **GPCLK0 must be called twice:** Once before insmod (so codec sees MCLK during driver probe) and once after sound card registers (to ensure stable clock when playback starts).

3. **BusyBox modprobe can't resolve deps:** Use explicit insmod in dependency order from `/lib/modules/<kver>/`.

4. **Modules must be decompressed:** `.ko` not `.ko.xz` on the recovery partition.

5. **ALSA needs full config tree:** `/usr/share/alsa/` must be on recovery partition for `plughw`, `alsactl`, and `amixer` to work.

6. **No udev in recovery:** Device tree binding after insmod requires uevent re-emission (`echo add > /sys/bus/.../uevent`).

7. **flash-pico.sh erases phase byte:** The rev marker write erases the entire 4KB sector. Fixed to read-back and preserve the phase byte.

8. **Recovery flag cleanup:** Both "Try Again" paths must delete `/data/digits/recovery-mode` or the device loops in recovery.

9. **Python GPCLK0 service can be removed:** The Go implementation in digitsd replaces it entirely.

## Current device state

- Normal digitsd binary has Go GPCLK0 via ExecStartPre (Python service disabled, Go replaces it)
- Recovery partition has working recovery binary with audio
- Device is in normal mode, audio works (dial tone confirmed)
- Python GPCLK0 service is disabled on this device; Go handles it

## Files of interest

- `pi/digitsd/cmd/digitsd/recovery.go` - The recovery mode implementation
- `pi/digitsd/cmd/digitsd/recovery_static/index.html` - Recovery web UI
- `pi/image/RECOVERY-PARTITION.md` - Build requirements for recovery partition
- `docs/superpowers/specs/2026-05-03-onboarding-feedback-design.md` - Original spec
- `docs/superpowers/plans/2026-05-03-onboarding-feedback.md` - Implementation plan

## GPCLK0 (resolved)

The Go GPCLK0 implementation fully replaces the Python script. Two issues were solved:

1. **ARM64 memory ordering:** `atomic.StoreUint32`/`atomic.LoadUint32` for peripheral register writes.
2. **Permission:** digitsd runs as user `digits` (can't open `/dev/mem`). Fixed via `ExecStartPre=+/usr/local/bin/digitsd --mode=gpclk0` which runs as root before the main process drops privileges. `--mode=gpclk0` just calls `enableGPCLK0()` and exits.

The Python `digits-enable-gpclk0` service and script can be removed from the image entirely.
