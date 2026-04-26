# Changes from V2 to V2.1

V2.1 is a minor revision that addresses bring-up issues found in V2. V2 fabricated boards remain usable with per-unit cable adapter rework and firmware-side workarounds documented in `hardware/pcb/v2/ERRATA.md`.

## Electrical

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

### J6 LED connector polarity

J6 pin 1 and pin 2 net assignments were swapped so the stock phone LED cable plugs in directly without per-unit rework.

| J6 pin | V2 net | V2.1 net |
|---|---|---|
| 1 | `LED_A` | `GND` |
| 2 | `GND` | `LED_A` |

V2 wired pin 1 to the LED anode, but the donor phone's LED cable carries the opposite polarity, leaving the LED reverse-biased on every V2 unit (see `hardware/pcb/v2/ERRATA.md` section 8).

Implementation: two global-label y-coordinate swaps at J6 in `kicad/digits-pcb.kicad_sch` (no symbol move, no footprint change), followed by Update PCB from Schematic and a fresh route for the two affected segments at J6.

### 10 kΩ pull-up on UART_TX_PI to +3V3 (optional)

V2 left the UART_TX_PI net (RP2040 GP28 to Pi J1.10) without an external pull-up. RP2040 GPIOs default to input-with-weak-pull-down at reset, so the line sits near 0 V until firmware drives it high in `main()`. The Pi's PL011 saw phantom RX bytes coupled from each TX edge during the floating-line window, presenting as a hardware loopback bug.

V2.1 may add a 10 kΩ resistor from UART_TX_PI to +3V3 on the carrier. Holds the line at UART idle even when the RP2040 is unflashed, held in reset, or in deep sleep.

**Status:** demoted to optional after the unified firmware made the firmware-side workaround permanent. `main.c` drives both candidate UART_TX pins (GP0 and GP28) high in the pre-bootstrap window before `board_init()` runs. The hardware pullup remains a clean implementation but is no longer required for V2.1 to function. See V2 ERRATA item 2 and `docs/architecture/unified-firmware.md`.

## Mechanical and BOM

### Component-side flip

V2 boards arrived with components on the side that ends up facing *down* into the phone shell when mounting holes align to the enclosure standoffs. Pi header is unreachable from above; populated parts are sandwiched between the carrier and the shell.

V2.1 flips components to the opposite copper side (or mirrors the mounting hole positions across the board's Y-axis, whichever requires fewer route changes). Net topology and routing geometry are preserved.

Implementation work is non-trivial KiCad surgery. Tracked in #313 alongside the other V2.1 schematic edits.

See V2 ERRATA item 1.

### BOM

V2.1 BOM is identical to V2. The flash chip swap is no longer needed (unified firmware uses `boot2_generic_03h` which works on the existing W25Q16JV). The 10 kΩ UART pullup resistor is optional; not strictly required.

## Carried forward from V2

Everything else (rail architecture, codec subsystem, RP2040 firmware interface, ringer subsystem, connector footprints, layer stack, design rules, the rest of the BOM) is unchanged from V2.
