<h1 align="center">Digits</h1>

<p align="center">
  Private encrypted phone network built from gutted vintage desk phones.
</p>

<p align="center">
  <a href="https://github.com/justinlindh/digits/actions/workflows/server-ci.yml"><img src="https://github.com/justinlindh/digits/actions/workflows/server-ci.yml/badge.svg" alt="Server CI"></a>
  <a href="https://github.com/justinlindh/digits/actions/workflows/commitlint.yml"><img src="https://github.com/justinlindh/digits/actions/workflows/commitlint.yml/badge.svg" alt="Commitlint"></a>
  <a href="https://github.com/justinlindh/digits/releases?q=server%2Fv"><img src="https://img.shields.io/github/v/release/justinlindh/digits?filter=server/v*&label=server" alt="Server release"></a>
  <a href="https://github.com/justinlindh/digits/releases?q=pi%2Fv"><img src="https://img.shields.io/github/v/release/justinlindh/digits?filter=pi/v*&label=pi" alt="Pi release"></a>
  <a href="https://github.com/justinlindh/digits/releases?q=fw%2Fv"><img src="https://img.shields.io/github/v/release/justinlindh/digits?filter=fw/v*&label=firmware" alt="Firmware release"></a>
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white" alt="Go 1.26">
  <img src="https://img.shields.io/badge/C-Pico_SDK-A8B9CC?logo=c&logoColor=white" alt="C / Pico SDK">
  <a href="LICENSE"><img src="https://img.shields.io/github/license/justinlindh/digits" alt="MIT License"></a>
</p>

<p align="center">
  <a href="https://digits.family">Website</a> &middot;
  <a href="https://app.digits.family">App</a> &middot;
  <a href="docs/">Docs</a>
</p>

---

Each Digits endpoint combines:
- **RP2040 Pico** -- phone UX and real-time hardware control (hook switch, keypad, bell, tones, indicators)
- **Raspberry Pi Zero 2 W** -- Linux-side services (VoIP stack, crypto/session logic, orchestration)
- **Raspberry Pi Codec Zero (DA7212)** -- audio input/output on the Pi side

The goal is a self-hosted, private, retro-style calling system with modern encrypted transport under the hood.

## Architecture

A single phone unit is split into two cooperating processors:

| Component | Role |
|-----------|------|
| **RP2040 Pico** (firmware) | Low-level telephony, timing-sensitive I/O, UART protocol to Pi |
| **Pi Zero 2 W** (digitsd) | Go daemon for VoIP, signaling, WebRTC media, Opus codec, call control |
| **Codec Zero** (DA7212) | Audio pHAT with mic input (3.5mm TRS), speaker output (screw terminal) |
| **Signaling Server** (Go) | WebSocket relay for SDP/ICE, PostgreSQL persistence, web app + admin |

See [Architecture deep-dive](docs/architecture/overview.md) for the full call path, data model, and NAT traversal.

## Project Structure

```text
firmware/   RP2040 Pico firmware (C/CMake + Pico SDK)
pi/         Pi Zero userland -- digitsd VoIP daemon, setup tools, image builder
server/     Go signaling server (WebSocket relay, web app, admin panel)
docs/       Hardware build guides, architecture, self-hosting
scripts/    Build and flash helpers
```

## Quick Start

### Server

```bash
cd server
make build     # builds bin/signald
make run       # build + run (defaults to :8080)
make test      # go test ./...
```

Requires Go 1.26+ and PostgreSQL. The server reads `DATABASE_URL` and runs migrations automatically on startup.

### Firmware (RP2040 Pico)

```bash
export PICO_SDK_PATH=/path/to/pico-sdk
./scripts/build.sh
./scripts/flash.sh   # hold BOOTSEL, plug in USB, then run
```

### Pi Daemon (digitsd)

```bash
cd pi/digitsd
make build          # cross-compiles to linux/arm64
make build-local    # native build
make test
```

Requires cross-compile toolchain: `gcc-aarch64-linux-gnu`, `libasound2-dev:arm64`, `libopus-dev:arm64`, `libopusfile-dev:arm64`.

## Service Codes

Hidden codes entered on the keypad during an active call.

| Code | Action | Details |
|------|--------|---------|
| `*#*0` -- `*#*9` | Volume | Sets earpiece volume (0 = quiet, 9 = max). Persists across reboots. |
| `*#8378#` | Audio test | Records 5 s from mic, plays it back through earpiece. (`*#TEST#` on keypad.) |
| `*#*#` | Shutdown | Graceful power-off. Safe to unplug after LED goes dark. |
| `*##*` | Reboot | Immediate reboot. |
| `*#0*` | Force re-pair | Clears device token, reboots into pairing mode. |
| `*#73887#` | Wi-Fi setup | Reboots into AP mode for Wi-Fi reconfiguration. (`*#SETUP#` on keypad.) |
| `*#00000#` | Factory reset | Wipes config, Wi-Fi, contacts. Fresh AP + pairing mode. |

### Confirmation feedback

- **Volume change:** 1 beep, then dial tone resumes.
- **Audio test:** 2 beeps (start speaking), 1 beep (playback done), dial tone resumes.
- **Shutdown:** 3 beeps, then powers off.
- **Reboot:** 2 beeps, then reboots.

## Easter Eggs

Hidden sequences that play audio clips through the earpiece. Each keypress must be within ~1.5 s of the last.

| Sequence | What plays |
|----------|------------|
| `5-5-4-2` | Towelie: *"That's it! That's the melody to Funky Town!"* |
| `0-0-0-0` | Rick Astley: *"Never gonna give you up..."* |
| `8-6-7-5-3-0-9` | Tommy Tutone: *"Jenny I got your number... 867-5309!"* (intercepts the dial) |

## Hardware

A custom carrier PCB is in pre-production that replaces the hand-wired protoboard with a single drop-in board. It integrates power regulation (12V to 5V), the Pico socket, Pi+Codec Zero header, and all off-board connectors (keypad, hook switch, bell ringer, handset audio, LED). Designed in KiCad, sized to fit the original Sangyn phone mounting posts.

Status: boards ordered, components sourced, pending assembly and validation.

## Documentation

- [Why Digits?](https://digits.family/why) -- the problem and vision
- [How it works](https://digits.family/how-it-works) -- overview for parents and curious people
- [Architecture](docs/architecture/overview.md) -- technical deep-dive
- [Build one](docs/build/components.md) -- BOM and hardware guide
- [Wiring](docs/build/wiring.md) -- electrical spec and GPIO map
- [Self-hosting](docs/hosting/self-hosting.md) -- run your own signaling server

## License

[MIT](LICENSE)
