# Digits PCB V1

First printed circuit board for the Digits project. Replaces a hand-wired ElectroCookie protoboard with a single carrier board that holds the Raspberry Pi Zero 2 W + Codec Zero HAT stack, the RP2040 Pico, power regulation, and connectors to every off-board component (keypad, hook switch, bell coil, status LED, handset audio).

V1 is a **hand-assembled board**. It is fabricated bare from any low-cost PCB house and populated at the bench with through-hole parts and a few hand-solderable SMD parts. Audio (Codec Zero HAT) and ringer (external L298N driver module plus a step-up transformer) live as external modules on headers rather than being integrated onto the PCB itself. See `hardware/pcb/README.md` for the V1-vs-V2 build-strategy framing.

## Status

Partially functional. Keypad, hook switch, handset audio, status LED, UART-to-Pi comms, power regulation, and the external ringer subsystem all work on boards built with the documented rework. The board has several defects that require per-unit bodges or careful wiring; read `ERRATA.md` before building one. A **V1.1** revision is planned to correct the defects; in-progress Gerbers live in `gerber-v1.1/`.

## File map

```
hardware/pcb/v1/
├── README.md                # this file
├── ERRATA.md                # known defects, workarounds, and V1.1 fixes -- READ FIRST
├── PLAN.md                  # original V1 design plan, connector list, and rationale
├── BOM.csv                  # bill of materials
├── mems-validation-plan.md  # MEMS mic disable procedure for Codec Zero HAT
├── kicad/                   # KiCad schematic, PCB layout, project files
├── gerber/                  # V1 Gerbers (as fabricated)
├── gerber-v1.1/             # V1.1 Gerbers (in progress)
└── renders/                 # PCB 3D renders
```

## Build references

- `docs/build/wiring.md` -- canonical off-board wiring including the keypad ribbon, transformer primary/secondary pairs, L298N ringer connections, hook switch, mic kill switch, and handset RJ9 adapter. Applies across V1 builds.
- `ERRATA.md` -- defects and per-unit workarounds. **Read before ordering bare boards or starting assembly.**
- `PLAN.md` -- original design decisions, connector assignments, and trade-off notes.
- `mems-validation-plan.md` -- the Codec Zero HAT ships with a live MEMS microphone that requires a hardware disable on builds destined for private spaces. This document is the validation and disable procedure.
