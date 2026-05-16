<p align="center">
  <img src="https://digits.family/favicon.svg" width="64" alt="Digits">
</p>

<h1 align="center">Digits</h1>

<p align="center">
  Private encrypted phone network built from gutted vintage desk phones.
</p>

<p align="center">
  <a href="https://github.com/justinlindh/digits/actions/workflows/server-ci.yml"><img src="https://github.com/justinlindh/digits/actions/workflows/server-ci.yml/badge.svg" alt="Server CI"></a>

  <a href="https://github.com/justinlindh/digits/releases?q=server%2Fv"><img src="https://img.shields.io/github/v/release/justinlindh/digits?filter=server/v*&label=server" alt="Server release"></a>
  <a href="https://github.com/justinlindh/digits/releases?q=pi%2Fv"><img src="https://img.shields.io/github/v/release/justinlindh/digits?filter=pi/v*&label=pi" alt="Pi release"></a>
  <a href="https://github.com/justinlindh/digits/releases?q=fw%2Fv"><img src="https://img.shields.io/github/v/release/justinlindh/digits?filter=fw/v*&label=firmware" alt="Firmware release"></a>
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white" alt="Go 1.26">
  <img src="https://img.shields.io/badge/C-Pico_SDK-A8B9CC?logo=c&logoColor=white" alt="C / Pico SDK">
  <a href="LICENSE"><img src="https://img.shields.io/badge/code-AGPL--3.0-blue" alt="AGPL-3.0 License"></a>
  <a href="hardware/pcb/"><img src="https://img.shields.io/badge/hardware-CC_BY--SA_4.0-green" alt="CC BY-SA 4.0 License"></a>
</p>

<p align="center">
  <a href="https://digits.family">Website</a> &middot;
  <a href="https://app.digits.family">App</a> &middot;
  <a href="docs/">Docs</a>
</p>

---

Each phone is a gutted vintage desk phone with two processors inside: an RP2040 handling real-time phone hardware (keypad scanning, hook switch, bell ringer, DTMF tones) and a Raspberry Pi Zero 2 W running a Go daemon for VoIP, end-to-end encrypted media (DTLS-SRTP), and call signaling. A Go server handles call routing, device pairing, and household management. There's a free public instance at [app.digits.family](https://app.digits.family), or you can run your own.

## Table of Contents

- [Architecture](#architecture)
- [Hardware](#hardware)
- [Project Structure](#project-structure)
- [Quick Start](#quick-start)
- [Hosting](#hosting)
- [Web App](#web-app)
- [Service Codes](#service-codes)
  - [Confirmation feedback](#confirmation-feedback)
- [Party Line](#party-line-three-way-calling)
- [Easter Eggs](#easter-eggs)
- [Privacy](#privacy)
- [Contributing](#contributing)
- [Documentation](#documentation)
- [License](#license)

## Architecture

| Component | Role |
|-----------|------|
| **RP2040** (firmware, C) | Real-time phone I/O: keypad matrix, hook switch, bell driver, DTMF/tone generation, status LED. Communicates with the Pi over UART. V0/V1 use a Pico H module; V2 has the RP2040 onboard. |
| **Pi Zero 2 W** (digitsd, Go) | VoIP daemon: WebRTC media, Opus codec, DTLS-SRTP, signaling client, call state machine, Wi-Fi setup, OTA updates. |
| **Audio codec** | Mic input and earpiece output. Codec Zero HAT (DA7212) on V0/V1, onboard TLV320AIC3104 on V2. |
| **Signaling server** (Go) | WebSocket relay for SDP/ICE exchange, PostgreSQL persistence, device pairing, household management, web dashboard. |

Calls are peer-to-peer WebRTC sessions between two phones (or three, for party-line calls). The server brokers the signaling handshake but never handles media.

See [Architecture deep-dive](docs/architecture/overview.md) for the full call path, data model, and NAT traversal.

## Hardware

Three hardware iterations, each a different build strategy:

| Rev | Construction | Audio | Ringer | Status |
|-----|-------------|-------|--------|--------|
| [V0](docs/build/components.md) | Perfboard, off-the-shelf modules | Codec Zero HAT (DA7212) | L298N + step-up transformer | Done, documented end-to-end |
| [V1](hardware/pcb/v1/) | Hand-assembled PCB, Pico H module | Codec Zero HAT (DA7212) | L298N + step-up transformer | Fabricated, has [errata](hardware/pcb/v1/ERRATA.md) |
| [V2](hardware/pcb/v2/) | Contract-assembled (JLCPCB), onboard RP2040 | Onboard TLV320AIC3104 | Onboard DRV8871 | Deployed, ~10 units in use |

**V1** is a bench-build target: single-sided, mostly through-hole, hand-solderable. External modules handle audio (Codec Zero HAT) and bell ringing (L298N H-bridge plus a step-up transformer).

**V2** is a fab-service target: onboard RP2040, audio codec (TLV320AIC3104, 32-pin QFN), and ringer driver (DRV8871). Arrives pre-assembled from JLCPCB. Not practical to hand-solder.

Schematics, PCB layouts, Gerbers, and BOMs are in [`hardware/pcb/`](hardware/pcb/). All designed in KiCad. The firmware abstracts board differences at runtime, so the same binary runs on V1 and V2.

## Project Structure

```text
firmware/       RP2040 firmware (C, CMake, Pico SDK)
pi/digitsd/     Pi-side VoIP daemon (Go, cross-compiled to arm64)
pi/image/       Raspberry Pi OS image builder
server/         Signaling server + web dashboard (Go, htmx, Tailwind)
hardware/pcb/   KiCad schematics, PCB layouts, Gerbers, BOMs
charts/digits/  Helm chart for Kubernetes deployment
tools/          Build scripts for firmware, Pi binaries, and OS images
docs/           Architecture notes, build guides, self-hosting
scripts/        Firmware build and flash helpers
```

## Quick Start

### Server

```bash
cd server
make build     # builds bin/signald
make run       # build + run (defaults to :8443)
make test      # go test ./...
```

Requires Go 1.26+ and PostgreSQL. The server reads `DATABASE_URL` and runs migrations on startup.

### Firmware

```bash
./scripts/build.sh     # auto-detects PICO_SDK_PATH
./scripts/flash.sh     # hold BOOTSEL, plug in USB, then run
```

### Pi Daemon

```bash
cd pi/digitsd
make build             # cross-compiles to linux/arm64 via Docker
make build-local       # native build (requires cross-compile libs)
make test
```

## Hosting

The server is a single Go binary (`signald`) backed by Postgres. For calls across different networks, you also need a TURN relay ([coturn](https://github.com/coturn/coturn)). Docker Compose is the straightforward way to run both: clone the repo, fill in an `.env` file, `docker compose up -d`. The [self-hosting guide](docs/hosting/self-hosting.md) covers the full setup including TURN, TLS, SMTP for magic-link auth, phone pairing, backups, and troubleshooting.

There is also a [Helm chart](charts/digits/) if you happen to have a Kubernetes cluster. There is no good reason for a family phone network to run on k8s, but I over-engineered almost everything in this project and it would have felt wrong to stop at the deployment layer. The chart supports CNPG Postgres, Redis Sentinel for multi-replica signaling, OpenTelemetry tracing, Pyroscope profiling, and Prometheus metrics. It is what runs in production. Completely unnecessary, but it works and it is there if you want it.

## Web App

The server includes a web dashboard for pairing phones, organizing households, viewing call history, and configuring per-line settings.

<p align="center">
  <img src="https://digits.family/images/screenshots/dashboard.png" width="720" alt="Digits web dashboard">
</p>

## Service Codes

Hidden codes entered on the keypad while the handset is off-hook.

| Code | Action | Details |
|------|--------|---------|
| `*#*0` -- `*#*9` | Volume | Sets earpiece volume (0 = quiet, 9 = max). Persists across reboots. |
| `*#8378#` | Audio test | Records 5 s from mic, plays it back through earpiece. (`*#TEST#` on keypad.) |
| `*#*#` | Shutdown | Graceful power-off. Safe to unplug after LED goes dark. |
| `*##*` | Reboot | Immediate reboot. |
| `*#0*` | Force re-pair | Clears device token, reboots into pairing mode. |
| `*#73887#` | Wi-Fi setup | Reboots into AP mode for Wi-Fi reconfiguration. (`*#SETUP#` on keypad.) |
| `*#873283#` | Update check | Checks for OTA firmware and daemon updates. (`*#UPDATE#` on keypad.) |
| `*#00000#` | Factory reset | Wipes config, Wi-Fi, contacts. Fresh AP + pairing mode. |

### Call Return (*69 / *89)

| Code | Action | Details |
|------|--------|---------|
| `*69` | Call return | Announces who last called you. Press `1` to call them back. If the line is busy, the system retries for 30 minutes and rings you with a distinctive pattern when the line is free. |
| `*89` | Cancel call return | Cancels any pending *69 busy-retry. Voice confirmation plays. |

These are dialed from dial tone (not prefixed with `*#`), matching the original 1990s POTS behavior.

### Voicemail (*96 / *97 / *98 / *99)

| Code | Action | Details |
|------|--------|---------|
| `*96` | Audition greeting | Plays your current outgoing greeting through the earpiece, so you can hear what callers hear. |
| `*97` | Record greeting | Records a custom outgoing greeting after the tone. |
| `*98` | Listen to messages | Plays your messages oldest first, then any saved (already heard) messages. During playback: `7` deletes, `9` saves, `#` skips, `*` replays. The retrieval code is configurable; `*98` is the default. |
| `*99` | Delete greeting | Removes your custom greeting and restores the default. |

When a call rings unanswered past the configured timeout, the phone answers it, plays your greeting, and records the caller's message. The handset microphone is muted while recording, so a caller never hears your room. See the [voicemail guide](docs/voicemail-guide.md) for using and configuring it, or [Voicemail](docs/voicemail.md) for the engineering reference: FSM, on-disk storage format, signaling, and configuration.

### Confirmation feedback

- **Volume change.** 1 beep, then dial tone resumes.
- **Audio test.** 2 beeps (start speaking), 1 beep (playback done), dial tone resumes.
- **Shutdown.** 3 beeps, then powers off.
- **Reboot.** 2 beeps, then reboots.

## Party Line (Three-Way Calling)

Classic 90s residential three-way calling. During an active call, press and briefly release the hook switch (a "flash", 100-600 ms on-hook) to get a second dial tone. Dial a third number, talk to them privately, then flash again to merge all three into a single call.

| Gesture | What happens |
|---------|--------------|
| Flash during an active call | Held party drops to silent hold; you hear a second dial tone |
| Dial a number | Ringback; third party's phone rings |
| Flash again after they answer | All three merged into the conference |
| Flash before they answer | Drops the add attempt; returns to the held party |
| Hang up (host) | Collapses the conference for everyone |
| Hang up (non-host) | Remaining pair keeps talking as a normal 2-party call |

Hard-capped at three parties, matching residential TWC on 5ESS / DMS-100 switches. Audio stays end-to-end encrypted on three independent DTLS-SRTP legs; the server never sees media, and mixing happens locally on each phone.

See [Party Line](docs/architecture/party-line.md) for the state machine, media topology, signalling protocol, and failure-mode coverage.

## Easter Eggs

Hidden sequences that play audio clips through the earpiece. Each keypress must be within ~1.5 s of the last.

| Sequence | What plays |
|----------|------------|
| `5-5-4-2` | Towelie: *"That's it! That's the melody to Funky Town!"* |
| `0-0-0-0` | Rick Astley: *"Never gonna give you up..."* |
| `8-6-7-5-3-0-9` | Tommy Tutone: *"Jenny I got your number... 867-5309!"* (intercepts the dial) |

## Privacy

Calls use WebRTC with DTLS-SRTP. Voice data is encrypted end-to-end between the two devices on the call. The signaling server facilitates connection setup but never has access to the audio stream. Even if the server were compromised, there would be no voice data to extract.

A dedicated hardware kill switch (separate from the hook switch) physically disconnects the microphone circuit when the handset is on the cradle. This is an electrical disconnect, not a software mute.

**What the server stores:** user accounts (email, name), household membership, phone line assignments, device pairing tokens, session data, call metadata (caller, callee, timestamps, duration), and connection quality telemetry (packet loss, jitter, bandwidth).

**What the server does not store:** voice audio, call recordings, transcripts, or location data. There is no mechanism in the server to capture call audio.

The server is designed to be self-hosted. If you do not want to use the public instance at [app.digits.family](https://app.digits.family), you can run the same code on your own infrastructure. See [Self-hosting](docs/hosting/self-hosting.md).

## Contributing

This is a personal project, not a company. Contributions to both the software and the hardware are welcome.

On the software side, the server is vanilla Go with htmx and Tailwind, the Pi daemon is Go with WebRTC, and the firmware is C on the Pico SDK. Standard open source workflow: fork, branch, PR.

On the hardware side, I am not an electrical engineer. I learned KiCad, read datasheets, and iterated until the board worked. The result is a functional PCB with minimal errata, which I'm genuinely proud of, but there is a lot of room for someone with real PCB design experience to improve the layout, power integrity, signal routing, and DFM. If you have those skills and this project interests you, I would particularly welcome that kind of help. The schematics and board files are all KiCad and licensed CC BY-SA 4.0.

## Documentation

- [Why Digits?](https://digits.family/why)
- [How it works](https://digits.family/how-it-works)
- [Build one](https://digits.family/build)
- [Architecture](docs/architecture/overview.md)
- [Party Line](docs/architecture/party-line.md)
- [Voicemail](docs/voicemail.md)
- [User guide](docs/user-guide.md)
- [Voicemail guide](docs/voicemail-guide.md)
- [Self-hosting](docs/hosting/self-hosting.md)
- [Load testing](docs/hosting/load-testing.md)

## License

Code (firmware, server, tooling): [AGPL-3.0](LICENSE)

Hardware (schematics, PCB layout, BOM): [CC BY-SA 4.0](https://creativecommons.org/licenses/by-sa/4.0/)
