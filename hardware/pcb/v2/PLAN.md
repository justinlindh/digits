# Digits Carrier Board V2 — JLCPCB Assembly-Ready

Goal: revise the v1 carrier board to maximize JLCPCB SMT assembly. Replace through-hole passives and the external L298N H-bridge module with SMD equivalents. Connectors and mechanical parts remain THT (hand-soldered after PCBA delivery).

---

## V1 → V2 Changes

### Components replaced (THT → SMD)

| Ref | V1 Part | V1 Package | V2 Part | V2 Package | JLCPCB # | Reason |
|-----|---------|------------|---------|------------|-----------|--------|
| D1 | SB540 | DO-201AD (THT) | SS54 | SMA (DO-214AC) | C22452 | SMD Schottky, same 5A/40V spec |
| L1 | Wurth 33uH radial | 10mm THT | CDRH127 33uH (shielded) | 12.3x12.3mm SMD | C9400 | Shielded SMD power inductor, 33uH 3A |
| C1 | Panasonic 680uF 25V | Radial THT | DMBJ RVT1E681M1010 | SMD 10x10mm | C976031 | SMD aluminum electrolytic |
| C2 | Nichicon 220uF 25V | Radial THT | DMBJ RVT1E221M0811 | SMD 8x10mm | C2895286 | SMD aluminum electrolytic |
| C3 | Vishay 100nF disc | THT | Samsung CL21B104KBCNNNC | 0805 | C49678 | Standard 0805 MLCC |
| R1 | Yageo 220Ω 1/4W | Axial THT | Yageo RC0805FR-07220RL | 0805 | C17557 | Standard 0805 resistor |

### Components added

| Ref | Part | Package | JLCPCB # | Purpose |
|-----|------|---------|-----------|---------|
| U2 | DRV8871DDAR | SOIC-8 | C75864 | On-board H-bridge, replaces external L298N module |
| R2 | 0.22Ω 1% | 2512 | C146886 | DRV8871 current-sense resistor (I_LIMIT ≈ 0.91A) |
| C4 | 100nF 50V MLCC | 0805 | C49678 | DRV8871 VCC decoupling |
| TP1-TP6 | Test points | SMD pad | — | +5V, +12V, GND, UART_TX, UART_RX, SW_NODE |

### Components removed

| V1 Ref | Part | Reason |
|--------|------|--------|
| J5 | 1x4 pin header (L298N control) | Replaced by on-board DRV8871 (U2) |

### Components unchanged

- U1 (LM2596S-5.0, already SMD D2PAK)
- J1 (2x20 Pi header), J2 (SWD), J3 (barrel jack), J4 (keypad), J6 (LED), J7 (bell terminal), J8 (handset), J9 (mic kill switch), J10 (earpiece)
- SW1 (hook switch)
- All connectors remain THT — hand-solder after PCBA

---

## Board Specifications

(Unchanged from v1 — same board outline, mounting holes, clearances.)

- **Board outline:** 76.2mm × 56.9mm (same as original Sangyn PCB)
- **Layers:** 2 (F.Cu + B.Cu)
- **Mounting holes:** 3× M3 (same positions as v1)
- **Pi stack:** J1 on B.Cu (floor side)
- **Ground plane:** B.Cu

---

## Schematic Changes

### DRV8871 H-Bridge (U2) — replaces L298N module

The DRV8871DDAR is a 3.6A, 6.5–45V H-bridge in SOIC-8. It needs only:
- R2: current-sense resistor on ISEN pin (0.22Ω → I_LIMIT ≈ 200mV / 0.22Ω ≈ 0.9A peak)
- C4: 100nF decoupling cap on VCC

**Connections:**
| DRV8871 Pin | Net | Destination |
|-------------|-----|-------------|
| VCC (pin 1) | +12V | Barrel jack / LM2596 input |
| IN1 (pin 2) | RINGER_IN1 | Pico GP11 |
| IN2 (pin 3) | RINGER_IN2 | Pico GP15 |
| ISEN (pin 4) | — | R2 to GND |
| GND (pin 5,6) | GND | Ground plane |
| OUT1 (pin 7) | BELL_A | J7 pin 1 |
| OUT2 (pin 8) | BELL_B | J7 pin 2 |

This eliminates J5 (L298N control header), the L298N module, and its wiring.

### Updated net table

| Net | From | To |
|-----|------|----|
| `UART_TX_PI` / `UART_RX_PI` | Pico GP0/GP1 | Pi GPIO15/GPIO14 (crossover) |
| `SWD_SWDIO` / `SWD_SWCLK` | Pi GPIO22/GPIO25 | J2 SWD connector |
| `HOOK_SW` | Pico GP10 | SW1 (other pin to GND) |
| `RINGER_IN1` / `RINGER_IN2` | Pico GP11/GP15 | U2 IN1/IN2 |
| `BELL_A` / `BELL_B` | U2 OUT1/OUT2 | J7 screw terminal |
| `LED_OUT` | Pico GP14 | R1 → J6 |
| `KP_ROW0-3` | Pico GP2-5 | J4 pins 7-4 |
| `KP_COL0-2` | Pico GP6-8 | J4 pins 1-3 |
| `MIC_HOT` / `MIC_GND` | J8 pins 1/4 | C3, J9 pins 1/3 |
| `EAR_P` / `EAR_N` | J8 pins 2/3 | J10 pins 1/2 |
| `+5V` | LM2596 output | Pico VSYS, Pi 5V (pin 2) |
| `+12V` | Barrel jack | LM2596 input, U2 VCC |

### Test points

Exposed SMD pads (no cost, zero BOM):
- TP1: +5V
- TP2: +12V
- TP3: GND
- TP4: UART_TX_PI
- TP5: UART_RX_PI
- TP6: LM2596 switching node (L1/U1 junction)

---

## PCB Layout Notes

- DRV8871 (U2) + R2 + C4 placed where L298N header (J5) used to be
- SMD passives (R1, C3, C4) on F.Cu near their associated ICs
- SMD caps C1, C2 near LM2596 (same positions as THT, just SMD pads)
- L1 SMD footprint replaces THT inductor footprint (check clearance — 6mm vs 10mm)
- Test point pads along board edge for easy probe access
- Keep ground plane on B.Cu intact

---

## JLCPCB Assembly Strategy

### SMT-assembled (top side, JLCPCB does this)
U1, U2, D1, L1, C1, C2, C3, C4, R1, R2

### Hand-solder after delivery
J1 (B.Cu side), J2, J3, J4, J6, J7, J8, J9, J10, SW1

### Order notes
- **PCB + Assembly:** Standard JLCPCB PCBA (economic or standard)
- **Layers:** 2
- **Generate:** Gerbers, BOM CSV (JLCPCB format), pick-and-place CPL file
- **SMD side:** Top only
- Extended parts fee applies to: U2 (DRV8871), possibly L1 and C1/C2

---

## Build Phases

- [x] Phase 1: Component selection and BOM (this document)
- [ ] Phase 2: Update KiCad schematic (swap footprints, add DRV8871 circuit)
- [ ] Phase 3: Update PCB layout (place new SMD components, re-route)
- [ ] Phase 4: DRC validation
- [ ] Phase 5: Generate JLCPCB production files (Gerber + BOM + CPL)
- [ ] Phase 6: Order from JLCPCB
- [ ] Phase 7: Hand-solder THT connectors
- [ ] Phase 8: Test

---

## Reference

- V1 design: `../v1/PLAN.md`
- V1 BOM: `../v1/BOM.csv`
- Wiring spec: `docs/build/wiring.md`
- UART protocol: `docs/architecture/uart-protocol.md`
- DRV8871 datasheet: https://www.ti.com/lit/ds/symlink/drv8871.pdf
- LM2596 datasheet: https://www.ti.com/lit/ds/symlink/lm2596.pdf

---

## KiCad ↔ JLCPCB Part Mapping

### Setup: Install Bouni's kicad-jlcpcb-tools plugin

This plugin lets you search JLCPCB's parts database directly from KiCad's PCB editor, assign LCSC part numbers to footprints, and generate BOM + CPL files in JLCPCB's required format.

**Install via KiCad Plugin and Content Manager:**
1. Open KiCad → Plugin and Content Manager
2. Add custom repository: `https://raw.githubusercontent.com/Bouni/bouni-kicad-repository/main/repository.json`
3. Install "JLCPCB Tools"
4. Access from PCB Editor → Tools → External Plugins → JLCPCB Tools

**Alternative: JLCPCB KiCad Library (CDFER)**
Pre-built symbols + footprints with LCSC numbers already assigned:
1. Add repo: `https://raw.githubusercontent.com/CDFER/cd_fer-kicad-repository/main/repository.json`
2. Install via Plugin and Content Manager
3. Use these symbols directly in schematic — LCSC numbers auto-populate

### Direct Component Mapping Table

Use this to assign LCSC part numbers in KiCad (via symbol field `LCSC` or using the JLCPCB Tools plugin):

| Ref | KiCad Symbol | KiCad Footprint | LCSC Part # | JLCPCB Link | Verified Stock |
|-----|-------------|-----------------|-------------|-------------|----------------|
| U1 | Regulator_Switching:LM2596S-5 | Package_TO_SOT_SMD:TO-263-5 | C347421 | [Link](https://jlcpcb.com/partdetail/C347421) | 99,717 |
| D1 | Diode:SS54 | Diode_SMD:D_SMA | C22452 | [Link](https://jlcpcb.com/partdetail/C22452) | 1,470,233 |
| L1 | Inductor_SMD:33uH | Inductor_SMD:L_12x12mm_H8mm | C9400 | [Link](https://jlcpcb.com/partdetail/C9400) | 44,740 |
| C1 | Device:CP1 | Capacitor_SMD:CP_Elec_10x10.5 | C976031 | [Link](https://jlcpcb.com/partdetail/C976031) | 12,651 |
| C2 | Device:CP1 | Capacitor_SMD:CP_Elec_8x10.5 | C2895286 | [Link](https://jlcpcb.com/partdetail/C2895286) | 2,864 |
| C3 | Device:C | Capacitor_SMD:C_0805_2012Metric | C49678 | [Link](https://jlcpcb.com/partdetail/C49678) | 13,893,007 |
| R1 | Device:R | Resistor_SMD:R_0805_2012Metric | C17557 | [Link](https://jlcpcb.com/partdetail/C17557) | 864,653 |
| U2 | Motor_Driver:DRV8871 | Package_SO:SOIC-8-1EP_3.9x4.9mm_P1.27mm_EP2.29x3mm | C75864 | [Link](https://jlcpcb.com/partdetail/C75864) | 6,376 |
| R2 | Device:R | Resistor_SMD:R_2512_6332Metric | C146886 | [Link](https://jlcpcb.com/partdetail/C146886) | 32 ⚠️ |
| C4 | Device:C | Capacitor_SMD:C_0805_2012Metric | C49678 | (same as C3) | 13,893,007 |

### ⚠️ Low Stock Alert

**R2 (C146886)** — Only 32 in stock. Consider alternatives:
- Order early before stock depletes
- Search for alternative 220mΩ 2512 resistors via the JLCPCB Tools plugin
- If out of stock at order time, this one part can be hand-soldered (2512 is large enough)

### Generating JLCPCB Production Files

With the plugin installed:
1. Open PCB in KiCad PCB Editor
2. Tools → External Plugins → JLCPCB Tools
3. Assign LCSC numbers (see table above) to each footprint
4. Click "Generate Fabrication Data"
5. Output: `jlcpcb/production_files/` with:
   - Gerber ZIP
   - BOM CSV (JLCPCB format)
   - CPL/pick-and-place file
6. Upload all three to JLCPCB order page

---

## Independent Review Notes (2026-04-07)

### DRV8871 VCC Decoupling Layout Requirement
U2 (DRV8871) VCC must have a low-impedance PCB trace to C1 (680uF on +12V rail) for transformer inductive kickback absorption. C4 (100nF) alone is insufficient. If layout requires long trace to C1, add a local 10uF MLCC near U2 VCC.

### Bell Volume — First Board Test
The DRV8871 current limit (0.91A with R2=0.22Ω) may reduce bell volume compared to v1's L298N (no current limiting). If bell is too quiet on first assembled board:
- Replace R2 with 0.1Ω → I_LIMIT = 2.0A
- DRV8871 is rated for 3.6A peak, so this is safe

### DRV8871 Sleep Mode Behavior
When IN1=IN2=0 (silent window), DRV8871 enters sleep mode. 50µs wake-up latency on first toggle — 0.2% of the 25ms half-period. Completely imperceptible. No firmware change needed.
