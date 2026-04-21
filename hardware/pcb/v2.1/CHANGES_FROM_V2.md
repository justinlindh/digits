# Changes from V2 to V2.1

V2.1 is a minor revision. V2 fabricated boards remain usable with per-unit cable adapter rework on the affected connectors (see `hardware/pcb/v2/ERRATA.md` sections 1 and the V1 errata that also applies to V2).

## Electrical

### J6 status-LED connector pin polarity

The J6 pin-to-net mapping was swapped so that plugging in the stock LED pigtail lights the LED without re-crimping. In V2, J6.1 was the driven anode side (through R1) and J6.2 was GND; the stock pigtail crimps the opposite polarity at the JST end and back-biased the LED.

| J6 pin | V2 net | V2.1 net |
|---|---|---|
| 1 | `LED_A` (driven through R1 from GPIO16) | `GND` |
| 2 | `GND` | `LED_A` (driven through R1 from GPIO16) |

Same class of defect as the J8 issue below (and as V1 ERRATA #5): a connector whose polarity was adopted without an explicit wire-to-pin assertion against a physical cable. V2.1 matches the as-crimped pigtail.

Implementation: two global-label text swaps at J6 in `kicad/digits-pcb.kicad_sch` (no symbol or wire moves). Update PCB from Schematic then re-route the two short nets around J6.

### J8 handset connector pin assignment

The J8 pin-to-net mapping was reassigned to match the stock Sangyn Retro 2500 RJ9 handset cable directly. No per-unit adapter wire swap is needed for V2.1 boards.

| J8 pin | V2 net | V2.1 net | Stock cable wire | Handset function |
|---|---|---|---|---|
| 1 | `MIC_HOT` | `MIC_HOT` | Black | Mic + |
| 2 | `EAR_P` | `GND` | Yellow | Mic − |
| 3 | `EAR_N` | `EAR_P` | Red | Earpiece |
| 4 | `GND` | `EAR_N` | Green | Earpiece |

V2 assumed the mic pair sat on the outer pins (1, 4). The stock cable actually puts the mic pair on the inner-left pins (1, 2), which meant the V2 `EAR_P` net was tied to the mic return wire and coupled playback into the mic capsule. This broke full-duplex audio on every V2 unit.

Implementation: three global-label text changes at J8 in `kicad/digits-pcb.kicad_sch` (no symbol or wire moves), followed by Update PCB from Schematic and a fresh route for the three affected nets (`GND`, `EAR_P`, `EAR_N`). `MIC_HOT` routing at J8.1 is unchanged.

## Mechanical and BOM

No changes. V2.1 is pin-compatible with the V2 enclosure, mounting holes, SW1 hookswitch position, and JST ZH cable assemblies. BOM is identical.

## Carried forward from V2

Everything else — rail architecture, codec subsystem, RP2040 firmware interface, ringer subsystem, connector footprints, silkscreen layout, layer stack, design rules — is unchanged from V2.
