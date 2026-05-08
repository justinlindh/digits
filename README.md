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

Each phone is a gutted vintage desk phone with two processors inside: a **Pico H** running C firmware for real-time phone hardware (keypad scanning, hook switch, bell ringer, DTMF tones) and a **Raspberry Pi Zero 2 W** running a Go daemon for VoIP, end-to-end encrypted media (DTLS-SRTP), and signaling. A **Go server** handles call routing, device pairing, household management, and a web dashboard. There's a free public instance at [app.digits.family](https://app.digits.family), or you can self-host.

Lift the handset, hear a dial tone, punch in a number. The bell rings on the other end. Calls are encrypted, the server never touches media, and when the handset goes back on the cradle the mic is physically disconnected.

## Privacy

Calls use WebRTC with DTLS-SRTP encryption, the same standard used by Signal and FaceTime. Voice data is encrypted end-to-end between the two phones on the call. The server handles connection setup only: it brokers the signaling handshake (who's calling whom), but it never has access to the audio stream. We can't listen to your calls. We designed the system so that it's technically impossible for us to even if we tried.

When the handset is on the cradle, the hook switch physically disconnects the microphone circuit. Not muted in software. Electrically disconnected. You can trust physics.

**What the server does store:** user accounts (email, name), household membership, phone line assignments, device pairing tokens, and session data. Call metadata is logged: who called whom, when, and for how long. Connection quality telemetry (packet loss, jitter, bandwidth) is collected during calls for diagnostics.

**What the server never stores:** voice audio, call recordings, transcripts, location data, or anything about what was said on a call. There is no mechanism to record calls; the architecture makes it impossible. The server doesn't even know what was said.

The public server at [app.digits.family](https://app.digits.family) is free and runs the same code that's in this repo. If you'd rather not trust anyone else's infrastructure, the whole thing is designed to self-host. The signaling server is a single Go binary with a Postgres database. Run it on a VPS, a home server, whatever. Your network is yours either way.

## Hardware

The project has gone through three hardware iterations, each a different build strategy for the same phone:

| Rev | Construction | Audio | Ringer | Status |
|-----|-------------|-------|--------|--------|
| [V0](docs/build/components.md) | Perfboard, off-the-shelf modules | Codec Zero HAT (DA7212) | L298N + step-up transformer | Done, documented end-to-end |
| [V1](hardware/pcb/v1/) | Hand-assembled PCB | Codec Zero HAT (DA7212) | L298N + step-up transformer | Fabricated, has [errata](hardware/pcb/v1/ERRATA.md) |
| [V2](hardware/pcb/v2/) | Contract-assembled (JLCPCB) | Onboard TLV320AIC3104 | Onboard DRV8871 | Deployed, ~10 units in use |

**V1** is a bench-build target: single-sided, mostly through-hole, hand-solderable. External modules handle audio (Codec Zero HAT) and bell ringing (L298N H-bridge plus a step-up transformer).

**V2** is a fab-service target: onboard audio codec (TLV320AIC3104, 32-pin QFN) and ringer driver (DRV8871). Arrives pre-assembled from JLCPCB. Cleaner result, no internal wiring, but not practical to hand-solder.

Both use KiCad. Schematics, PCB layouts, Gerbers, and BOMs are in [`hardware/pcb/`](hardware/pcb/). The firmware abstracts board differences at runtime, so the same binary runs on V1 and V2.

## Architecture

| Component | Role |
|-----------|------|
| **Pico H** (firmware, C) | Real-time phone I/O: keypad matrix, hook switch, bell driver, DTMF/tone generation, status LED. Communicates with the Pi over UART. |
| **Pi Zero 2 W** (digitsd, Go) | VoIP daemon: WebRTC media (Opus codec, DTLS-SRTP), signaling client, call state machine, Wi-Fi setup, OTA updates. |
| **Audio codec** | Mic input and earpiece output. DA7212 HAT on V0/V1, onboard TLV320AIC3104 on V2. |
| **Signaling server** (Go) | WebSocket relay for SDP/ICE exchange, PostgreSQL persistence, device pairing, household management, web dashboard. |

The server never sees call audio. Each call is a direct peer-to-peer WebRTC session between two phones (or three, for party-line calls), with the server only brokering the initial signaling handshake.

See [Architecture deep-dive](docs/architecture/overview.md) for the full call path, data model, and NAT traversal.

## Web App

The server includes a web dashboard for managing your network: pairing phones, organizing households, viewing call history, and configuring per-line settings like voice style and Do Not Disturb schedules.

<p align="center">
  <img src="https://digits.family/images/screenshots/dashboard.png" width="720" alt="Digits web dashboard">
</p>

## Project Structure

```text
firmware/       Pico H firmware (C, CMake, Pico SDK)
pi/digitsd/     Pi-side VoIP daemon (Go, cross-compiled to arm64)
pi/image/       Raspberry Pi OS image builder
server/         Signaling server + web dashboard (Go, htmx, Tailwind)
hardware/pcb/   KiCad schematics, PCB layouts, Gerbers, BOMs
docs/           Architecture notes, build guides, self-hosting
scripts/        Build and flash helpers
```

## Quick Start

### Server

```bash
cd server
make build     # builds bin/signald
make run       # build + run (defaults to :8080)
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

The server is a single Go binary (`signald`) backed by Postgres. Docker Compose is the straightforward way to run it: clone the repo, fill in an `.env` file, `docker compose up -d`, done. The [self-hosting guide](docs/hosting/self-hosting.md) walks through the full setup including TLS, SMTP for magic-link auth, phone pairing, backups, and troubleshooting.

There is also a [Helm chart](charts/digits/) if you happen to have a Kubernetes cluster. There's no good reason for a three-phone family network to run on k8s, but I over-engineered almost everything in this project and it would have felt wrong to stop at the deployment layer. The chart supports CNPG Postgres, Redis Sentinel for multi-replica signaling, OpenTelemetry tracing, Pyroscope profiling, and Prometheus metrics. It's what runs in production. Again, completely unnecessary, but it works and it's there if you want it.

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
| `*#00000#` | Factory reset | Wipes config, Wi-Fi, contacts. Fresh AP + pairing mode. |

### Confirmation feedback

- **Volume change.** 1 beep, then dial tone resumes.
- **Audio test.** 2 beeps (start speaking), 1 beep (playback done), dial tone resumes.
- **Shutdown.** 3 beeps, then powers off.
- **Reboot.** 2 beeps, then reboots.

## Party Line (Three-Way Calling)

Classic 90s residential three-way calling. During an active call, press and briefly release the hook switch (a "flash", 100-600 ms on-hook) and you'll hear a second dial tone. Dial a third number, talk to them privately, then flash again to merge all three into a single call. Everyone hears everyone.

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

## Documentation

- [Why Digits?](https://digits.family/why)
- [How it works](https://digits.family/how-it-works)
- [Build one](https://digits.family/build)
- [Architecture](docs/architecture/overview.md)
- [Party Line](docs/architecture/party-line.md)
- [Self-hosting](docs/hosting/self-hosting.md)

## License

Code (firmware, server, tooling): [AGPL-3.0](LICENSE)

Hardware (schematics, PCB layout, BOM): [CC BY-SA 4.0](https://creativecommons.org/licenses/by-sa/4.0/)
