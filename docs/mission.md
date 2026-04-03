# Digits — Mission Statement

## Why This Exists

Children are growing up in a world where every communication tool is also a surveillance tool, an advertising platform, and a gateway to infinite distraction. Phones have screens. Screens have apps. Apps have algorithms designed to maximize engagement, not healthy development.

Digits exists because kids deserve a way to talk to their friends — just talk — without any of that. A phone that's just a phone.

## The Problem

- Every "kids' phone" or "kids' smartwatch" on the market is a surveillance device marketed to anxious parents
- Open platforms like Discord create passive radicalization pipelines through discovery mechanics
- Screen-based communication atrophies the social skills that voice conversation builds — tone, timing, empathy, thinking on your feet
- Parents are forced to choose between "no phone" (social isolation) and "smartphone" (unlimited risk)
- PBX/IVR hell has destroyed the concept of calling someone and getting a human

## What Digits Is

A physical retro telephone — heavy, satisfying, with a real mechanical bell — that makes encrypted voice calls over the internet to a small, curated contact list. No screen. No apps. No internet access. No surveillance. Just voice.

- **E2E encrypted** — not even the server operator can listen
- **No subscription** — you buy the hardware, you own it, forever
- **No parental backdoor** — privacy is a right, including for children (backed by developmental psychology research: Deci & Ryan, Smetana, danah boyd, Leah Plunkett)
- **Open source** — all software, all schematics, all bill of materials, published and forkable
- **Self-hostable** — the signaling server can run on your own hardware; you don't depend on us
- **Hardware kill switch** — when the phone is on the cradle, the microphone is physically disconnected. No software override possible. A device in a child's bedroom should not be capable of listening when idle.

## Who It's For

- Parents who want their kids to build real social skills through voice conversation
- Families who value privacy and reject surveillance-as-parenting
- Kids (ages ~8-14) who want to call their friends without a smartphone
- Anyone who misses the simplicity of picking up a phone and calling someone
- Tinkerers and hackers who want to build or modify their own

## Design Philosophy

### Open Everything
- All source code: MIT or Apache 2.0
- All hardware schematics and wiring diagrams: published
- Full bill of materials with sourcing links
- If someone wants to clone this and build their own from scratch — great. That's the point.
- Fuck the DMCA. Every buyer owns their device completely. Tinker, hack, modify, void nothing.

### Self-Hosting First
- The default Digits network runs a hosted signaling server for convenience
- Every phone ships with the ability to point at a custom server endpoint
- The server is open source and lightweight enough to run on a Raspberry Pi
- If Digits-the-company disappears tomorrow, every phone keeps working on self-hosted infrastructure
- Admin panel supports custom endpoint configuration

### No Subscriptions, Ever
- The server cost per-user is trivial (WebSocket signaling + TURN relay)
- Hardware price includes lifetime access to the Digits network
- If we ever need community funding for infrastructure, it's voluntary contribution — never a gate

### Hardware You Can Trust
- Physical mic disconnect on cradle (not software mute — hardware kill)
- No camera. No screen. No accelerometer. No GPS. Minimum attack surface.
- The less a device can do, the less it can be exploited

## Easter Eggs & Future Features

These are stretch goals and fun ideas — none of them change the core product mission. They're opt-in, power-user features that live alongside the primary purpose of kid-to-kid voice calls.

### Voice Assistant Integration (Fork/Power-User)
- Dial a special code (e.g., `*BOT` or `#0`) to talk to an AI assistant
- Uses the existing audio path: mic → STT → AI → TTS → earpiece
- **This is a paradox** — connecting to advanced intelligence is almost the opposite of the product's purpose. It's explicitly a power-user/adult feature, never a default, never marketed to kids.
- Could be amazing for the builder/tinkerer: "Hey, what are we working on today?"

### Dial 0 for Operator
- Users can dial `0` and reach a real human for support
- Old-school operator concept — no IVR trees, no "our options have changed," just a person
- Initially: rings directly to the creator's phone
- At scale: would need a support team, but the principle remains — humans helping humans

### Games
- Simon Says via keypad (sequences, high scores)
- Potentially other audio-based games that work within the phone's constraints
- Fun additions that don't require a screen

### The Spirit
A digital phone that emulates retro POTS has endless possibilities. The key is that every feature we add must pass the test: *does this serve the mission, or does it undermine it?* Easter eggs are fine. Scope creep into "smartphone with extra steps" is not.

## Market Thesis

- $150 price point (hardware only, no subscription)
- BOM at prototype scale: ~$60-80/unit. At 100+ units from suppliers: significantly less.
- Target: privacy-conscious parents, homeschool communities, anti-surveillance advocates, retro tech enthusiasts
- Channel: Kickstarter for initial validation, then direct sales
- Proof of concept: 6 prototype phones distributed to creator's son and friends. If kids actually use them to call each other, that's product-market fit no survey can match.
- Open source is not a business risk — at this price point, people buy convenience. "I could build my own" ≠ "I will build my own."

## Proof Points

- Full working prototype (2026): mechanical bell ringer, DTMF keypad, hook switch, UART-linked Pico+Pi, Codec Zero audio with ML noise suppression, E2E encrypted WebRTC architecture designed
- Validated with parents and a child psychologist — the problem resonates
- Research-backed privacy stance (Self-Determination Theory, social domain theory, developmental psychology literature)
- Creator has 30 years of software engineering experience and deep hardware integration skills
