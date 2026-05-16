# Digits Documentation

## Build One

Everything you need to build a Digits phone from scratch.

- [Components](build/components.md) -- bill of materials and procurement
- [Wiring](build/wiring.md) -- full electrical spec, GPIO map, connectors
- [Datasheets](build/datasheets.md) -- component reference sheets
- [Hardware kill switch](build/hardware-kill-switch.md) -- mic privacy circuit
- [Teardown photos](build/teardown/photos/) -- Sangyn 2500 disassembly reference photos

## How It Works

- [Architecture overview](architecture/overview.md) -- system design, call path, data model, NAT traversal
- [UART protocol](architecture/uart-protocol.md) -- Pico/Pi communication spec
- [Voicemail](voicemail.md) -- answering machine engineering reference: call FSM, on-disk storage format, audio pipeline, signaling, service codes
- [State machine](diagrams/phone-fsm.dot) -- firmware FSM ([rendered](diagrams/phone-fsm.png))

## Using Your Phone

- [Quick start guide](user-guide.md) -- setup and everyday use
- [Voicemail guide](voicemail-guide.md) -- using voicemail from the handset and configuring it in the web app

## Run Your Own Server

- [Self-hosting guide](hosting/self-hosting.md) -- Docker Compose deployment, TLS, backup, troubleshooting

## Why Digits?

- [digits.family/why](https://digits.family/why) -- the problem and vision
- [digits.family/how-it-works](https://digits.family/how-it-works) -- how the system works
- [Mission](mission.md) -- short project mission statement
- [Why Digits](why-digits.md) -- why voice calls matter for kids
