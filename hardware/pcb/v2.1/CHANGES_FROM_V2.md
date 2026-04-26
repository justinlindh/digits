# Changes from V2 to V2.1

V2.1 is a minor revision. V2 fabricated boards remain usable with a per-unit cable adapter rework (see `hardware/pcb/v2/ERRATA.md` section 1).

## Electrical

### SW2 BOOTSEL button

A 6 mm momentary tact switch (`SW2`, footprint `Button_Switch_THT:SW_PUSH_6mm`) is wired between the `QSPI_SS` net (U3 pin 56 / U4 pin 1) and `GND`. Holding it while powering up the carrier drags QSPI_SS low during the RP2040 bootrom's flash-CS sample window, putting the chip into BOOTSEL (USB MSC + PICOBOOT) until reset. This eliminates the paperclip-on-U4-pin-1 procedure that V2 required for first flash and recovery, and that removes the failure mode documented in `hardware/pcb/v2/ERRATA.md` item 4 (a slipped paperclip shorting +3.3 V to GND killed one V2 RP2040's SWD interface during bring-up).

PCB placement of SW2 is handled in a separate change.

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

V2.1 is pin-compatible with the V2 enclosure, mounting holes, SW1 hookswitch position, and JST ZH cable assemblies. BOM gains one line: `SW2`, the 6 mm BOOTSEL tact switch (same part as SW1).

## Carried forward from V2

Everything else — rail architecture, codec subsystem, RP2040 firmware interface, ringer subsystem, connector footprints, silkscreen layout, layer stack, design rules — is unchanged from V2.
