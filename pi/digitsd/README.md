# digitsd

The Pi-side daemon that runs on each Digits phone. Manages the hardware (audio codec, keypad, ringer, hook switch) via UART to the Pico, connects to the signaling server over WebSocket, and handles WebRTC calls.

## Architecture

```
                       signaling server (WebSocket)
                              |
                         signal.Client
                              |
    phone.Controller  <-->  main loop  <-->  webrtc.PeerManager
         |                                        |
    phone.SerialPort                         audio.Pipeline
    (UART to Pico)                           (Opus + ALSA)
```

### Internal packages

| Package | Purpose |
|---------|---------|
| `audio` | ALSA capture/playback, RNNoise denoising, mixer control |
| `bootcount` | Tracks consecutive boot failures for recovery triggering |
| `codec` | Opus encoder/decoder wrappers |
| `config` | JSON config file at `/data/digits/config.json` |
| `contacts` | Phone directory from linked households |
| `phone` | FSM controller, UART serial protocol, service codes |
| `signal` | WebSocket client to signaling server |
| `updater` | OTA binary updates from GitHub releases |
| `version` | Build version/commit injection via ldflags |
| `watchdog` | Hardware watchdog keepalive (`/dev/watchdog0`) |
| `webrtc` | Pion WebRTC peer connection lifecycle |
| `assets` | Embedded rootfs overlay and tone files (see below) |

## Build

Requires CGO for ALSA and Opus bindings. Cross-compiled to `linux/arm64` for the Pi Zero 2 W.

```bash
# Docker cross-compile (recommended, no host toolchain needed)
make build

# Local cross-compile (requires aarch64-linux-gnu-gcc, libopus-dev, libasound2-dev)
make build-local

# Run tests (host architecture, some audio tests skip without ALSA)
make test
```

The build embeds assets from `pi/image/rootfs-overlay/` and `pi/tones/` into the binary via `make embed`. The `build` target runs this automatically.

## Embedded assets

`make embed` copies rootfs overlay files (systemd services, hostapd config, SWD config, boot scripts) and tone WAVs into `internal/assets/embed/`. These are compiled into the binary with `//go:embed` so digitsd can deploy them to the filesystem for OTA updates and factory resets.

Some overlay files are excluded from embedding (boot fragments, sudoers) because they're only needed at image build time, not runtime.

## Runtime

Started by `digitsd.service` on the Pi. Key flags:

```
digitsd \
  -config /data/digits/config.json \
  -serial /dev/serial0 \
  -tones /data/digits/tones \
  -socket /data/digits/uart.sock
```

Requires supplementary groups: `dialout` (serial), `audio` (ALSA), `gpio`, `i2c`, `spi`.

## Debug utilities

The `cmd/` directory contains additional binaries for hardware debugging and latency profiling. These are not built by default and are not deployed to devices.

| Binary | Purpose |
|--------|---------|
| `alsatest` | 440 Hz ALSA playback test |
| `clocksync` | One-way clock offset measurement between hosts |
| `dmixlat` | ALSA output latency via `snd_pcm_delay()` |
| `latbench` | End-to-end Opus encode/decode/playback latency |
| `latclient` | WebRTC call latency measurement |
| `memprofile` | WebRTC peer connection memory profiling |
| `pipetest` | Opus pipeline throughput test |
