# Digits Carrier Board -- Custom PCB Plan

Goal: replace the hand-wired ElectroCookie protoboard with a purpose-built PCB that carries the Pico, mounts the Pi+Codec Zero stack, integrates the power regulation, and breaks out connectors for all off-board components (keypad, hook switch, bell, LED, handset audio, etc.).

---

## Board Specifications

### Dimensions

- **Board outline:** 76.2mm x 56.9mm (3.0" x 2.24")
- Same footprint as original Sangyn PCB -- drops right into existing mounting posts.

### Mounting holes (origin = bottom-left corner)

| Post | X (mm) | Y (mm) |
|------|--------|--------|
| 1 | 8.4 | 23.4 |
| 2 | 67.3 | 10.2 |
| 3 | 72.4 | 40.9 |

Hole diameter: 3.2mm (M3).

### Height clearance

| Side | Clearance | Contents |
|------|-----------|----------|
| Body (components face up toward handset cradle) | ~19mm (0.75") | Pico, hook switch, connectors, power section |
| Floor (base plate side, below PCB) | ~48mm (1.9") | Pi+Codec Zero stack mounts here via J1 on B.Cu |

### Hook switch (on-board, replaces external V-153-1C25)

Position: **47.4mm, 39.4mm** from bottom-left origin (upper-right quadrant).

- **Footprint:** `Button_Switch_THT:SW_Push_6mm` (placeholder -- verify against sourced part)
- **Lever height (released):** 8.6mm above board
- **Lever height (pressed):** 2.4mm above board
- **Travel:** ~6.2mm
- **Logic:** Normally-open. Pressed (closed) = on-hook. Requires `hook_inverted: true` in config.json (see `feat/hook-invert-config` branch).

---

## Schematic

### Components

| Ref | Component | Value | Purpose |
|-----|-----------|-------|---------|
| A1 | RaspberryPi_Pico | -- | Microcontroller module (plug-in) |
| J1 | 2x20 female header | 2.54mm | Pi+Codec Zero stack (B.Cu, floor side) |
| U1 | LM2596S-5 | TO-263-5 | 12V to 5V buck regulator |
| C1 | Electrolytic cap | 680uF 25V | LM2596 input filter |
| C2 | Electrolytic cap | 220uF 25V | LM2596 output filter |
| D1 | Schottky diode | SB540 (5A 40V) | LM2596 flyback |
| L1 | Inductor | 33uH 3.6A | LM2596 output |
| C3 | Ceramic disc cap | 100nF | Mic bypass filter (ultrasonic) |
| R1 | Resistor | 220 ohm | Status LED current limiter |
| J3 | Barrel jack | 2.1x5.5mm | 12V power input |
| J4 | JST ZH 7-pin | 1.5mm pitch | Keypad ribbon cable |
| J5 | Pin header 1x4 | 2.54mm | L298N control (IN1, IN2, +12V, GND) |
| J6 | JST ZH 2-pin | 1.5mm pitch | Status LED |
| J7 | Phoenix screw terminal | 5mm pitch | Bell coil / transformer secondary |
| J8 | JST ZH 4-pin | 1.5mm pitch | Handset RJ9 (MIC_HOT, EAR_P, EAR_N, MIC_GND) |
| J9 | Pin header 1x3 | 2.54mm | Mic kill switch breakout |
| J10 | Pin header 1x2 | 2.54mm | Earpiece output to Codec Zero lineout |
| J2 | JST SH 3-pin | 1.0mm pitch | SWD debug (SWDIO, GND, SWCLK) |
| SW1 | Tactile switch | 6mm with lever | Hook switch |

### Key nets

| Net | From | To |
|-----|------|----|
| `UART_TX_PI` / `UART_RX_PI` | Pico GP0/GP1 | Pi GPIO15/GPIO14 (crossover) |
| `SWD_SWDIO` / `SWD_SWCLK` | Pi GPIO22/GPIO25 | J2 SWD connector |
| `HOOK_SW` | Pico GP10 | SW1 (other pin to GND) |
| `RINGER_IN1` / `RINGER_IN2` | Pico GP11/GP15 | J5 pins 1/2 |
| `LED_OUT` | Pico GP14 | R1 -> J6 |
| `KP_ROW0-3` | Pico GP2-5 | J4 pins 7-4 |
| `KP_COL0-2` | Pico GP6-8 | J4 pins 1-3 |
| `MIC_HOT` / `MIC_GND` | J8 pins 1/4 | C3, J9 pins 1/3 |
| `EAR_P` / `EAR_N` | J8 pins 2/3 | J10 pins 1/2 |
| `+5V` | LM2596 output | Pico VSYS, Pi 5V (pin 2) |
| `+12V` | Barrel jack | LM2596 input, J5 pin 3 |

### Audio path wiring at build time

- J8: handset RJ9 JST cable plugs directly in (no splicing)
- J9 pin 1 -> wire to D2F-01F COM, D2F-01F NO -> wire to J9 pin 2, TRS cable from J9 pins 2+3 -> Codec Zero mic jack
- J10: short wires to Codec Zero lineout screw terminals

---

## PCB Layout

- **2-layer board** (F.Cu + B.Cu)
- **Power section** (J3, U1, C1, C2, D1, L1) grouped on the left side
- **Pico** centered
- **Connectors** along right edge
- **J1 (Pi header)** on B.Cu -- Pi+Codec stack hangs toward floor
- **Ground plane** on B.Cu
- **Trace widths:** 0.25mm signal, 0.75mm power
- **Autorouted** with Freerouting, ground plane added after

---

## Build Status

- [x] Phase 1: Measure physical space
- [x] Phase 2: KiCad setup
- [x] Phase 3: Schematic
- [x] Phase 4: Footprint assignment
- [x] Phase 5: PCB layout and routing
- [x] Phase 6: DRC validation
- [x] Phase 7: Gerber export and board order (JLCPCB)
- [x] Phase 7b: Component sourcing (DigiKey + Amazon)
- [ ] Phase 8: Assemble and test

### Pre-assembly checklist

- [ ] **Hook switch part:** Source 6x6mm tactile switch with ~8.6mm lever. Try salvaging lever from original Sangyn PCB.
- [x] **Hook switch firmware:** Configurable inversion via `hook_inverted` config flag (PR #41).
- [x] **Component sourcing:** BOM ordered from DigiKey + Amazon. See `BOM.csv`.
- [x] **Test fit:** 1:1 paper printout verified in phone body.

### Assembly steps

1. Solder U1 (LM2596, SMD D2PAK) first -- hardest part
2. Solder through-hole power components (C1, C2, D1, L1, J3)
3. Power up, verify 5V output before plugging in anything else
4. Solder remaining through-hole components (R1, C3, SW1, all connectors)
5. Plug in Pico, verify UART (`PING`/`PONG`)
6. Plug in Pi+Codec stack on B.Cu side, verify boot and audio
7. Test each subsystem: keypad, hook switch, ringer, LED, handset audio

---

## V2 Ideas

Potential improvements for a future board revision.

### On-board H-bridge (DRV8871)

Replace the external L298N module with a DRV8871DDAR (TI, ~$2.50). 8-pin SOIC, 3.6A/45V, only needs one external current-sense resistor and a decoupling cap. Eliminates the L298N module, its cable, and the space it takes inside the phone body. Same two control pins (IN1, IN2).

### Bare RP2040 instead of Pico H module

Solder the RP2040 QFN-56 directly on the board with 2MB flash, 12MHz crystal, 1.1V regulator, and decoupling caps. Saves ~$4/board, dramatically reduces height on the body side (Pico H is the tallest component), and frees board space. More complex to design but well-documented reference circuit from the Pico datasheet.

### ESP32 as microcontroller alternative

The Pico's workload is trivial (keypad scan, hook debounce, ringer PWM, UART). An ESP32 could handle this and has built-in WiFi/BLE that might enable future features. Worth considering if RP2040/Pico sourcing becomes difficult. Would require a firmware rewrite but the logic is straightforward.

### Test points

Add exposed pads for key signals: +5V, +12V, GND, UART TX/RX, switching node (U1 OUT), and hook switch. Zero cost, makes assembly debugging with a multimeter much easier.

### On-board TRS 3.5mm jack for mic output

Currently J9 breaks out mic signals and a TRS plug is hand-crimped for the Codec Zero's jack. A board-mount TRS jack with a short cable to the Codec Zero would be cleaner.

### FPC connector for keypad

The keypad ribbon is a flat flex cable. A proper FPC zero-insertion-force connector might be more reliable than the JST ZH with crimped wires. Depends on the actual ribbon cable type in the donor phone.

---

## Reference

### 6.1 -- Self-check

- Run DRC in KiCad -- fix all errors, review all warnings
- Run ERC (Electrical Rule Check) in the schematic editor -- fix all errors
- Visually check that every connector is accessible from a board edge
- Verify the Pi Zero header and Pico header don't overlap physically

### 6.2 -- Export for review

Export and commit these to `hardware/pcb/`:

- Schematic PDF (File -> Plot from schematic editor)
- PCB 3D view screenshot (View -> 3D Viewer, take a screenshot)
- Board outline dimensions screenshot
- Gerber files (File -> Fabrication Outputs -> Gerbers)

Review checklist:
- Every net matches `docs/build/wiring.md`
- Component orientation and polarity are correct
- Power trace widths are adequate for current
- Mounting holes align with physical measurements
- No physical overlap between Pi stack and Pico

### 6.3 -- Iterate

Expect 2-3 rounds of review before ordering.

---

## Phase 7: Order Boards (DONE -- ordered from JLCPCB)

### 7.1 -- Generate manufacturing files

- File -> Fabrication Outputs -> Gerbers (use JLCPCB preset)
- File -> Fabrication Outputs -> Drill Files
- KiCad has built-in presets for JLCPCB, PCBWay, and OSH Park

### 7.1b -- Pre-order checklist (DO NOT SKIP)

Before generating Gerbers and ordering, resolve these deferred items:

- [ ] **Hook switch part selection:** Source a 6x6mm tactile switch with snap-on lever matching original specs (8.6mm released, 2.4mm pressed, ~6.2mm travel). Verify pin spacing matches the `SW_Push_6mm` footprint -- update footprint if different.
- [ ] **Hook switch firmware change:** Invert hook sense in firmware (GP10 LOW = on-hook instead of HIGH). One-line change in `firmware/src/hook.c`.
- [ ] **Inductor part selection:** Pick a specific 33uH inductor and verify its physical footprint matches what's assigned.
- [ ] **Capacitor sizing:** Verify 680uF and 220uF electrolytic cap footprint diameters match chosen parts (cap diameters vary by voltage rating and brand).
- [x] **Test fit:** Print the PCB layout at 1:1 scale on paper, place it inside the phone body, and verify mounting holes align and nothing collides with the bell, hook lever, or phone body walls.

### 7.2 -- Upload and order

- JLCPCB: upload the Gerber zip
- Options: 2-layer board, 1.6mm thickness, HASL finish, green solder mask (all defaults)
- 5 boards: ~$2-5 + ~$5-15 shipping
- Turnaround: ~5 days fabrication + shipping

### 7.3 -- Order components

Source from LCSC (JLCPCB's parts house), DigiKey, or Mouser. A detailed BOM with specific part numbers will be generated at this stage.

---

## Phase 8: Assemble and Test

1. Solder components onto one board
2. Power up with bench supply or 12V wall wart -- check 5V output before plugging in Pi/Pico
3. Plug in the Pico, verify UART communication (`PING`/`PONG`)
4. Plug in the Pi+Codec stack, verify boot and audio
5. Test each connector: keypad, hook switch, ringer, LED
6. Compare behavior to existing protoboard build
7. Note any issues for v2 revision

---

## Reference: Current Wiring Summary

Full wiring spec: `docs/build/wiring.md`
Component list: `docs/build/components.md`
Datasheets: `docs/build/datasheets.md`
Teardown photos: `docs/build/teardown/photos/`
UART protocol: `docs/architecture/uart-protocol.md`

### SMD conversion for PCBA assembly

The v1 board is almost entirely through-hole. Only U1 (LM2596S-5, TO-263-5) and J2 (JST SH, SMD horizontal) are surface-mount. This makes automated assembly expensive -- THT assembly costs ~2-3x more per component than SMD at services like JLCPCB.

**Components to convert to SMD equivalents:**

| Ref | Current (THT) | SMD Replacement | Package |
|-----|---------------|-----------------|---------|
| C1 | 680uF radial electrolytic | SMD aluminum electrolytic or polymer | 10x10mm or 8x8mm |
| C2 | 220uF radial electrolytic | SMD aluminum electrolytic | 6.3x7.7mm |
| C3 | 100nF ceramic disc | MLCC | 0805 |
| R1 | 220 ohm axial | Thick film resistor | 0805 |
| D1 | 1N5824 DO-201AD | SS54 or equivalent | SMA/SMB |
| L1 | 33uH radial (Fastron) | SMD power inductor | 10x10mm or 12x12mm |

**Components that stay THT** (connectors are inherently through-hole):
J1 (2x20 header), J3 (barrel jack), J4-J10 (JST/pin headers), J7 (screw terminal), SW1 (tactile switch).

**Cost impact estimate (JLCPCB turnkey):**
- v1 all-THT: ~$8-14/board at 5 units, ~$5-8 at 50 units
- v2 SMD passives + THT connectors: ~$5-8/board at 5 units, ~$3-5 at 50 units
- All prices exclude Pico module (~$4, hand-soldered or consignment)

The entire LM2596 reference design (U1, C1, C2, D1, L1) has well-documented SMD equivalents. This is the highest-impact conversion since it's 6 components in one cluster.
