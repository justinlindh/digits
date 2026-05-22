# Changes from V2 to V3

V3 is a major revision of the carrier board. It keeps the V2 architecture (RP2040 + Pi Zero 2 W + TLV320AIC3104 codec + DRV8871 bell driver) but changes the power input, the bell drive, the hook and mic-kill mechanism, and the component side, plus the connector pin and reference-designator fixes carried in from V2 bring-up. V2 fabricated boards remain usable with the per-unit cable adapter rework and firmware workarounds documented in `hardware/pcb/v2/ERRATA.md`.

For the bell-drive and mic-kill design rationale (including rejected alternatives) see `PLANNED.md`.

## Power

### 5 V input, buck stage removed

V2 took 12 V in and stepped it down to 5 V with an LM2596 buck converter (U1 + L1 + D1 + C2). V3 takes +5 V in directly and removes the entire buck stage. The +5 V rail feeds the Pi via the 40-pin header and the U5 AMS1117-3.3 LDO exactly as before; only the upstream stage is gone. There is no +12V net on V3.

| | V2 | V3 |
|---|---|---|
| Input voltage | 12 V | 5 V |
| Buck regulator | U1 LM2596S-5 + L1 33 µH + D1 SS54 + C2 220 µF | removed |
| Top rail | +12V | +5V |

### Input connector upsized to JST XH

The input connector reference is now `PWR` (was `J3`). The part changed from JST ZH (1.0 A per contact, at its limit on V2 during ringer peaks) to JST XH B2B-XH-A, 2.5 mm pitch, ~3 A (LCSC C158012). Net path: `PWR` -> `/VIN_RAW` -> F1 (1.5 A PTC) -> +5V.

## Bell drive: on-board XL6019 boost replaces the external mains transformer

V2 drives the DRV8871 (U2) into an external 120V:12V mains transformer used in reverse as a step-up, with the high-voltage secondary on the bell coils. V3 deletes the transformer. U2's motor supply (VM, pin 5) is now fed from `VBOOST`, an on-board ~37 V rail.

New parts on the `/ringer/` sheet:

| Ref | Part | LCSC | Role |
|---|---|---|---|
| U10 | XL6019E1, TO-263-5 | C73018 | +5 V to ~37 V boost converter; tab (pad 6) on SW_NODE |
| L10 | 47 µH | C9906 | Boost inductor (+5V -> SW_NODE) |
| D10 | SS56 Schottky | C65009 | Boost rectifier (SW_NODE -> VBOOST) |
| C100 | 100 µF / 63 V | C28241 | VBOOST bulk |
| C101 | 1 µF 0805 | C105952 | VBOOST HF bypass |
| R20 | 57.6 kΩ | C26983 | FB divider top (VBOOST -> FB) |
| R21 | 2 kΩ | C4109 | FB divider bottom (FB -> GND) |

Vout = 1.25 * (1 + R20/R21) ~= 37.25 V. The DRV8871 VM bypass/bulk caps C54 (100 nF) and C55 (47 µF) now sit on VBOOST instead of the old +12V rail. Bench-validated: ~78 dBA at 33 V, comparable to the ~79 dBA transformer.

## Hook and mic-kill: single DPDT cradle switch

V2 used a separate tactile hookswitch (`SW1`, 6 mm) for hook sense plus a separate physical microswitch wired through the `J9` connector for mic kill. V3 replaces both with one 6-pin DPDT telephone hook switch, `SW1`, custom footprint `SW_DPDT_Hook_24.2x17.1mm`, that presses the cradle plunger.

- Pole 1: common pin 2 = `HOOK_SW` switches between pin 3 = GND and pin 1 (unused). Hook sense.
- Pole 2: common pin 5 = `MIC_HOT` switches between pin 4 = `MIC_FROM_SW` and pin 6 (unused). Series mic interrupt; mic is dead on-hook (privacy, no GPIO override).

This retires the V2 tactile SW1 and the J9 mic-kill connector. SW1 is the only part on the back copper side (see component-side flip below).

## SW2 BOOTSEL tact switch

V3 adds `SW2`, a 6 mm momentary tact switch between `QSPI_SS` and GND. Held during power-on it enters the RP2040 bootrom. Eliminates the V2 paperclip-on-U4-pin-1 procedure that destroyed a V2 unit's SWD subsystem.

## Power indicator LEDs

V3 adds two on-board indicator LEDs:

- `D2` (red) on +5V through `R12` = 300 Ω.
- `D3` (green) on +3V3 through `R13` = 330 Ω.

These are generic 0603 LEDs and current-limit resistors with no LCSC part assigned in the schematic; assign before fab.

## Handset and LED connector pin assignments

### J8 handset connector

The J8 pin-to-net mapping matches the stock Sangyn Retro 2500 RJ9 handset cable directly, so no per-unit adapter wire swap is needed.

| J8 pin | V2 net | V3 net | Stock cable wire | Handset function |
|---|---|---|---|---|
| 1 | `MIC_HOT` | `MIC_HOT` | Black | Mic + |
| 2 | `EAR_P` | `GND` | Yellow | Mic return |
| 3 | `EAR_N` | `EAR_P` | Red | Earpiece |
| 4 | `GND` | `EAR_N` | Green | Earpiece |

V2 assumed the mic pair sat on the outer pins (1, 4). The stock cable puts the mic pair on the inner-left pins (1, 2), so the V2 `EAR_P` net was tied to the mic return wire and coupled playback into the mic capsule, breaking full-duplex audio on every V2 unit.

### LED connector polarity

The `LED` connector (was `J6`) pin 1 and pin 2 net assignments are swapped from V2 so the stock phone LED cable plugs in directly.

| LED pin | V2 net | V3 net |
|---|---|---|
| 1 | `LED_A` | `GND` |
| 2 | `GND` | `LED_A` |

V2 wired pin 1 to the LED anode, but the donor phone's LED cable carries the opposite polarity, leaving the LED reverse-biased on every V2 unit (see `hardware/pcb/v2/ERRATA.md` section 8).

## Connector reference designators

V3 gives the connectors semantic reference designators, consistent across schematic and PCB:

| V2 | V3 | Function |
|---|---|---|
| J3 | PWR | +5 V input |
| J6 | LED | Indicator LED to the phone housing |
| J4 | KEYPAD | 7-pin keypad matrix |
| J7 | BELL | Bell H-bridge output (BELL_A / BELL_B) |
| J1 | Pi Zero W 2 | 40-pin Pi header |
| J9 | (removed) | Old mic-kill loop, folded into SW1 |

## Component-side flip

V2 boards arrived with components on the side that faces down into the phone shell when the mounting holes align to the enclosure standoffs, leaving the Pi header unreachable from above. V3 flips the components to face up so the Pi header is reachable. `SW1` is the only back-side part, because it must press the cradle plunger.

## BOM and firmware notes

No flash chip swap is needed: the unified firmware uses `boot2_generic_03h`, which works on the existing W25Q16JV (U4). The 10 kΩ UART_TX_PI pull-up is optional; firmware idles GP28 high in its place.

## Carried forward from V2

The codec subsystem (TLV320AIC3104 + XC6206P182 1.8 V LDO), the RP2040 + W25Q16JV + crystal subsystem, the +3V3 LDO (U5 AMS1117-3.3), the DRV8871 itself, the keypad and SWD and UART interfaces, the layer stack, and the design rules are unchanged from V2.
