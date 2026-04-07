# Digits Carrier Board -- Custom PCB Plan

Goal: replace the hand-wired ElectroCookie protoboard with a purpose-built PCB that carries the Pico, mounts the Pi+Codec Zero stack, integrates the power regulation, and breaks out connectors for all off-board components (keypad, hook switch, bell, LED, handset audio, etc.).

---

## Phase 1: Measure and Document the Physical Space (DONE)

### Board dimensions

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

| Direction | Clearance | Notes |
|-----------|-----------|-------|
| Toward metal base plate (top when installed) | ~48mm (1.9") | Plenty of room. Pi+Codec Zero stack (~16-17mm) faces this side. |
| Toward phone body (bottom when installed) | ~19mm (0.75") | Tight. Only through-hole leads and low-profile connectors on this side. |

**Constraint:** The Pi Zero + Codec Zero stack must be mounted on the base-plate-facing side of the PCB. All tall components go on that side. The phone-body-facing side should have only flat/low-profile parts.

### Hook switch (on-board, replaces external V-153-1C25)

The original Sangyn PCB has a through-hole lever tactile switch at board position **47.4mm, 39.4mm** (from bottom-left origin), in the upper-right quadrant of the board. It has a blue switch body with a white snap-on plastic lever that extends above the board surface, actuated by the phone cradle mechanism pressing down from above.

- **Footprint:** Using `Button_Switch_THT:SW_Push_6mm` as placeholder in schematic. **Before ordering boards**, source the actual switch part: need a 6x6mm through-hole tactile switch with a snap-on extended lever that reaches 8.6mm above the board (2.4mm when pressed). The original Sangyn switch has a blue body with white plastic lever. Search for "6x6mm tact switch with lever" or Alps SKHH series equivalents. The footprint may need updating if the actual switch has different pin spacing than the generic 6mm footprint.
- **Lever height (released):** 8.6mm (0.337") above board surface
- **Lever height (pressed):** 2.4mm (0.095") above board surface
- **Travel:** ~6.2mm
- **Actuation:** Cradle presses lever downward when handset is placed on hook
- **Wiring:** Connect to Pico GP10 + GND (replaces external V-153-1C25 + JST-XH connector)
- **Logic:** Switch is **normally-open**. Pressed (closed) = on-hook (handset on cradle). Released (open) = off-hook (handset lifted).
- **Note:** Current firmware expects GP10 HIGH = on-hook (internal pull-up, V-153-1C25 NC pulls to GND when off-hook). The on-board switch has inverted logic (closed = on-hook instead of open = on-hook). Either wire the switch to pull GP10 LOW when closed (on-hook) and invert the firmware logic, or wire it so closing the switch pulls GP10 HIGH. Simplest approach: switch connects GP10 to GND when closed (on-hook), making GP10 LOW = on-hook -- requires a one-line firmware change to invert the hook sense.

### Other notes

- Bell assembly is adjacent to but not overlapping the PCB area
- Keypad ribbon cable exits toward the board edge (connector placement TBD based on which edge)
- 12V power cable enters through former RJ11 port (connector placement TBD)

---

## Phase 2: Install KiCad and Learn the Basics (DONE)

### 2.1 -- Install KiCad 8

```bash
# Debian/Ubuntu:
sudo apt install kicad

# Or grab the latest from https://www.kicad.org/download/
```

KiCad 8 is the current stable release. It includes the schematic editor, PCB layout editor, footprint/symbol libraries, and Gerber viewer.

### 2.2 -- Watch one tutorial

DigiKey's "KiCad 8 Getting Started" series on YouTube is the gold standard. Watch at minimum:

- Episode 1: Project setup and schematic basics (~20 min)
- Episode 2: Schematic symbols and wiring (~20 min)
- Episode 5: PCB layout basics (~25 min)

You don't need to watch them all before starting -- just enough to understand the workflow: **Schematic** (logical connections) -> **Assign Footprints** (physical shapes) -> **PCB Layout** (place and route).

### 2.3 -- Create the project

- Open KiCad, File -> New Project
- Name it `digits-carrier` or similar
- Save it inside the repo at `hardware/pcb/`
- This creates two files: `digits-carrier.kicad_sch` (schematic) and `digits-carrier.kicad_pcb` (board layout)

---

## Phase 3: Draw the Schematic (DONE)

Capture the logical circuit -- "what connects to what." No physical layout yet. Enter one section at a time.

### 3.1 -- RP2040 / Pico section

**Option A (recommended for v1): Keep the Pico H as a plug-in module.** Place a 2x20 pin header symbol representing the Pico's pins. The PCB gets two rows of through-hole pads that the Pico plugs into or gets soldered to. Much simpler than dealing with the RP2040's QFN package, external flash, crystal, etc.

**Option B (advanced, v2): Bare RP2040 on-board.** Place the RP2040 chip directly with supporting circuitry (16MB flash, 12MHz crystal, 1.1V regulator, decoupling caps). Saves ~$4/board and is more compact but significantly harder.

### 3.2 -- Pi Zero 2 W header

- Place a 2x20 pin header symbol (same footprint, different labels)
- This is where the Pi + Codec Zero stack plugs in
- Label the pins actually used: GPIO14/15 (UART), GPIO22/25 (SWD), and the I2C/I2S pins (used by Codec Zero, directly through the header)

### 3.3 -- Power section

- 12V barrel jack or screw terminal input
- LM2596-based buck converter circuit: LM2596 IC + input cap (680uF electrolytic) + output cap (220uF electrolytic) + Schottky diode (1N5824) + inductor (33uH). The LM2596 datasheet has the exact reference circuit.
- Output: 5V rail feeding both headers
- Also break out the 12V rail to the H-bridge section

### 3.4 -- H-bridge / ringer section

**Option A (recommended for v1): Keep L298N as off-board module.** Put a 4-pin connector on the PCB (IN1, IN2, 12V, GND) that wires to the L298N module.

**Option B (v2): On-board H-bridge.** Use a smaller IC like the DRV8871 (8-SOIC, 3.6A, two control pins). The L298N is oversized for driving a transformer primary.

### 3.5 -- Connectors

Add symbols for each off-board connection:

| Connector | Pins | Purpose | Physical footprint |
|-----------|------|---------|-------------------|
| Conn_01x07 (J4) | 7 | Keypad ribbon cable | **JST ZH 1.5mm pitch** (`JST_ZH_B7B-ZR_1x07_P1.50mm_Vertical`) -- matches phone keypad JST plug |
| SW_Push (on-board) | 2 | Hook switch -- replaces external V-153-1C25 | Through-hole tactile switch with lever (see Phase 1 hook switch notes) |
| Screw terminal 2-pos | 2 | Bell coil / transformer secondary | `TerminalBlock_1x02_P5.08mm` |
| Conn_01x04 | 4 | L298N control (IN1, IN2, +12V, GND) | `JST_XH_B4B-XH-A_1x04_P2.50mm_Vertical` or pin header |
| Conn_01x02 | 2 | Status LED (with 220R resistor on-board) | **JST ZH 1.5mm pitch** (`JST_ZH_B2B-ZR_1x02_P1.50mm_Vertical`) -- matches existing phone LED module |
| Conn_01x03 | 3 | SWD debug (SWDIO, GND, SWCLK) | `JST_SH_SM03B-SRSS-TB_1x03-1MP_P1.00mm_Horizontal` |
| Barrel_Jack_Switch | 2 | 12V power input | `BarrelJack_Horizontal` |

**Audio path connectors (added to simplify handset wiring):**

| Connector | Pins | Purpose | Physical footprint |
|-----------|------|---------|-------------------|
| Conn_01x04 (J8) | 4 | Handset RJ9 wires (MIC_HOT, EAR_P, EAR_N, MIC_GND) | **JST ZH 1.5mm pitch** (`JST_ZH_B4B-ZR_1x04_P1.50mm_Vertical`) -- matches phone body's RJ9 JST plug |
| C 100nF (C3) | 2 | Bypass cap between MIC_HOT and MIC_GND (ultrasonic filter) | `C_Disc_D3.0mm_W1.6mm_P2.50mm` |
| Conn_01x03 (J9) | 3 | Mic/kill switch breakout (MIC_HOT, MIC_FROM_SW, MIC_GND) | `PinHeader_1x03_P2.54mm_Vertical` |
| Conn_01x02 (J10) | 2 | Earpiece output (EAR_P, EAR_N) to Codec Zero lineout | `PinHeader_1x02_P2.54mm_Vertical` |

**Build-time wiring for audio:**
- J8: handset RJ9 JST cable plugs directly in (no splicing)
- J9 pin 1 → wire to D2F-01F COM, D2F-01F NO → wire to J9 pin 2, then TRS cable from J9 pins 2+3 → Codec Zero mic jack
- J10: short wires to Codec Zero lineout screw terminals

Notes:
- The hook switch is an on-board component (SW_Push), not an off-board connector. Wired: one pin to HOOK_SW (Pico GP10), other pin to GND.
- The LED connector is JST ZH 1.5mm pitch to match the existing phone LED module's ribbon cable plug.
- Pi header (J1) is on B.Cu (back side) -- Pi+Codec Zero stack mounts underneath, hanging toward the floor (48mm clearance).

### 3.6 -- Nets and labels

Key nets to define:

| Net name | From | To |
|----------|------|----|
| `UART_TX_PICO` | Pico GP0 (pin 1) | Pi GPIO15 (pin 10) |
| `UART_RX_PICO` | Pi GPIO14 (pin 8) | Pico GP1 (pin 2) |
| `SWD_SWDIO` | Pi GPIO22 (pin 15) | Pico SWD header pin 1 |
| `SWD_SWCLK` | Pi GPIO25 (pin 22) | Pico SWD header pin 3 |
| `HOOK_SW` | Hook switch connector | Pico GP10 (pin 14) |
| `RINGER_IN1` | Pico GP11 (pin 15) | L298N connector IN1 |
| `RINGER_IN2` | Pico GP15 (pin 20) | L298N connector IN2 |
| `LED_OUT` | Pico GP14 (pin 19) | 220R -> LED connector |
| `KP_ROW0` | Pico GP2 (pin 4) | Keypad connector pin 7 |
| `KP_ROW1` | Pico GP3 (pin 5) | Keypad connector pin 6 |
| `KP_ROW2` | Pico GP4 (pin 6) | Keypad connector pin 5 |
| `KP_ROW3` | Pico GP5 (pin 7) | Keypad connector pin 4 |
| `KP_COL0` | Pico GP6 (pin 9) | Keypad connector pin 1 |
| `KP_COL1` | Pico GP7 (pin 10) | Keypad connector pin 2 |
| `KP_COL2` | Pico GP8 (pin 11) | Keypad connector pin 3 |
| `+5V` | LM2596 output | Pico VSYS + Pi 5V |
| `+12V` | Barrel jack | LM2596 input + L298N connector |
| `GND` | Common ground | All components |

### Deliverable

A completed schematic. Export a PDF (File -> Plot) for review. Every connection will be validated against the wiring documentation in `docs/build/wiring.md`.

---

## Phase 4: Assign Footprints (DONE)

After the schematic is done, assign a physical footprint to each symbol.

| Component | KiCad Footprint |
|-----------|----------------|
| Pico H headers | `PinHeader_2x20_P2.54mm_Vertical` |
| Pi Zero header | `PinHeader_2x20_P2.54mm_Vertical` |
| LM2596 | `TO-263-5` (D2PAK) |
| 33uH inductor | Through-hole radial or SMD (depends on chosen part) |
| 680uF electrolytic cap | Through-hole radial |
| 220uF electrolytic cap | Through-hole radial |
| 1N5824 Schottky diode | `DO-201AD` (through-hole) |
| 220R resistor | `Resistor_THT:R_Axial_DIN0207_L6.3mm_D2.5mm` or `0805` SMD |
| 100nF ceramic cap | `0805` SMD or through-hole disc |
| Hook switch (SW_Push) | Through-hole tactile switch with extended lever (~8.6mm height). Position: 47.4mm, 39.4mm from board origin. |
| LED connector (JST ZH 2-pin) | `JST_ZH_B2B-ZR_1x02_P1.50mm_Vertical` (1.5mm pitch, matches existing phone LED module) |
| L298N connector (JST-XH 4-pin) | `JST_XH_B4B-XH-A_1x04_P2.50mm_Vertical` |
| SWD debug (JST-SH 3-pin) | `JST_SH_SM03B-SRSS-TB_1x03-1MP_P1.00mm_Horizontal` |
| Keypad header (1x7) | `PinHeader_1x07_P2.54mm_Vertical` |
| Bell coil screw terminal | `TerminalBlock_1x02_P5.08mm` |
| Barrel jack | `BarrelJack_Horizontal` |

Recommendation: use through-hole components wherever possible for v1. Much easier to hand-solder and debug.

---

## Phase 5: PCB Layout (DONE)

Place components and route copper traces.

### 5.1 -- Board outline

- In the PCB editor, draw the board outline on the `Edge.Cuts` layer using measurements from Phase 1.
- Add mounting holes at positions matching the phone body's standoffs.

### 5.2 -- Place components

- Import from schematic: Tools -> Update PCB from Schematic
- All components appear in a pile -- drag them into position
- General placement strategy:
  - **Pi Zero header** on one side (tallest component stack)
  - **Pico header** nearby (keep UART traces short)
  - **Power section** (barrel jack, LM2596 circuit) near the edge where the power cable enters
  - **Connectors** along board edges, oriented so cables route naturally to their destinations
  - **L298N connector** near the edge closest to the transformer

### 5.3 -- Route traces

- Route Track tool: shortcut `X`
- Signal traces: 0.25mm (10 mil) width for logic
- Power traces: 0.5-1.0mm for 5V/GND, 1.0mm+ for 12V paths
- Ground plane: add a copper fill on the back layer connected to GND (standard practice, simplifies routing)

### 5.4 -- Design rules

- Minimum clearance: 0.2mm (8 mil)
- Minimum trace width: 0.2mm
- Run DRC (Design Rule Check) frequently -- catches shorts and clearance violations

---

## Phase 6: Review and Validate (DONE)

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
