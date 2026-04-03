# Docs Cleanup for Public Release — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure and clean up `docs/` for public GitHub release, organized around maker/hacker audience needs.

**Architecture:** Delete obsolete planning docs and internal dev notes. Consolidate three architecture docs into one. Reorganize remaining docs into `build/`, `architecture/`, and `hosting/` subdirectories. Add navigation README. Update main repo README with docs links.

**Tech Stack:** Markdown, git

**Terminology reference:** The current server data model uses `Household`, `Device`, `Line`, and `HouseholdLink`. Old terms to replace: "phone" (when meaning a line/number) -> "line", "directory" -> removed, "contact" (as a model) -> removed. "Phone" is still fine when referring to the physical device.

---

### Task 1: Delete obsolete files

**Files:**
- Delete: `docs/pi-os-audit.md`
- Delete: `docs/easter-eggs-backlog.md`
- Delete: `docs/electrocookie-layout.txt`
- Delete: `docs/debugging/2026-03-23-webrtc-audio-debugging.md`
- Delete: `docs/debugging/` (empty directory after file removal)
- Delete: `docs/architecture/voip-call-path.md`
- Delete: `docs/architecture/networking-nat-traversal.md`
- Delete: `docs/diagrams/cross-household-linking.md`
- Delete: `docs/diagrams/digits-electrocookie-layout.png`
- Delete: `docs/diagrams/electrocookie-board-layout.png`
- Delete: `docs/diagrams/phone-fsm.puml`
- Delete: `docs/diagrams/phone-fsm-graphviz.png`
- Delete: `docs/diagrams/phone-fsm.svg`
- Delete: `docs/diagrams/img/04-call-permission.png`
- Delete: `docs/diagrams/img/04-call-permission.svg`
- Delete: `docs/diagrams/img/05-revocation-flow.png`
- Delete: `docs/diagrams/img/05-revocation-flow.svg`

- [ ] **Step 1: Delete all obsolete files**

```bash
cd /home/justin/src/digits

# Obsolete docs
git rm docs/pi-os-audit.md
git rm docs/easter-eggs-backlog.md
git rm docs/electrocookie-layout.txt
git rm docs/debugging/2026-03-23-webrtc-audio-debugging.md

# Architecture docs being consolidated
git rm docs/architecture/voip-call-path.md
git rm docs/architecture/networking-nat-traversal.md
git rm docs/diagrams/cross-household-linking.md

# Redundant/obsolete diagrams
git rm docs/diagrams/digits-electrocookie-layout.png
git rm docs/diagrams/electrocookie-board-layout.png
git rm docs/diagrams/phone-fsm.puml
git rm docs/diagrams/phone-fsm-graphviz.png
git rm docs/diagrams/phone-fsm.svg
git rm docs/diagrams/img/04-call-permission.png
git rm docs/diagrams/img/04-call-permission.svg
git rm docs/diagrams/img/05-revocation-flow.png
git rm docs/diagrams/img/05-revocation-flow.svg
```

- [ ] **Step 2: Remove empty debugging directory**

```bash
rmdir docs/debugging
```

- [ ] **Step 3: Commit**

```bash
git commit -m "docs: delete obsolete planning docs, debug logs, and redundant diagrams"
```

---

### Task 2: Restructure directory layout

Move existing docs into the new `build/`, `hosting/`, and `build/teardown/` subdirectories.

**Files:**
- Move: `docs/components.md` -> `docs/build/components.md`
- Move: `docs/wiring.md` -> `docs/build/wiring.md`
- Move: `docs/datasheets.md` -> `docs/build/datasheets.md`
- Move: `docs/hardware-kill-switch.md` -> `docs/build/hardware-kill-switch.md`
- Move: `docs/teardown/notes.md` -> `docs/build/teardown/notes.md`
- Move: `docs/photos/` -> `docs/build/teardown/photos/`
- Move: `docs/uart-protocol.md` -> `docs/architecture/uart-protocol.md`
- Move: `docs/self-hosting.md` -> `docs/hosting/self-hosting.md`

- [ ] **Step 1: Create new directories**

```bash
mkdir -p docs/build/teardown
mkdir -p docs/hosting
```

- [ ] **Step 2: Move build docs**

```bash
git mv docs/components.md docs/build/components.md
git mv docs/wiring.md docs/build/wiring.md
git mv docs/datasheets.md docs/build/datasheets.md
git mv docs/hardware-kill-switch.md docs/build/hardware-kill-switch.md
```

- [ ] **Step 3: Move teardown and photos**

```bash
git mv docs/teardown/notes.md docs/build/teardown/notes.md
rmdir docs/teardown
git mv docs/photos docs/build/teardown/photos
```

- [ ] **Step 4: Move architecture and hosting docs**

```bash
git mv docs/uart-protocol.md docs/architecture/uart-protocol.md
git mv docs/self-hosting.md docs/hosting/self-hosting.md
```

- [ ] **Step 5: Commit**

```bash
git commit -m "docs: restructure into build/, architecture/, hosting/ subdirectories"
```

---

### Task 3: Create docs/README.md navigation hub

**Files:**
- Create: `docs/README.md`

- [ ] **Step 1: Write docs/README.md**

```markdown
# Digits Documentation

## Build One

Everything you need to build a Digits phone from scratch.

- [Components](build/components.md) — bill of materials and procurement
- [Wiring](build/wiring.md) — full electrical spec, GPIO map, connectors
- [Datasheets](build/datasheets.md) — component reference sheets
- [Hardware kill switch](build/hardware-kill-switch.md) — mic privacy circuit
- [Teardown notes](build/teardown/notes.md) — Sangyn 2500 disassembly reference

## How It Works

- [Architecture overview](architecture/overview.md) — system design, call path, data model, NAT traversal
- [UART protocol](architecture/uart-protocol.md) — Pico/Pi communication spec
- [State machine](diagrams/phone-fsm.dot) — firmware FSM ([rendered](diagrams/phone-fsm.png))

## Run Your Own Server

- [Self-hosting guide](hosting/self-hosting.md) — Docker Compose deployment, TLS, backup, troubleshooting

## Why Digits?

- [digits.family/why](https://digits.family/why) — the problem and vision
- [digits.family/how-it-works](https://digits.family/how-it-works) — how the system works
- [Mission](mission.md) — short project mission statement
- [Why Digits](why-digits.md) — why voice calls matter for kids
```

- [ ] **Step 2: Commit**

```bash
git add docs/README.md
git commit -m "docs: add navigation README"
```

---

### Task 4: Slim down mission.md and why-digits.md

These docs duplicate content on digits.family. Slim them to brief versions that link to the website.

**Files:**
- Modify: `docs/mission.md`
- Modify: `docs/why-digits.md`

- [ ] **Step 1: Rewrite mission.md**

Replace the full contents of `docs/mission.md` with:

```markdown
# Digits — Mission

Kids deserve a way to talk to their friends without surveillance, algorithms, or screens. Digits is a physical retro telephone that makes encrypted voice calls over the internet to a small list of trusted contacts. No screen. No apps. No subscription. Just voice.

**Core principles:**

- **E2E encrypted** — the server operator cannot listen to calls
- **No subscription** — buy the hardware, own it forever
- **Open source** — all software, schematics, and BOM published under MIT
- **Self-hostable** — run the signaling server on your own hardware
- **Hardware kill switch** — the microphone is physically disconnected when the handset is on the cradle

For the full story, see [digits.family/why](https://digits.family/why).
```

- [ ] **Step 2: Rewrite why-digits.md**

Replace the full contents of `docs/why-digits.md` with:

```markdown
# Why Digits

An entire generation is growing up without ever making a phone call. The devices they carry are texting machines, content consumption terminals, and algorithmic attention traps that happen to have a phone app buried in a folder somewhere.

Digits is a return to what a phone was supposed to be: a device that connects two people in real time, with no screen, no feed, no algorithm, and no record.

**Why voice matters:**

- Real-time conversation builds skills that text doesn't develop — active listening, spontaneous articulation, emotional reading, sustained attention
- Kids need private spaces to develop. Digits has no call logging, no recording, and E2E encryption — not because there's something to hide, but because children deserve a space to be awkward and make mistakes without a permanent record
- A ringing bell creates genuine anticipation. A push notification creates anxiety. They are not the same thing.

Digits does one thing. It does it well. That's the point.

For the full argument, see [digits.family/why](https://digits.family/why).
```

- [ ] **Step 3: Commit**

```bash
git add docs/mission.md docs/why-digits.md
git commit -m "docs: slim mission and why-digits, link to digits.family for full versions"
```

---

### Task 5: Create architecture/overview.md

Consolidate the three deleted architecture docs into one. Pull from:
- `voip-call-path.md` — why WebRTC+Opus, call flow sequence, latency targets
- `networking-nat-traversal.md` — ICE/STUN/TURN strategy, coturn
- `cross-household-linking.md` — data model, household linking

Reference current code state: digitsd has full WebRTC audio working on LAN, TURN not yet wired in client.

**Files:**
- Create: `docs/architecture/overview.md`

- [ ] **Step 1: Write architecture/overview.md**

```markdown
# Architecture Overview

Digits is a point-to-point encrypted phone network. Each physical phone is a self-contained endpoint that connects to a central signaling server for call setup, then communicates directly with the other phone for audio.

## System Components

```
┌─────────────────────────────┐         ┌─────────────────────────────┐
│        Digits Phone A       │         │        Digits Phone B       │
│                             │         │                             │
│  ┌───────┐    ┌──────────┐  │         │  ┌──────────┐    ┌───────┐  │
│  │ Pico  │◄──►│ Pi Zero  │  │         │  │ Pi Zero  │◄──►│ Pico  │  │
│  │ RP2040│UART│ 2W       │  │         │  │ 2W       │UART│ RP2040│  │
│  └───────┘    │ (digitsd)│  │         │  │ (digitsd)│    └───────┘  │
│               └─────┬────┘  │         │  └────┬─────┘              │
│  Codec Zero (DA7212)│       │         │       │  Codec Zero (DA7212)│
└─────────────────────┼───────┘         └───────┼─────────────────────┘
                      │                         │
                      │  WebSocket signaling     │
                      └──────────┬──────────────┘
                                 │
                          ┌──────▼──────┐
                          │   signald   │
                          │  (Go server)│
                          └─────────────┘
```

**RP2040 Pico** — Handles real-time hardware: hook switch, keypad scanning, mechanical bell, tones, status LED. Communicates with the Pi over UART using a [line-based ASCII protocol](uart-protocol.md).

**Raspberry Pi Zero 2 W** — Runs `digitsd`, a Go daemon that owns the VoIP stack: WebSocket signaling client, WebRTC media endpoint (Pion), Opus encode/decode, ALSA audio capture and playback via the Codec Zero. Also handles OTA updates, Wi-Fi provisioning, and device pairing.

**Codec Zero (DA7212)** — Audio pHAT providing I2S audio I/O. External electret mic input via 3.5mm TRS jack, mono speaker output via screw terminal. Connected to Pi over I2C (control) and I2S (audio data).

**signald** — Go server providing WebSocket relay for SDP/ICE signaling, user authentication (magic links + optional Google OAuth), household and line management, admin panel. Does not touch audio — it only relays call setup messages.

## Data Model

![Data model](../diagrams/img/01-data-model.png)

- **Household** — A family group. Owns one or more lines.
- **Line** — A 7-digit phone number. Belongs to a household.
- **Device** — A physical Digits phone. Paired to a line via a pairing code.
- **HouseholdLink** — A connection between two households, established via invite code. Lines in linked households can call each other.

![Linking flow](../diagrams/img/02-linking-flow.png)

Households connect by exchanging invite codes through the web app. Once linked, any line in household A can call any line in household B. Links can be revoked by either household.

## Call Path

Digits uses WebRTC for media transport. Audio is encrypted end-to-end using DTLS-SRTP — the signaling server relays call setup messages but never sees or touches the audio stream.

**Why WebRTC:** Reuses the existing signaling protocol with minimal extension. Pion (Go WebRTC library) runs natively on Pi. Opus codec provides high quality at low bitrate. SRTP encryption is built in. The alternative (raw RTP or SIP) would have required building encryption, codec negotiation, and NAT traversal from scratch.

### Call flow

**Outgoing call:**
1. User lifts handset — Pico sends `HOOK:OFF`, plays dial tone
2. User dials 7 digits — Pico sends `KEY:*` events, then `DIAL:<number>`
3. `digitsd` creates a WebRTC peer connection with a local Opus audio track
4. SDP offer sent to signald, relayed to the called phone
5. Called phone answers — SDP answer returned, ICE candidates exchanged
6. SRTP media flows directly between the two Pis
7. Either party hangs up — `digitsd` sends hangup message, tears down peer connection

**Incoming call:**
1. signald sends ring message to `digitsd`
2. `digitsd` sends `RING:START` to Pico — mechanical bell rings
3. User lifts handset — Pico sends `HOOK:OFF`
4. `digitsd` creates peer connection, generates SDP answer
5. ICE candidates exchanged, SRTP media established

### Audio pipeline

- **Capture:** ALSA `plughw:1,0` at 48kHz stereo, right channel extracted (external mic input)
- **Processing:** Optional RNNoise ML denoiser for background noise suppression
- **Encode:** Opus 48kHz mono, 24kbps, VoIP mode, in-band FEC enabled, 20ms frames
- **Transport:** RTP/SRTP via Pion WebRTC
- **Decode:** Opus decoder on receiving Pi
- **Playback:** Mixed with local tones (dial tone, ringback, busy) via audio mixer, output to ALSA

**Latency:** Measured at 75-90ms one-way end-to-end. Target was <100ms.

## NAT Traversal

Phones on different home networks need to traverse NAT routers to establish direct media connections. Digits uses WebRTC's ICE framework:

1. **Host candidates** — direct LAN connection (works if both phones are on the same network)
2. **STUN** — discovers the phone's public IP and port mapping. Works for most consumer NAT routers (~75% of connections)
3. **TURN relay** — fallback for symmetric NAT and CGNAT (~20% of connections). The signaling server provides time-limited TURN credentials (HMAC-based, per RFC 5766) so phones can relay through a coturn server

**Why TURN is mandatory:** Roughly 20% of real-world connections will fail without it. Symmetric NAT (common in enterprise and some consumer routers) and CGNAT (T-Mobile Home Internet, Starlink, many ISPs) make STUN-only insufficient. Self-hosted coturn preserves the privacy model — it relays encrypted SRTP it cannot decrypt.

**Bandwidth:** Audio-only WebRTC uses ~40kbps per direction. Even heavy TURN relay usage costs negligible bandwidth at small scale.

## Current Status

**Working:**
- Full bidirectional Opus/WebRTC audio on LAN
- E2E encrypted calls (DTLS-SRTP)
- Mechanical bell ringing, dial tone, ringback, busy tone
- Household linking via invite codes
- Device pairing
- OTA firmware and daemon updates
- Service codes (volume, audio test, shutdown, reboot, re-pair, Wi-Fi setup, factory reset)
- RNNoise background noise suppression

**Next:**
- TURN/STUN integration in `digitsd` — server-side credential generation is built, client needs to request and use ICE servers before creating peer connections
- Reconnect behavior on dropped WebRTC connections
- Comfort noise generation on packet loss
```

- [ ] **Step 2: Commit**

```bash
git add docs/architecture/overview.md
git commit -m "docs: add consolidated architecture overview"
```

---

### Task 6: Update uart-protocol.md

Fix stale references. The doc references `dtmf-uart.service` and `~/digits/pi/uart.log` in the Hard Rules section — `digitsd` has replaced the old Python service. Also check FSM states against current firmware.

**Files:**
- Modify: `docs/architecture/uart-protocol.md` (moved in Task 2)

- [ ] **Step 1: Check current firmware FSM states and UART commands**

Read `firmware/src/main.c` (or wherever the FSM and UART handler live) to verify the command list and state names match the doc. Check for any new commands not listed. Also check if `dtmf-uart.service` is still referenced anywhere or if `digitsd` has fully replaced it.

```bash
# Find FSM state definitions in firmware
grep -rn "STATE\|FSM\|IDLE\|DIAL_TONE\|DIALING\|RINGING\|CONNECTED\|BUSY" firmware/src/ --include="*.c" --include="*.h" | head -40

# Check if dtmf-uart.service exists anywhere
grep -rn "dtmf.uart\|dtmf_uart" pi/ docs/ --include="*.py" --include="*.md" --include="*.service" | head -20

# Check digitsd UART commands
grep -rn "RING:START\|RING:STOP\|TONE:\|LED:\|HOOK:FORCE\|PING\|RESET\|STATE?\|KEYTEST\|KEYDUMP" pi/digitsd/ --include="*.go" | head -30
```

- [ ] **Step 2: Update Hard Rules section**

In `docs/architecture/uart-protocol.md`, replace the Hard Rules section. The old text references `dtmf-uart.service` and `~/digits/pi/uart.log`. Update to reference `digitsd`:

Old (line 128-132):
```markdown
## Hard Rules

1. **Never steal the serial port from `dtmf-uart.service`.** The service owns `/dev/serial0`. Monitor via `tail -f ~/digits/pi/uart.log`. See Hard Rules below.
2. **To send a debug command:** Stop service -> send command -> read response -> restart service immediately.
3. **UART line endings:** Pico requires `\r\n`. Bash `printf 'PING\n'` won't work. Use Python serial or `printf 'PING\r\n'`.
4. **Inject commands without stopping service:** Use `/tmp/ring_inject.py` for one-shot command injection.
```

New:
```markdown
## Hard Rules

1. **Never steal the serial port from `digitsd`.** The daemon owns `/dev/serial0`. Use `journalctl -u digitsd` to monitor.
2. **To send a debug command:** Stop the service (`systemctl stop digitsd`), send command, read response, restart immediately.
3. **UART line endings:** Pico requires `\r\n`. Bash `printf 'PING\n'` won't work. Use `printf 'PING\r\n'` or a serial tool.
```

Also update the Transport section (line 13):
Old: `- **Pi-side service:** `dtmf-uart.service` owns the serial port. All monitoring goes through `~/digits/pi/uart.log`. See Hard Rules below.`
New: `- **Pi-side service:** `digitsd` owns the serial port. See Hard Rules below.`

- [ ] **Step 3: Update any stale FSM states or commands based on Step 1 findings**

Apply any corrections found in Step 1. If any new commands exist in firmware or digitsd that aren't documented, add them. If any documented commands no longer exist, remove them.

- [ ] **Step 4: Commit**

```bash
git add docs/architecture/uart-protocol.md
git commit -m "docs: update UART protocol references from dtmf-uart to digitsd"
```

---

### Task 7: Update build/components.md

The BOM is stale — still lists WM8960 Audio HAT as needed, and many items show "arriving" status from March 2026.

**Files:**
- Modify: `docs/build/components.md` (moved in Task 2)

- [ ] **Step 1: Rewrite components.md**

Replace full contents with an updated BOM reflecting current state. The Codec Zero replaced the WM8960. The ringer uses an L298N H-bridge, not MOSFET/optoisolator. Remove items that were never used. Add items that are in the current build (L298N, LM2596 buck converter, ElectroCookie protoboard).

Check `docs/build/wiring.md` for the actual components used in the build to ensure accuracy.

```markdown
# Digits — Components

## Per Phone

| Component | Notes |
|-----------|-------|
| Sangyn Retro 2500 phone | Donor phone — gutted, keeping case/handset/keypad/bell |
| RP2040 Pico H | Pre-soldered headers. Firmware handles keypad, hook, bell, tones, LED |
| Raspberry Pi Zero 2 W | Runs digitsd (VoIP stack, signaling, audio) |
| Raspberry Pi Codec Zero (DA7212) | Audio pHAT — I2S, external mic in (3.5mm TRS), speaker out (screw terminal) |
| V-153-1C25 lever microswitch | Hook switch replacement. SPDT, 51mm lever arm |
| Omron D2F-01F subminiature microswitch | Mic kill switch. Breaks mic line when handset is on cradle |
| L298N H-bridge motor driver | Bell ringer — alternates coil polarity at 20Hz for AC drive |
| LM2596 buck converter | 12V wall wart down to 5.16V for Pi/Pico power |
| ElectroCookie solderable breadboard | Protoboard for wiring everything together |
| 220 ohm resistor | Status LED current limiter |
| 22 AWG hookup wire | Signal-level GPIO runs |
| Ferrule crimp terminals | Screw terminal connections |
| 6.3mm female spade terminals | Microswitch connections |
| 12V DC wall wart | Power supply |

## Tools

| Tool | Notes |
|------|-------|
| Soldering iron + solder | Protoboard assembly |
| Wire strippers | 22 AWG |
| Multimeter | Continuity checks, voltage verification |
| Ferrule crimper | For screw terminal connections |

See [wiring.md](wiring.md) for the full electrical spec and GPIO map.
```

- [ ] **Step 2: Commit**

```bash
git add docs/build/components.md
git commit -m "docs: update BOM to reflect current build"
```

---

### Task 8: Update build/wiring.md terminology

The wiring doc was verified 2026-03-22 and is mostly accurate. Check for old terminology and fix references.

**Files:**
- Modify: `docs/build/wiring.md` (moved in Task 2)

- [ ] **Step 1: Scan for old terminology**

```bash
grep -in "phone number\|directory\|contact list\|dtmf.uart\|dtmf_uart" docs/build/wiring.md
```

- [ ] **Step 2: Fix any findings from Step 1**

Replace old terminology with current terms. If no old terminology is found, no changes needed.

- [ ] **Step 3: Commit (if changes were made)**

```bash
git add docs/build/wiring.md
git commit -m "docs: update wiring.md terminology"
```

---

### Task 9: Light edits to datasheets.md and hardware-kill-switch.md

**Files:**
- Modify: `docs/build/datasheets.md` (moved in Task 2)
- Modify: `docs/build/hardware-kill-switch.md` (moved in Task 2)

- [ ] **Step 1: Check datasheets.md for stale content**

The doc lists a NOYITO FR120N marked as RETIRED. Remove it — no need to document components that were never used in the final build. Check for any other old terminology.

- [ ] **Step 2: Remove NOYITO FR120N section from datasheets.md**

Delete the NOYITO FR120N section entirely (it was replaced by L298N H-bridge).

- [ ] **Step 3: Scan hardware-kill-switch.md for old terminology**

```bash
grep -in "phone number\|directory\|contact\|dtmf.uart" docs/build/hardware-kill-switch.md
```

Fix any findings. This doc is likely clean — it's about circuit design.

- [ ] **Step 4: Commit**

```bash
git add docs/build/datasheets.md docs/build/hardware-kill-switch.md
git commit -m "docs: clean up datasheets and hardware kill switch docs"
```

---

### Task 10: Update hosting/self-hosting.md

Check against current server Docker config for accuracy.

**Files:**
- Modify: `docs/hosting/self-hosting.md` (moved in Task 2)

- [ ] **Step 1: Verify Docker Compose config**

```bash
cat server/docker-compose.yml
```

Compare service names, ports, database names, and environment variables against what the doc describes. Check if `server/docker/Caddyfile` matches the doc's architecture diagram.

- [ ] **Step 2: Check for stale references**

The doc references:
- `SQLite persistence, phone directory` in the main README (line 32) — this is a README issue, not self-hosting
- `pi/README-mixer.md` (line 150) — verify this file exists, update path if needed
- Clone URL `github.com/justinlindh/digits` — verify this is the intended public repo URL

```bash
ls pi/README-mixer.md 2>/dev/null || echo "file not found"
```

- [ ] **Step 3: Fix any stale references found in Steps 1-2**

Update paths, service names, or configuration details to match current code. If the Pi setup reference (`README-mixer.md`) no longer exists, update or remove that link.

- [ ] **Step 4: Commit**

```bash
git add docs/hosting/self-hosting.md
git commit -m "docs: update self-hosting guide for current server config"
```

---

### Task 11: Update build/teardown photos README

After moving photos into the new location, internal references may need updating.

**Files:**
- Modify: `docs/build/teardown/photos/README.md`

- [ ] **Step 1: Read the photos README**

```bash
cat docs/build/teardown/photos/README.md
```

- [ ] **Step 2: Fix any relative paths or references**

The photo README may reference paths relative to the old `docs/photos/` location. Update any relative links to work from the new `docs/build/teardown/photos/` location. Photo filenames should be unchanged.

- [ ] **Step 3: Commit (if changes were made)**

```bash
git add docs/build/teardown/photos/README.md
git commit -m "docs: fix photo README paths after move"
```

---

### Task 12: Update main README.md

Update the repo root README to fix stale content and add docs links pointing to digits.family and the new docs structure.

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Fix stale content in Architecture Summary**

Line 25: "Runs Python utilities and service logic" — should reference Go/digitsd, not Python.
Line 32: "SQLite persistence, phone directory" — server uses PostgreSQL and the "directory" concept is gone.
Line 40: "pi/         Pi Zero setup + Python test utilities" — should mention Go daemon.
Line 44: "docs/       Wiring notes, protocol documentation, planning docs" — update description.

Apply these edits:

Line 25, change:
```
   - Runs Python utilities and service logic
   - Owns audio stack setup and end-to-end call/crypto control paths
```
to:
```
   - Runs `digitsd`, a Go daemon for VoIP, signaling, and audio
   - Owns the WebRTC media endpoint, Opus codec, and call control
```

Line 31-33, change:
```
4. **Signaling Server (Go)**
   - WebSocket relay for SDP/ICE signaling between phones
   - SQLite persistence, phone directory, call history
   - Web UI dashboard (htmx + Tailwind)
```
to:
```
4. **Signaling Server (Go)**
   - WebSocket relay for SDP/ICE signaling between phones
   - PostgreSQL persistence, household and line management
   - Web app + admin dashboard
```

Line 39-44, change:
```
firmware/   RP2040 Pico firmware (C/CMake + Pico SDK)
pi/         Pi Zero setup + Python test utilities
server/     Go signaling server (WebSocket relay, web UI)
docs/       Wiring notes, protocol documentation, planning docs
scripts/    Build/flash helpers for Phase 0 bench workflow
```
to:
```
firmware/   RP2040 Pico firmware (C/CMake + Pico SDK)
pi/         Pi Zero userland — digitsd VoIP daemon, setup tools, image builder
server/     Go signaling server (WebSocket relay, web app, admin panel)
docs/       Hardware build guides, architecture, self-hosting
scripts/    Build and flash helpers
```

- [ ] **Step 2: Replace Documentation section**

Replace lines 113-121:
```markdown
## Documentation

- Wiring details: [docs/wiring.md](docs/wiring.md)
- UART protocol spec: [docs/uart-protocol.md](docs/uart-protocol.md)
- Component datasheets: [docs/datasheets.md](docs/datasheets.md)
- Self-hosting guide: [docs/self-hosting.md](docs/self-hosting.md)
- Architecture: [docs/architecture/](docs/architecture/)

- Service codes: [Service Codes](#service-codes)
- Easter eggs: [Easter Eggs](#easter-eggs)
```

with:
```markdown
## Documentation

- [Why Digits?](https://digits.family/why) — the problem and vision
- [How it works](https://digits.family/how-it-works) — overview for parents and curious people
- [Architecture](docs/architecture/overview.md) — technical deep-dive: call path, data model, NAT traversal
- [Build one](docs/build/components.md) — BOM and hardware guide
- [Wiring](docs/build/wiring.md) — full electrical spec and GPIO map
- [Self-hosting](docs/hosting/self-hosting.md) — run your own signaling server

See [docs/](docs/) for the full documentation index.
```

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: update README with current architecture and new docs links"
```

---

### Task 13: Final review pass

Verify everything is consistent and nothing is broken.

- [ ] **Step 1: Check for broken internal links**

```bash
# Find all markdown links to local files in docs/
grep -rn ']\(\./' docs/ --include="*.md" | head -30
grep -rn ']\(\.\.' docs/ --include="*.md" | head -30
grep -rn '](docs/' README.md

# Verify each linked file exists
```

- [ ] **Step 2: Check for remaining old terminology**

```bash
grep -rn "phone directory\|phone number\|dtmf.uart\|dtmf_uart\|SQLite" docs/ README.md --include="*.md"
```

Fix any remaining instances.

- [ ] **Step 3: Verify directory structure matches spec**

```bash
find docs/ -type f | sort
```

Expected structure:
```
docs/README.md
docs/mission.md
docs/why-digits.md
docs/build/components.md
docs/build/wiring.md
docs/build/datasheets.md
docs/build/hardware-kill-switch.md
docs/build/teardown/notes.md
docs/build/teardown/photos/README.md
docs/build/teardown/photos/*.jpg
docs/architecture/overview.md
docs/architecture/uart-protocol.md
docs/hosting/self-hosting.md
docs/diagrams/phone-fsm.dot
docs/diagrams/phone-fsm.png
docs/diagrams/img/01-data-model.png
docs/diagrams/img/01-data-model.svg
docs/diagrams/img/02-linking-flow.png
docs/diagrams/img/02-linking-flow.svg
docs/diagrams/img/03-system-overview.png
docs/diagrams/img/03-system-overview.svg
docs/superpowers/specs/2026-04-03-docs-cleanup-design.md
docs/superpowers/plans/2026-04-03-docs-cleanup.md
```

- [ ] **Step 4: Fix any issues found in Steps 1-3 and commit**

```bash
git add -A docs/ README.md
git commit -m "docs: final review fixes"
```
