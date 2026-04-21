# Digits PCBs

Index of the Digits project's printed-circuit-board revisions. Each revision lives in its own directory as a self-contained snapshot: schematic, PCB layout, Gerbers, BOM, and per-revision documentation.

## Revisions

| Rev  | Status        | Construction                    | Notes |
|------|---------------|---------------------------------|-------|
| V1   | Fabricated    | Hand-assembled                  | First PCB attempt. External Codec Zero HAT for audio; external L298N plus step-up transformer for ringer. Has documented defects (see `v1/ERRATA.md`). |
| V1.1 | Planned       | Hand-assembled                  | Incremental revision of V1 that corrects the V1 errata. Fix targets tracked per-entry in `v1/ERRATA.md`. |
| V2   | Fabricated    | Contract-assembly (JLCPCB PCBA) | Onboard TLV320AIC3104 audio codec and onboard DRV8871 H-bridge ringer driver. Not practical to hand-solder. Shipped with a J8 pinout errata (see `v2/ERRATA.md`). |
| V2.1 | In progress   | Contract-assembly (JLCPCB PCBA) | Minor spin of V2 with J8 pinout reassigned to match the stock Sangyn handset cable directly. Schematic, PCB, and docs in `v2.1/`. |

## V1 vs V2: two different builds

V1 and V2 are not the same circuit on different boards. They are two different hardware strategies for the same phone.

- **V1 is a bench-build target.** The board is single-sided friendly, uses mostly through-hole parts, and keeps the complex analog work (audio codec) and power electronics (ringer H-bridge plus a step-up transformer) on external plug-in modules. You can order V1 bare from any low-cost PCB house and populate it yourself with an iron and a multimeter. The trade-off is more external wiring inside the phone body and a handful of defects that require per-unit bodges, all documented in `v1/ERRATA.md`.

- **V2 is a fab-service target.** Onboard TLV320AIC3104 audio codec (32-pin QFN with exposed thermal pad) and onboard DRV8871 H-bridge (8-pin SOIC). Hand-population is not reasonable; V2 is designed to be ordered pre-assembled from JLCPCB or an equivalent PCBA service. The trade-off is higher per-unit cost and longer turnaround in exchange for a clean board with almost no internal wiring.

Pick **V1** (really V1.1 once it ships) if you want to understand the design, iterate on a subsystem, or build one at your bench from parts.

Pick **V2** (really V2.1 once it ships) if you want a production board with integrated audio and ringer and are willing to order assembled.

## What lives where

- `v<N>/README.md` -- per-revision introduction and file map
- `v<N>/ERRATA.md` -- defects and workarounds for each revision; present where the revision has known issues
- `v<N>/kicad/` -- KiCad project, schematic, layout, and project-local symbol library
- `v<N>/gerber/` -- Gerber output corresponding to the as-fabricated boards for that revision
- `docs/build/wiring.md` (repo-wide, not per-revision) -- off-board wiring reference that applies across V1: handset RJ9 to JST adapter, L298N ringer wiring with transformer pairs, keypad ribbon, hook switch, mic kill switch
