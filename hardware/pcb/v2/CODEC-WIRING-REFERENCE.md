# TLV320AIC3104 Codec Wiring Reference

This is the definitive reference for wiring U6 and its passives in KiCad. Use this when wiring the schematic by hand.

---

## Components Added for Codec

| Ref | Value | Footprint | LCSC | Purpose |
|-----|-------|-----------|------|---------|
| U6 | TLV320AIC3104IRHBR | Texas_RHB0032E VQFN-32 | C181753 | Audio codec |
| R7 | 10k | 0402 | C60490 | RESET pullup to +3V3 |
| R8 | 2.2k | 0402 | C25879 | MICBIAS series resistor for electret mic |
| C17 | 10uF | 0402 or 0805 | C15525 | AVDD decoupling |
| C18 | 100nF | 0805 | C49678 | DRVDD decoupling (pin 10 side) |
| C19 | 10uF | 0402 or 0805 | C15525 | DRVDD decoupling (pin 16 side) |
| C20 | 100nF | 0805 | C49678 | IOVDD decoupling |
| C21 | 100nF | 0805 | C49678 | AVDD additional decoupling |
| C22 | 10uF | 0402 or 0805 | C15525 | AVSS/analog ground bulk |
| C23 | 1uF | 0402 | C52923 | AC coupling, mic input (MIC1LP) |
| C24 | 10uF | 0402 | C15525 | AC coupling, earpiece output (HPLOUT) |
| C25 | 10uF | 0402 | C15525 | DVDD decoupling (internal LDO output) |
| C26 | 470nF | 0402 | C47339 | Unused analog inputs to GND (noise suppression) |
| C27 | 1nF | 0402 | C14442 | RESET pin ESD protection cap |

---

## U6 Pin-by-Pin Wiring

### I2S Bus (connect to Pi header J1)

| Pin | Name | Connect To | Net Label | Notes |
|-----|------|-----------|-----------|-------|
| 25 | MCLK | J1 pin 7 (GPIO4/GPCLK0) | CODEC_MCLK | Optional; can use PLL-from-BCLK instead |
| 26 | BCLK | J1 pin 12 (GPIO18) | CODEC_BCLK | I2S bit clock |
| 27 | WCLK | J1 pin 35 (GPIO19) | CODEC_WCLK | I2S word clock (LRCLK) |
| 28 | DIN | J1 pin 40 (GPIO21) | CODEC_DIN | Pi TX to codec RX |
| 29 | DOUT | J1 pin 38 (GPIO20) | CODEC_DOUT | Codec TX to Pi RX |

### I2C Control (connect to Pi header J1)

| Pin | Name | Connect To | Net Label | Notes |
|-----|------|-----------|-----------|-------|
| 32 | SCL | J1 pin 5 (GPIO3/I2C1_SCL) | CODEC_SCL | |
| 1 | SDA | J1 pin 3 (GPIO2/I2C1_SDA) | CODEC_SDA | |

### Microphone Input

| Pin | Name | Connect To | Net Label | Notes |
|-----|------|-----------|-----------|-------|
| 2 | MIC1LP | C23 pin 1 | (internal) | AC-coupled from mic; C23 other end to MIC_FROM_SW |
| 3 | MIC1LM | GND | GND | Single-ended mic, tie inverting input to ground |
| 7 | MICBIAS | R8 pin 1 | MICBIAS_OUT | R8 other end provides bias to electret mic |
| 4 | MIC1RP | C26 to GND | CODEC_UNUSED_IN | Tied together with other unused inputs via C26 (470nF) to GND |
| 5 | MIC1RM | C26 to GND | CODEC_UNUSED_IN | Per TI: unused inputs must be grounded via cap to prevent noise coupling |
| 6 | MIC2L | C26 to GND | CODEC_UNUSED_IN | |
| 8 | MIC2R | C26 to GND | CODEC_UNUSED_IN | |

### Audio Output (Earpiece)

| Pin | Name | Connect To | Net Label | Notes |
|-----|------|-----------|-----------|-------|
| 11 | HPLOUT | C24 pin 1 | (internal) | AC-coupled to earpiece; C24 other end to EAR_P |
| 12 | HPLCOM | no connect | -- | Power down via registers. Do NOT tie to GND (causes thermal issues per TI E2E) |
| 14 | HPRCOM | no connect | -- | Power down via registers. Do NOT tie to GND |
| 15 | HPROUT | no connect | -- | Place X (mono earpiece, right channel unused) |
| 19 | LEFT_LOP | no connect | -- | Place X (line out unused) |
| 20 | LEFT_LOM | no connect | -- | Place X |
| 21 | RIGHT_LOP | no connect | -- | Place X |
| 22 | RIGHT_LOM | no connect | -- | Place X |

### Power Supply

| Pin | Name | Connect To | Net Label | Notes |
|-----|------|-----------|-----------|-------|
| 17 | AVDD | +3V3 via C17 | +3V3 | 10uF decoupling to GND |
| 10 | DRVDD | +3V3 via C18 | +3V3 | 100nF decoupling to GND |
| 16 | DRVDD | +3V3 via C19 | +3V3 | 10uF decoupling to GND |
| 31 | IOVDD | +3V3 via C20 | +3V3 | 100nF decoupling to GND |
| 24 | DVDD | C25 to GND only | CODEC_DVDD | **DO NOT connect to +3V3.** Internal LDO output, 1.8V. Decoupling cap only. |

### Ground

| Pin | Name | Connect To | Net Label |
|-----|------|-----------|-----------|
| 9 | AVSS1 | GND | GND |
| 18 | AVSS2 | GND | GND |
| 30 | DVSS | GND | GND |
| 13 | DRVSS | GND | GND |
| 33 | GND (EP) | GND | GND |

### Control

| Pin | Name | Connect To | Net Label | Notes |
|-----|------|-----------|-----------|-------|
| 23 | ~{RESET} | R7 to +3V3 | (internal) | Active-low reset; R7 (10k) pullup to +3V3 |

---

## Passive Wiring Detail

### Decoupling Caps (each cap: pin 1 to power rail, pin 2 to GND)

| Cap | Pin 1 connects to | Pin 2 connects to |
|-----|-------------------|-------------------|
| C17 | AVDD (U6 pin 17), net +3V3 | GND |
| C18 | DRVDD (U6 pin 10), net +3V3 | GND |
| C19 | DRVDD (U6 pin 16), net +3V3 | GND |
| C20 | IOVDD (U6 pin 31), net +3V3 | GND |
| C21 | AVDD (U6 pin 17), net +3V3 | GND |
| C22 | AVSS bulk, net GND | GND (both sides) |
| C25 | DVDD (U6 pin 24), net CODEC_DVDD | GND |

### AC Coupling Caps

| Cap | Pin 1 connects to | Pin 2 connects to |
|-----|-------------------|-------------------|
| C23 | MIC1LP (U6 pin 2) | MIC_FROM_SW net (from J9 mic switch) |
| C24 | HPLOUT (U6 pin 11) | EAR_P net (to J10 earpiece) |

### Resistors

| Res | Pin 1 connects to | Pin 2 connects to |
|-----|-------------------|-------------------|
| R7 | +3V3 | ~{RESET} (U6 pin 23) |
| R8 | MICBIAS_OUT (from U6 pin 7) | Mic bias point (connects to mic circuit) |

---

## J1 (Pi Header) Pin Usage for Codec

These J1 pins need new connections for the codec. They were previously unused.

| J1 Pin | Pi GPIO | Net Label | Connects To |
|--------|---------|-----------|-------------|
| 3 | GPIO2 (SDA1) | CODEC_SDA | U6 pin 1 |
| 5 | GPIO3 (SCL1) | CODEC_SCL | U6 pin 32 |
| 7 | GPIO4 (GPCLK0) | CODEC_MCLK | U6 pin 25 |
| 12 | GPIO18 (PCM_CLK) | CODEC_BCLK | U6 pin 26 |
| 35 | GPIO19 (PCM_FS) | CODEC_WCLK | U6 pin 27 |
| 38 | GPIO20 (PCM_DIN) | CODEC_DOUT | U6 pin 29 |
| 40 | GPIO21 (PCM_DOUT) | CODEC_DIN | U6 pin 28 |

---

## What Existed Before (V2 without codec)

The following nets and components existed and were routed on the V2 PCB before the codec was added. They should NOT be changed:

- Power: +12V, +5V, +3V3, GND rails and all associated caps/regulators
- U1 (LM2596S-5), U2 (DRV8871), U3 (RP2040), U4 (W25Q16), U5 (AMS1117-3.3)
- All connectors: J1, J3, J4, J6, J7, J8, J9, J10
- All existing passives: C1-C16, R1-R6, F1, Y1, D1, L1, SW1
- All existing net labels for GPIO signals, UART, SWD, keypad, etc.

---

## Design Notes

- HPLCOM/HPRCOM are no-connect (not GND) to avoid thermal issues per TI E2E guidance. Power down these blocks via register writes.
- Unused analog inputs (MIC1RP, MIC1RM, MIC2L, MIC2R) are tied together via C26 (470nF) to GND per TI recommendation to prevent noise coupling.
- C27 (1nF) provides ESD protection on the RESET pin.
- DVDD is the internal 1.8V LDO output. Never connect to +3V3.
- I2C address is fixed at 0x18 (no ADDR pin on TLV320AIC3104).
