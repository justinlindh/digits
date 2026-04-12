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
| R2 | 0.1Ω 1% | 2512 | C160587 | DRV8871 current-sense resistor (I_LIMIT ≈ 2.0A) |
| C4 | 100nF 50V MLCC | 0805 | C49678 | DRV8871 VCC decoupling |
| TP1-TP6 | Test points | SMD pad | — | +5V, +12V, GND, UART_TX, UART_RX, SW_NODE |

### Components removed

| V1 Ref | Part | Reason |
|--------|------|--------|
| J5 | 1x4 pin header (L298N control) | Replaced by on-board DRV8871 (U2) |

### Components replaced: Pico H → bare RP2040

The v1 and original v2 design used a Raspberry Pi Pico H module plugged into headers on the carrier board. V2 now replaces this with a bare RP2040 chip (QFN-56) soldered directly to the board, plus a minimal support circuit. This eliminates the Pico H module, its 2x20 header footprint on the body side, and saves significant height and board area. See the **RP2040 Minimal Support Circuit** section below for full details.

### Components unchanged

- U1 (LM2596S-5.0, already SMD D2PAK)
- J1 (2x20 Pi header), J3 (barrel jack), J4 (keypad), J6 (LED), J7 (bell terminal), J8 (handset), J9 (mic kill switch), J10 (earpiece)
- SW1 (hook switch)
- All connectors remain THT -- hand-solder after PCBA
- Pico H module is **removed** -- replaced by on-board RP2040 (U3) and support circuit
- J2 (SWD connector) is **removed** -- SWD signals route directly from J1 (Pi header) to RP2040 dedicated SWDIO/SWCLK pins (24/25)

---

## Board Specifications

- **Board outline:** 76.2mm x 56.9mm (same as original Sangyn PCB)
- **Layers:** 2 (F.Cu + B.Cu)
- **Mounting holes:** 3x M3 (same positions as v1)
- **Pi stack:** J1 on B.Cu (floor side)
- **Ground plane:** B.Cu

### Power Architecture

| Rail | Source | Voltage | Supplies |
|------|--------|---------|----------|
| +12V | Barrel jack (J3) | 12V | LM2596 input (U1), DRV8871 VCC (U2) |
| +5V | LM2596 (U1) output | 5V | Pi 5V (J1 pin 2), AMS1117-3.3 (U5) input |
| +3.3V | AMS1117-3.3 (U5) output | 3.3V | RP2040 VREG_VIN, IOVDD, DVDD, USB_VDD, ADC_AVDD; W25Q16 flash (U4) VCC |
| +1.1V | RP2040 internal VREG (U3) | 1.1V | RP2040 digital core (VREG_VOUT) |

5V → AMS1117-3.3 (U5, LDO) → 3.3V for IO/USB/flash and RP2040 VREG_VIN (no inductor -- LDO not switching). 3.3V → RP2040 VREG_VIN → internal LDO → 1.1V DVDD (fully integrated, no external inductor).

**CRITICAL: VREG_VIN max is 3.3V.** The RP2040 datasheet (section 2.9.3) specifies VREG_VIN nominal range 1.8-3.3V. Do NOT connect to +5V -- this will destroy the chip. The Pico H module had its own onboard 3.3V regulator (RT6150) feeding VREG_VIN; U5 replaces that function.

**Note:** The 40-pin Pico header footprint is removed from the board. The RP2040 QFN-56 package (7x7mm) goes where the Pico module used to sit, freeing significant board area and reducing body-side height.

---

## Schematic Changes

### DRV8871 H-Bridge (U2) — replaces L298N module

The DRV8871DDAR is a 3.6A, 6.5–45V H-bridge in SOIC-8. It needs only:
- R2: current-sense resistor on ISEN pin (0.1Ω → I_LIMIT ≈ 200mV / 0.1Ω ≈ 0.9A peak)
- C4: 100nF decoupling cap on VCC

**Connections:**
| DRV8871 Pin | Net | Destination |
|-------------|-----|-------------|
| VCC (pin 1) | +12V | Barrel jack / LM2596 input |
| IN1 (pin 2) | RINGER_IN1 | RP2040 GP11 |
| IN2 (pin 3) | RINGER_IN2 | RP2040 GP15 |
| ISEN (pin 4) | — | R2 to GND |
| GND (pin 5,6) | GND | Ground plane |
| OUT1 (pin 7) | BELL_A | J7 pin 1 |
| OUT2 (pin 8) | BELL_B | J7 pin 2 |

This eliminates J5 (L298N control header), the L298N module, and its wiring.

### Updated net table

| Net | From | To |
|-----|------|----|
| `UART_TX_PI` / `UART_RX_PI` | RP2040 GP0/GP1 | Pi GPIO15/GPIO14 (crossover) |
| `SWD_SWDIO` / `SWD_SWCLK` | Pi GPIO (via J1) | RP2040 SWDIO (pin 25) / SWCLK (pin 24) |
| `HOOK_SW` | RP2040 GP10 | SW1 (other pin to GND) |
| `RINGER_IN1` / `RINGER_IN2` | RP2040 GP11/GP15 | U2 IN1/IN2 |
| `BELL_A` / `BELL_B` | U2 OUT1/OUT2 | J7 screw terminal |
| `LED_OUT` | RP2040 GP14 | R1 -> J6 |
| `KP_ROW0-3` | RP2040 GP2-5 | J4 pins 7-4 |
| `KP_COL0-2` | RP2040 GP6-8 | J4 pins 1-3 |
| `MIC_HOT` / `MIC_GND` | J8 pins 1/4 | C3, J9, R8/C23 -> U6 MIC1LP |
| `EAR_P` / `EAR_N` | U6 HPLOUT -> C24 | J10, J8 pins 2/3 |
| `CODEC_SDA` | Pi GPIO2 (J1 pin 3) | U6 SDA |
| `CODEC_SCL` | Pi GPIO3 (J1 pin 5) | U6 SCL |
| `CODEC_BCLK` | Pi GPIO18 (J1 pin 12) | U6 BCLK |
| `CODEC_WCLK` | Pi GPIO19 (J1 pin 35) | U6 WCLK |
| `CODEC_DIN` | Pi GPIO21 (J1 pin 40) | U6 DIN |
| `CODEC_DOUT` | Pi GPIO20 (J1 pin 38) | U6 DOUT |
| `CODEC_MCLK` | Pi GPIO4 (J1 pin 7) | U6 MCLK |
| `+5V` | LM2596 output | U5 input, Pi 5V (pin 2) |
| `+3.3V` | U5 (AMS1117-3.3) output | RP2040 VREG_VIN, IOVDD/DVDD/USB_VDD/ADC_AVDD, U4 flash VCC |
| `+12V` | Barrel jack | LM2596 input, U2 VCC |

---

## RP2040 Minimal Support Circuit

The bare RP2040 (U3, QFN-56) requires a small support circuit that the Pico H module previously provided on its own PCB. All components below are SMT-assembled by JLCPCB.

### Block Diagram

```
+5V (from LM2596)
  |
  +---> U5 AMS1117-3.3 ---> +3.3V rail
                               |
                               +---> IOVDD, DVDD, USB_VDD, ADC_AVDD, flash VCC
                               |
                               +---> RP2040 VREG_VIN --[internal LDO]--> VREG_VOUT (1.1V core)
```

### Components

| Ref | Part | Value | Package | JLCPCB # | Purpose |
|-----|------|-------|---------|-----------|---------|
| U3 | RP2040 | -- | QFN-56 (7x7mm) | C2040 | Microcontroller |
| U4 | W25Q16JVSSIQ | 2MB | SOIC-8 | C131025 | QSPI flash (program storage) |
| U5 | AMS1117-3.3 | 3.3V LDO | SOT-223 | C6186 | 5V to 3.3V for IO/USB/flash (needs 10uF input cap at VIN) |
| Y1 | 12MHz crystal | 12MHz | 3225 (3.2x2.5mm) | C9002 | USB and PLL clock source |
| C5, C6 | Ceramic cap | 22pF | 0402 | C1555 | Crystal load capacitors |
| ~~C7, C8~~ | ~~Ceramic cap~~ | ~~1nF~~ | ~~0402~~ | ~~C52923~~ | **Removed** -- 1nF with 27 ohm creates 5.9MHz LPF, below USB Full Speed 12MHz |
| C9, C10, C11 | Ceramic cap | 10uF 6.3V | 0603 | C109455 | VREG_VIN, VREG_VOUT, LDO output bulk |
| C12-C16 | Ceramic cap | 100nF 50V | 0805 | C49678 | Bypass: IOVDD, DVDD, USB_VDD, ADC_AVDD, flash VCC |
| R3, R4 | Resistor | 27 ohm | 0402 | C25100 | USB D+/D- series resistors |
| R5 | Resistor | 10k ohm | 0402 | C60490 | RUN pin pullup to 3.3V |
| R6 | Resistor | 10k ohm | 0402 | C60490 | QSPI_SS pullup to 3.3V |
| F1 | PTC Fuse | 1.5A | 1210 | C70102 | Resettable overcurrent protection on +12V input |

### RP2040 Pin Connections

**Power pins:**
- VREG_VIN (pin 44) -> +3.3V (from U5 AMS1117-3.3 output -- max 3.3V, do NOT connect to +5V)
- VREG_VOUT (pin 45) -> 1.1V core (to digital core power pins via trace)
- IOVDD (pins 1, 10, 22, 33, 42, 49) -> +3.3V (each with nearby 100nF bypass cap)
- DVDD (pin 23, 50) -> +3.3V (with 100nF bypass)
- USB_VDD (pin 48) -> +3.3V (with 100nF bypass)
- ADC_AVDD (pin 43) -> +3.3V (with 100nF bypass)

**QSPI flash (U4) connections:**
- QSPI_SS (pin 51) -> U4 /CS
- QSPI_SCLK (pin 52) -> U4 CLK
- QSPI_SD0-SD3 (pins 53-56) -> U4 DI, DO, /WP, /HOLD

**Crystal (Y1) connections:**
- XIN (pin 20) -> Y1 pin 1, C5 to GND
- XOUT (pin 21) -> Y1 pin 3, C6 to GND
- **Note:** Y1 (X322512MSB4SI) has 20pF load capacitance. With 22pF external caps, effective load is ~14pF (short by 6pF). Either switch to 33pF caps or use a 10pF-load crystal (e.g., ABM8-272-T3) with 15pF caps.

**USB connections:**
- USB_DM (pin 46) -> R3 (27 ohm) -> USB D-
- USB_DP (pin 47) -> R4 (27 ohm) -> USB D+
- **Note:** C7/C8 (1nF USB filter caps) were removed. 1nF with 27 ohm resistors would create a 5.9MHz low-pass filter, below USB Full Speed 12MHz, degrading signal integrity.

**Boot/reset:**
- TESTEN (pin 19) -> GND (required -- if floating, RP2040 may enter factory test mode)
- RUN (pin 26) -> R5 (10k) pullup to +3.3V
- QSPI_SS (pin 51) -> R6 (10k) pullup to +3.3V (reliable flash chip-select during power-up)
- QSPI_SS doubles as BOOTSEL -- directly usable for UF2 boot mode
- Optional: BOOTSEL button from QSPI_SS to GND via 1k series resistor (not included in BOM -- can be added if needed)

**GPIO (directly mapped from Pico pinout):**
- GP0 (pin 2) -> UART_TX_PI
- GP1 (pin 3) -> UART_RX_PI
- GP2-GP5 (pins 4-7) -> KP_ROW0-3
- GP6-GP8 (pins 8-11) -> KP_COL0-2
- GP10 (pin 14) -> HOOK_SW
- GP11 (pin 15) -> RINGER_IN1
- GP14 (pin 17) -> LED_OUT
- GP15 (pin 18) -> RINGER_IN2

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
U1, U2, U3 (RP2040), U4 (flash), U5 (3.3V LDO), U6 (codec), D1, L1, Y1, F1, C1-C27, R1-R8

### Hand-solder after delivery
J1 (B.Cu side), J3, J4, J6, J7, J8, J9, J10, SW1

### Order notes
- **PCB + Assembly:** Standard JLCPCB PCBA (economic or standard)
- **Layers:** 2
- **Generate:** Gerbers, BOM CSV (JLCPCB format), pick-and-place CPL file
- **SMD side:** Top only
- Extended parts fee applies to: U2 (DRV8871), U3 (RP2040), U4 (W25Q16), possibly L1, C1/C2
- The RP2040 (QFN-56) requires fine-pitch placement -- use JLCPCB standard assembly (not economic) for reliable QFN soldering

---

## Build Phases

- [x] Phase 1: Component selection and BOM (this document)
- [x] Phase 2: Update KiCad schematic (swap footprints, add DRV8871 circuit, RP2040 support circuit)
- [ ] Phase 2.5: Add audio codec circuit (TLV320AIC3104)
- [x] Phase 3: Update PCB layout (place SMD components, route traces, GND pour)
- [x] Phase 4: DRC validation (0 errors, 0 unconnected pads)
- [ ] Phase 5: Generate JLCPCB production files (Gerber + BOM + CPL via Fabrication Toolkit)
- [ ] Phase 6: Order from JLCPCB
- [ ] Phase 7: Hand-solder THT connectors
- [ ] Phase 8: Test

---

## Migration Notes from Pico H

### GPIO mapping is unchanged

The RP2040 GPIO numbers (GP0-GP29) are identical whether accessed through a Pico H module or a bare RP2040 chip. All firmware C code that references `GPIO_PIN` numbers requires zero changes. The pin numbers in the firmware map to RP2040 GPIO numbers, not physical package pins -- KiCad handles the QFN-56 physical pin mapping.

### USB programming

The bare RP2040 uses the same UF2 bootloader approach as the Pico H:
- Hold BOOTSEL (QSPI_SS) low during reset to enter USB mass storage boot mode
- Drag-and-drop `.uf2` firmware file, same as before
- The `scripts/flash.sh` script works without modification

On the bare RP2040, the BOOTSEL function is on the QSPI_SS pin. If no physical BOOTSEL button is populated, entering boot mode requires briefly shorting QSPI_SS to GND while toggling RUN (or power cycling). A BOOTSEL button footprint can be added to the board for convenience.

**USB access required:** UF2/BOOTSEL mode requires physical USB D+/D- connections. The board needs either a USB micro/C connector, test pads, or pogo pin pads for USB_DM and USB_DP. Without this, flashing is SWD-only via the Pi. For production units where the Pi handles all firmware updates over SWD, USB pads may be sufficient (no connector needed).

### SWD debug

J2 (SWD debug connector) has been removed. SWD signals now route directly from J1 (Pi header) to the RP2040's dedicated SWDIO (pin 25) and SWCLK (pin 24) pins. No physical connector is needed since both chips are on the same board. The Pi still drives SWD via GPIO for probe-rs flashing, same as v1.

### Board layout impact

- The 40-pin 2x20 Pico header footprint (used for the plug-in Pico H module) is **removed** from the board
- The RP2040 QFN-56 package is 7x7mm -- dramatically smaller than the Pico H module (51x21mm)
- The RP2040 and its support circuit (flash, crystal, LDO, caps) fit in roughly the same area the Pico header occupied
- Body-side height is reduced significantly -- the Pico H (with headers) was the tallest component on the body side

### Cost comparison

| Item | Pico H approach | Bare RP2040 approach |
|------|----------------|---------------------|
| Microcontroller | Pico H module (~$5) | RP2040 chip (~$0.70) |
| Flash | Included on Pico | W25Q16 (~$0.30) |
| Crystal | Included on Pico | 12MHz 3225 (~$0.10) |
| 3.3V regulation | Included on Pico | AMS1117-3.3 (~$0.15) |
| Passives | None | ~$0.40 total |
| Assembly | Hand-solder headers | JLCPCB SMT (included in assembly fee) |
| **Total** | **~$5 + hand labor** | **~$1.65 + assembly fee** |

---

## TLV320AIC3104 Audio Codec Circuit

Replaces the Raspberry Pi Codec Zero HAT (DA7212). The codec connects to the Pi via I2S (GPIO18-21) and I2C (GPIO2/3) through J1. GPCLK0 (GPIO4) is wired to MCLK as a fallback, but the default configuration uses PLL-from-BCLK.

### Components

| Ref | Part | Value | Package | JLCPCB # | Purpose |
|-----|------|-------|---------|-----------|---------|
| U6 | TLV320AIC3104IRHBR | -- | QFN-32 (5x5mm) | C181753 | Audio codec |
| C17 | MLCC | 10uF | 0402 | C15525 | AVDD decoupling |
| C18 | MLCC | 100nF | 0805 | C49678 | DRVDD decoupling (pin 10) |
| C19 | MLCC | 10uF | 0402 | C15525 | DRVDD decoupling (pin 16) |
| C20 | MLCC | 100nF | 0805 | C49678 | IOVDD decoupling |
| C21 | MLCC | 100nF | 0805 | C49678 | AVDD additional decoupling |
| C22 | MLCC | 10uF | 0402 | C15525 | AVSS analog ground bulk |
| C23 | MLCC | 1uF | 0402 | C52923 | Mic input AC coupling |
| C24 | MLCC | 10uF | 0402 | C15525 | Earpiece output AC coupling |
| C25 | MLCC | 10uF | 0402 | C15525 | DVDD decoupling (internal 1.8V LDO output) |
| R7 | Resistor | 10k | 0402 | C60490 | RESET pullup to +3V3 |
| R8 | Resistor | 2.2k | 0402 | C25879 | MICBIAS series resistor for electret mic |
| C26 | MLCC | 470nF | 0402 | C47339 | Unused analog inputs to GND (noise suppression) |
| C27 | MLCC | 1nF | 0402 | C14442 | RESET pin ESD protection cap |

### Pin Connections

**Power:**
- AVDD (pin 17) -> +3.3V (C17 + C21 decoupling)
- DRVDD (pin 10, 16) -> +3.3V (C18 + C19 decoupling)
- IOVDD (pin 31) -> +3.3V (C20 decoupling)
- DVDD (pin 24) -> C25 to GND only. **DO NOT connect to +3.3V.** This is the internal 1.8V LDO output.
- AVSS1 (pin 9), AVSS2 (pin 18), DVSS (pin 30), DRVSS (pin 13), GND/EP (pin 33) -> GND
- RESET (pin 23) -> R7 (10k) -> +3.3V

**Digital (via J1):**

| Pi GPIO | J1 Pin | Function | U6 Pin |
|---------|--------|----------|--------|
| GPIO2 | 3 | I2C1 SDA | SDA (pin 1) |
| GPIO3 | 5 | I2C1 SCL | SCL (pin 32) |
| GPIO18 | 12 | I2S BCLK | BCLK (pin 26) |
| GPIO19 | 35 | I2S LRCLK | WCLK (pin 27) |
| GPIO21 | 40 | I2S DOUT (Pi TX) | DIN (pin 28) |
| GPIO20 | 38 | I2S DIN (Pi RX) | DOUT (pin 29) |
| GPIO4 | 7 | GPCLK0 | MCLK (pin 25) |

**Audio:**
- Mic: MICBIAS (pin 7) -> R8 (2.2k) -> mic bias point; MIC_FROM_SW (from J9) -> C23 (1uF, AC coupling) -> MIC1LP (pin 2). MIC1LM (pin 3) to GND (single-ended).
- Earpiece: HPLOUT (pin 11) -> C24 (10uF, AC coupling) -> EAR_P. HPLCOM (pin 12) no-connect (power down via registers).
- Unused inputs: MIC1RP, MIC1RM, MIC2L, MIC2R -> tied together via C26 (470nF) to GND (prevents noise coupling per TI recommendation).
- Unused outputs: HPROUT, HPRCOM, LEFT_LOP/LOM, RIGHT_LOP/LOM -- no-connect flags.

**Control:**
- I2C address is fixed at 0x18 (no ADDR pin on TLV320AIC3104).
- ~{RESET} (pin 23) -> R7 (10k) pullup to +3V3, C27 (1nF) to GND for ESD protection.

---

## Reference

- V1 design: `../v1/PLAN.md`
- V1 BOM: `../v1/BOM.csv`
- Wiring spec: `docs/build/wiring.md`
- UART protocol: `docs/architecture/uart-protocol.md`
- RP2040 datasheet: https://datasheets.raspberrypi.com/rp2040/rp2040-datasheet.pdf
- RP2040 hardware design guide: https://datasheets.raspberrypi.com/rp2040/hardware-design-with-rp2040.pdf
- DRV8871 datasheet: https://www.ti.com/lit/ds/symlink/drv8871.pdf
- LM2596 datasheet: https://www.ti.com/lit/ds/symlink/lm2596.pdf
- AMS1117-3.3 datasheet: http://www.advanced-monolithic.com/pdf/ds1117.pdf
- TLV320AIC3104 datasheet: https://www.ti.com/lit/ds/symlink/tlv320aic3104.pdf
- Codec wiring reference: `CODEC-WIRING-REFERENCE.md`

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
| R2 | Device:R | Resistor_SMD:R_2512_6332Metric | C160587 | [Link](https://jlcpcb.com/partdetail/C160587) | 32 ⚠️ |
| C4 | Device:C | Capacitor_SMD:C_0805_2012Metric | C49678 | (same as C3) | 13,893,007 |
| U3 | MCU_RaspberryPi:RP2040 | Package_DFN_QFN:QFN-56-1EP_7x7mm_P0.4mm_EP3.2x3.2mm | C2040 | [Link](https://jlcpcb.com/partdetail/C2040) | Extended |
| U4 | Memory_Flash:W25Q16JV | Package_SO:SOIC-8_3.9x4.9mm_P1.27mm | C131025 | [Link](https://jlcpcb.com/partdetail/C131025) | Extended |
| U5 | Regulator_Linear:AMS1117-3.3 | Package_TO_SOT_SMD:SOT-223-3_TabPin2 | C6186 | [Link](https://jlcpcb.com/partdetail/C6186) | Basic |
| Y1 | Device:Crystal | Crystal_SMD_3225-4Pin_3.2x2.5mm | C9002 | [Link](https://jlcpcb.com/partdetail/C9002) | Basic |
| C5,C6 | Device:C | Capacitor_SMD:C_0402_1005Metric | C1555 | [Link](https://jlcpcb.com/partdetail/C1555) | Basic |
| ~~C7,C8~~ | ~~Device:C~~ | ~~Capacitor_SMD:C_0402_1005Metric~~ | ~~C52923~~ | **Removed** | -- |
| C9-C11 | Device:C | Capacitor_SMD:C_0603_1608Metric | C109455 | [Link](https://jlcpcb.com/partdetail/C109455) | Basic |
| C12-C16 | Device:C | Capacitor_SMD:C_0805_2012Metric | C49678 | (same as C3) | Basic |
| R3,R4 | Device:R | Resistor_SMD:R_0402_1005Metric | C25100 | [Link](https://jlcpcb.com/partdetail/C25100) | Basic |
| R5 | Device:R | Resistor_SMD:R_0402_1005Metric | C60490 | [Link](https://jlcpcb.com/partdetail/C60490) | Basic |
| R6 | Device:R | Resistor_SMD:R_0402_1005Metric | C60490 | (same as R5) | Basic |
| F1 | Device:Polyfuse | Fuse:Fuse_1210_3225Metric | C70102 | [Link](https://jlcpcb.com/partdetail/C70102) | Basic |
| U6 | TLV320AIC3104IRHBR | Package_DFN_QFN:QFN-32-1EP_5x5mm_P0.5mm_EP3.5x3.5mm | C181753 | [Link](https://jlcpcb.com/partdetail/C181753) | Extended |
| C17,C19,C22,C24,C25 | Device:C | Capacitor_SMD:C_0402_1005Metric | C15525 | [Link](https://jlcpcb.com/partdetail/C15525) | Basic |
| C18,C20,C21 | Device:C | Capacitor_SMD:C_0805_2012Metric | C49678 | (same as C3) | Basic |
| C23 | Device:C | Capacitor_SMD:C_0402_1005Metric | C52923 | [Link](https://jlcpcb.com/partdetail/C52923) | Basic |
| R7 | Device:R | Resistor_SMD:R_0402_1005Metric | C60490 | (same as R5) | Basic |
| R8 | Device:R | Resistor_SMD:R_0402_1005Metric | C25879 | [Link](https://jlcpcb.com/partdetail/C25879) | Basic |
| C26 | Device:C | Capacitor_SMD:C_0402_1005Metric | C47339 | [Link](https://jlcpcb.com/partdetail/C47339) | Basic |
| C27 | Device:C | Capacitor_SMD:C_0402_1005Metric | C14442 | [Link](https://jlcpcb.com/partdetail/C14442) | Basic |

### ⚠️ Low Stock Alert

**R2 (C160587)** — Only 32 in stock. Consider alternatives:
- Order early before stock depletes
- Search for alternative 100mΩ 2512 resistors via the JLCPCB Tools plugin
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
The DRV8871 current limit (2.0A with R2=0.1Ω) may reduce bell volume compared to v1's L298N (no current limiting). If bell is too quiet on first assembled board:
- Replace R2 with 0.1Ω → I_LIMIT = 2.0A
- DRV8871 is rated for 3.6A peak, so this is safe

### DRV8871 Sleep Mode Behavior
When IN1=IN2=0 (silent window), DRV8871 enters sleep mode. 50µs wake-up latency on first toggle — 0.2% of the 25ms half-period. Completely imperceptible. No firmware change needed.
