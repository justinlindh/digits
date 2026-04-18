# PCB v2 — net topology reference

Authoritative description of how every IC, connector, and passive on the digits carrier board (`hardware/pcb/v2/kicad/digits-pcb.kicad_sch`) is wired. Use this as the source of truth when debugging connectivity, when spec'ing a new revision, or when auditing whether a schematic change stayed faithful to the design intent.

## What this board does

The digits v2 PCB is a carrier that sits under a Raspberry Pi Zero 2 W. It:

- Takes +12 V in from a barrel jack, runs it through a 1.5 A polyfuse, steps it down to +5 V with an LM2596S buck converter, and drops to +3V3 via an AMS1117 LDO. The +5 V rail feeds the Pi via the 40-pin header.
- Hosts a TLV320AIC3104 stereo audio codec that replaces the Pi Codec Zero HAT in the v1 prototype. The codec talks to the Pi over I²C (control) and I²S (audio), drives the handset earpiece in capless BTL mode, and receives the handset mic through a DC-blocking cap after an external kill-switch.
- Hosts an RP2040 microcontroller with a W25Q16 SPI flash. The RP2040 handles the keypad matrix, the hookswitch, a status LED, and the bell ringer (driven through a DRV8871 H-bridge into the telephone's bell coil). The RP2040 talks to the Pi over UART and is programmed via SWD from a Pi GPIO pair.
- Provides mechanical mounting through three M3 holes at positions locked by the phone enclosure.

## Components

All component references, values, footprints, and placements are fixed by the phone enclosure and the existing PCB. Changing any of them requires a board respin.

| Ref | Value | Purpose | Footprint |
|---|---|---|---|
| `U1` | LM2596S-5 | +12V → +5V buck | TO-263-5_TabPin3 |
| `U2` | DRV8871DDAR | Bell ringer H-bridge | HTSOP-8-1EP |
| `U3` | RP2040 | Keypad/hookswitch/ringer/UART MCU | QFN-56-1EP |
| `U4` | W25Q16JVSNIQ | RP2040 QSPI boot flash | SOIC-8 narrow |
| `U5` | AMS1117-3.3 | +5V → +3V3 LDO | SOT-223-3 |
| `U6` | TLV320AIC3104IRHBR | Stereo audio codec | VQFN-32 5×5 mm |
| `Y1` | ABM8-272-T3 (12 MHz) | RP2040 crystal, per RP2040 DS §2.16.1.1 | Crystal_SMD_3225-4Pin |
| `J1` | Conn_02x20_Odd_Even | Raspberry Pi 40-pin header | PinHeader 2×20 2.54 mm |
| `J3` | Barrel_Jack_Switch | +12 V power input | BarrelJack_Horizontal |
| `J4` | Conn_01x07 | Keypad flex ribbon | JST ZH B7B-ZR-SM4-TF |
| `J6` | Conn_01x02 | Status LED | JST ZH B2B-ZR-SM4-TF |
| `J7` | Screw_Terminal_01x02 | Bell coil | Phoenix PT-1,5-2-5.0 |
| `J8` | Conn_01x04 | Handset (mic + earpiece) | JST ZH B4B-ZR-SM4-TF |
| `J9` | Conn_01x03 | Mic kill-switch loop | PinHeader 1×03 2.54 mm |
| `J10` | Conn_01x02 | Secondary earpiece | PinHeader 1×02 2.54 mm |
| `SW1` | SW_Push | Hookswitch | Button_Switch_THT SW_PUSH_6mm |
| `F1` | 1.5 A | Polyfuse on +12 V input | Fuse_1210_3225Metric |
| `D1` | SS54 | Buck catch diode | D_SMA |
| `L1` | 33 µH | Buck output inductor | L_12x12mm_H8mm |
| `R1` | 220 Ω | Status LED current limit | 0805 |
| `R2` | 33 kΩ | DRV8871 current limit sense | 2512 |
| `R3` | 27 Ω | USB D- series termination | 0402 |
| `R4` | 27 Ω | USB D+ series termination | 0402 |
| `R5` | 10 kΩ | RP2040 RUN pullup | 0402 |
| `R7` | 10 kΩ | Codec `/RESET` pullup to +3V3 | 0402 |
| `R9` | 1 kΩ | XOUT series damping resistor (per RPi "Hardware design with RP2040" §2 for IOVDD = 3.3 V) | 0402 |
| `R8` | 2.2 kΩ | Mic bias series (MICBIAS → mic) | 0402 |
| `C1` | 680 µF | +12 V bulk input | CP_Elec 10×10.5 |
| `C2` | 220 µF | +5 V bulk output | CP_Elec 8×6.5 |
| `C3` | 100 nF | MIC_HOT to GND RFI filter | 0805 |
| `C4` | 100 nF | +12 V high-frequency bypass | 0805 |
| `C5, C6` | 15 pF C0G | Crystal load caps, `C_load = 2·(CL − C_stray)` for 10 pF CL | 0402 |
| `C9` | 10 µF | +3V3 bulk near LDO output | 0603 |
| `C10` | 1 µF | DVDD_1V1 bulk at VREG_VOUT, matches Pico reference | 0402 |
| `C11` | 10 µF | +5 V bulk near LDO input | 0603 |
| `C12–C16, C28` | 100 nF | RP2040 IOVDD per-pin HF decap (§2.9.1; one per IOVDD pin, 6 total) | 0402 |
| `C29, C30` | 100 nF | RP2040 DVDD per-pin decap on DVDD_1V1 (§2.9.2; pins 23, 50) | 0402 |
| `C31` | 1 µF | RP2040 VREG_VIN local cap (§2.9.3; pin 44) | 0402 |
| `C32` | 100 nF | RP2040 USB_VDD decap (§2.9.4; pin 48) | 0402 |
| `C33` | 100 nF | RP2040 ADC_AVDD decap (§2.9.5; pin 43) | 0402 |
| `C34` | 100 nF | W25Q16JV flash VCC decap (Winbond DS + RP2040 reference practice) | 0402 |
| `C35` | 100 nF | RUN pin POR filter at U3.26 (Pico reference) | 0402 |
| `C17, C19, C22` | 10 µF | Codec AVDD/DRVDD/IOVDD bulk | 0402 |
| `C18, C20, C21` | 100 nF | Codec AVDD/DRVDD/IOVDD high-frequency decap | 0805 |
| `C23` | 100 nF | Mic1LP DC-blocking coupling cap | 0402 |
| `C25` | 10 µF | CODEC_DVDD bulk (codec internal 1.8 V digital rail) | 0402 |
| `C26` | 1 µF | Unused codec analog input termination to GND | 0402 |
| `C27` | 1 nF | Codec `/RESET` ESD cap | 0402 |
| `MH1, MH2, MH3` | — | M3 mechanical mounting | MountingHole_3.2mm_M3 |

## Power tree

```
J3 barrel jack    ──┬── J3.1 ──── F1 (1.5 A polyfuse) ──── +12 V
                    │                                        │
                    └── J3.2, J3.3 ──── GND                   ├── C1 680 µF bulk
                                                              ├── C4 100 nF HF bypass
                                                              ├── U2.5  VM (DRV8871 motor supply)
                                                              └── U1.1  VIN (LM2596 input)

U1 LM2596S-5 (fixed 5 V)
    pin 1 VIN        ← +12 V
    pin 2 OUT        → switching node → D1 cathode → L1.1
    pin 3 GND        ← GND
    pin 4 FB         ← +5 V (fixed-variant senses output directly)
    pin 5 ~ON/OFF    ← GND (active-low enable; tied low = ON)
D1 SS54 catch diode  : anode → GND, cathode → switching node
L1 33 µH             : 1 → switching node, 2 → +5 V

+5 V rail            ├── C2 220 µF bulk (at L1 output)
                     ├── C11 10 µF bulk (at LDO input)
                     ├── U5.3 VI (AMS1117-3.3 input)
                     └── J1.2, J1.4 (Pi +5 V input pins)

U5 AMS1117-3.3
    pin 1 GND        ← GND
    pin 2 VO         → +3V3 rail
    pin 3 VI         ← +5 V

+3V3 rail            ├── C9 10 µF bulk
                     ├── C12, C13, C14, C15, C16, C28 — 6 × 100 nF, one per RP2040 IOVDD pin (§2.9.1)
                     ├── C31 1 µF local at U3.44 VREG_VIN (§2.9.3)
                     ├── C32 100 nF at U3.48 USB_VDD (§2.9.4)
                     ├── C33 100 nF at U3.43 ADC_AVDD (§2.9.5)
                     ├── C34 100 nF at U4.8 W25Q16 VCC (Winbond DS)
                     ├── C17, C19, C22 10 µF bulk (codec AVDD, DRVDD, IOVDD)
                     ├── C18, C20, C21 100 nF HF (codec power decap pairs)
                     ├── R5 pullup  → U3.26 RUN
                     ├── R7 pullup  → CODEC_RESET
                     ├── U3 IOVDD ×6, ADC_AVDD, VREG_VIN, USB_VDD
                     └── U6 AVDD, DRVDD ×2, IOVDD

DVDD_1V1 (RP2040 internal 1.1 V regulator output)
                     ├── C10 1 µF bulk at U3.45 VREG_VOUT
                     ├── C29 100 nF local at U3.23 DVDD (§2.9.2)
                     ├── C30 100 nF local at U3.50 DVDD (§2.9.2)
                     ├── U3 pin 45 VREG_VOUT (source)
                     └── U3 pins 23, 50 DVDD (sink)

CODEC_DVDD (TLV320AIC3104 internal 1.8 V digital regulator output)
                     ├── C25 10 µF bulk
                     └── U6 pin 24 DVDD
```

## Raspberry Pi 40-pin header (J1)

J1 is the standard Raspberry Pi 40-pin header; Pi pin numbering matches the physical pinout. J1 is not rotated or mirrored in the schematic.

| J1 pin | Pi function | Net | Notes |
|---|---|---|---|
| 1 | +3V3 | (NC) | We don't draw from Pi +3V3; the carrier provides its own rail |
| 2 | +5V | **+5V** | Carrier supplies +5 V to the Pi |
| 3 | GPIO2 / SDA1 | **CODEC_SDA** | Codec I²C data |
| 4 | +5V | **+5V** | Second carrier-supplied +5 V pin |
| 5 | GPIO3 / SCL1 | **CODEC_SCL** | Codec I²C clock |
| 6 | GND | **GND** | |
| 7 | GPIO4 / GCLK0 | **CODEC_MCLK** | Codec master clock from Pi GPCLK0 |
| 8 | GPIO14 / TXD0 | **UART_TX_PI** | Pi-side TX; lands on RP2040 GPIO29 (RX) |
| 9 | GND | **GND** | |
| 10 | GPIO15 / RXD0 | **UART_RX_PI** | Pi-side RX; comes from RP2040 GPIO28 (TX) |
| 11 | GPIO17 | (NC) | |
| 12 | GPIO18 / PCM_CLK | **CODEC_BCLK** | I²S bit clock |
| 13 | GPIO27 | (NC) | |
| 14 | GND | **GND** | |
| 15 | GPIO22 | **CODEC_RESET** | Pi drives codec /RESET low during probe via Linux driver DT `reset-gpios` binding |
| 16 | GPIO23 | (NC) | |
| 17 | +3V3 | (NC) | |
| 18 | GPIO24 | **SWD_SWDIO** | Pi bit-bangs SWDIO to program the RP2040 (openocd raspberrypi-native convention) |
| 19 | GPIO10 / MOSI | (NC) | |
| 20 | GND | **GND** | |
| 21 | GPIO9 / MISO | (NC) | |
| 22 | GPIO25 | **SWD_SWCLK** | Pi bit-bangs SWCLK |
| 23 | GPIO11 / SCLK | (NC) | |
| 24 | GPIO8 / CE0 | (NC) | |
| 25 | GND | **GND** | |
| 26 | GPIO7 / CE1 | (NC) | |
| 27 | ID_SD | (NC) | (HAT EEPROM pin — no EEPROM on this carrier) |
| 28 | ID_SC | (NC) | |
| 29 | GPIO5 | (NC) | |
| 30 | GND | **GND** | |
| 31 | GPIO6 | (NC) | |
| 32 | GPIO12 | (NC) | |
| 33 | GPIO13 | (NC) | |
| 34 | GND | **GND** | |
| 35 | GPIO19 / PCM_FS | **CODEC_WCLK** | I²S word/frame clock |
| 36 | GPIO16 | (NC) | |
| 37 | GPIO26 | (NC) | |
| 38 | GPIO20 / PCM_DIN | **CODEC_DOUT** | Codec audio output → Pi audio input |
| 39 | GND | **GND** | |
| 40 | GPIO21 / PCM_DOUT | **CODEC_DIN** | Pi audio output → codec audio input |

The audio I²S/I²C pin choices follow the standard Pi audio HAT convention used by IQaudIO Codec Zero, HiFiBerry, Innomaker, etc. No dtoverlay gymnastics required on the Pi — use the stock `i2s-mmap` + a simple `reset-gpios = <&gpio 22 GPIO_ACTIVE_LOW>` binding in the codec overlay.

## RP2040 (U3)

KiCad symbol `MCU_RaspberryPi:RP2040`; pin numbering matches the RP2040 datasheet (QFN-56).

| Pin | Name | Net | Notes |
|---|---|---|---|
| 1 | IOVDD | +3V3 | |
| 2–9 | GPIO0–GPIO7 | (NC) | Reserved for future use |
| 10 | IOVDD | +3V3 | |
| 11–17 | GPIO8–GPIO14 | (NC) | Reserved |
| 18 | GPIO15 | RINGER_IN2 | To DRV8871 IN2 |
| 19 | TESTEN | GND | Normal operation |
| 20 | XIN | XIN | 12 MHz crystal input |
| 21 | XOUT | XOUT_MCU | Drives XOUT side of crystal through R9 (1 kΩ series damping) to net `XOUT` at Y1 |
| 22 | IOVDD | +3V3 | |
| 23 | DVDD | DVDD_1V1 | RP2040 core supply from internal regulator |
| 24 | SWCLK | SWD_SWCLK | To Pi GPIO25 |
| 25 | SWDIO | SWD_SWDIO | To Pi GPIO24 |
| 26 | RUN | RUN | Pulled up to +3V3 via R5 10 kΩ, decoupled to GND via C35 100 nF (POR filter, Pico reference) |
| 27 | GPIO16 | LED_OUT | Via R1 220 Ω to status LED on J6 |
| 28, 29 | GPIO17, GPIO18 | (NC) | Reserved |
| 30 | GPIO19 | RINGER_IN1 | To DRV8871 IN1 |
| 31 | GPIO20 | HOOK_SW | Hookswitch input (SW1) |
| 32 | GPIO21 | KP_ROW2 | Keypad row 2 |
| 33 | IOVDD | +3V3 | |
| 34 | GPIO22 | KP_COL2 | Keypad column 2 |
| 35 | GPIO23 | KP_COL1 | Keypad column 1 |
| 36 | GPIO24 | KP_COL0 | Keypad column 0 |
| 37 | GPIO25 | KP_ROW3 | Keypad row 3 |
| 38 | GPIO26/ADC0 | KP_ROW1 | Keypad row 1 |
| 39 | GPIO27/ADC1 | KP_ROW0 | Keypad row 0 |
| 40 | GPIO28/ADC2 | UART_RX_PI | UART0 TX alt → Pi GPIO15 (RXD0) |
| 41 | GPIO29/ADC3 | UART_TX_PI | UART0 RX alt ← Pi GPIO14 (TXD0) |
| 42 | IOVDD | +3V3 | |
| 43 | ADC_AVDD | +3V3 | C33 100 nF local decap (§2.9.5); no separate analog filter, short trace, reference ground plane |
| 44 | VREG_VIN | +3V3 | Internal LDO input |
| 45 | VREG_VOUT | DVDD_1V1 | Internal LDO output (1.1 V), drives DVDD pins |
| 46 | USB_DM | USB_DM | Via R3 27 Ω to USB_DM_PAD test stub |
| 47 | USB_DP | USB_DP | Via R4 27 Ω to USB_DP_PAD test stub |
| 48 | USB_VDD | +3V3 | |
| 49 | IOVDD | +3V3 | |
| 50 | DVDD | DVDD_1V1 | |
| 51 | QSPI_SD3 | QSPI_SD3 | Flash IO3 |
| 52 | QSPI_SCLK | QSPI_SCLK | Flash CLK |
| 53 | QSPI_SD0 | QSPI_SD0 | Flash DI/IO0 |
| 54 | QSPI_SD2 | QSPI_SD2 | Flash WP/IO2 |
| 55 | QSPI_SD1 | QSPI_SD1 | Flash DO/IO1 |
| 56 | ~QSPI_SS | QSPI_SS | Flash /CS |
| 57 | GND (exposed pad) | GND | Die thermal pad |

UART naming is Pi-centric: `UART_TX_PI` is the wire on which the *Pi's* TX signal travels (Pi GPIO14 out → RP2040 GPIO29 RX in). `UART_RX_PI` is the wire on which the Pi's RX signal travels (RP2040 GPIO28 TX out → Pi GPIO15 RX in).

## W25Q16JV flash (U4)

Standard Winbond SPI NOR flash, SOIC-8 narrow (150-mil) package. The `SN` suffix of the part number (`W25Q16JVSNIQ`) identifies the 150-mil variant — do not substitute a `SS`-suffix part without changing the footprint.

| Pin | Name | Net |
|---|---|---|
| 1 | ~CS | QSPI_SS |
| 2 | DO / IO1 | QSPI_SD1 |
| 3 | ~WP / IO2 | QSPI_SD2 |
| 4 | GND | GND |
| 5 | DI / IO0 | QSPI_SD0 |
| 6 | CLK | QSPI_SCLK |
| 7 | ~HOLD / IO3 | QSPI_SD3 |
| 8 | VCC | +3V3 |

No external pullup on `QSPI_SS`. The RP2040 bootrom actively drives /CS within nanoseconds of reset, and the official Pico + all three Raspberry Pi Press reference designs (`eg/ch05`, `eg/ch10`, `eg/ch11`) omit it. C34 (100 nF, 0402) decouples the flash VCC directly at U4.8 per the Winbond W25Q16JV datasheet.

## DRV8871 ringer driver (U2)

TI DRV8871DDA in HTSOP-8 with thermal pad. Drives the mechanical bell coil through a full-bridge. The `ILIM` resistor R2 (33 kΩ) sets a current limit of approximately 0.94 A per TI datasheet equation 1.

| Pin | Name | Net | Notes |
|---|---|---|---|
| 1 | GND | GND | |
| 2 | IN2 | RINGER_IN2 | From RP2040 GPIO15 |
| 3 | IN1 | RINGER_IN1 | From RP2040 GPIO19 |
| 4 | ILIM | ILIM | To R2 33 kΩ → GND |
| 5 | VM | +12V | Motor supply |
| 6 | OUT1 | BELL_A | To J7.1 (bell coil +) |
| 7 | GND | GND | |
| 8 | OUT2 | BELL_B | To J7.2 (bell coil −) |
| 9 (EP) | GND | GND | Thermal pad |

The bell is rung by alternating IN1/IN2 PWM from the RP2040, which produces an AC current waveform in the coil. The DRV8871 handles full-bridge decode automatically.

## TLV320AIC3104 codec (U6)

TI stereo audio codec in VQFN-32 5×5 mm. KiCad symbol: custom `digits-pcb:TLV320AIC3104IRHBR`, matches datasheet pin naming.

| Pin | Name | Net | Notes |
|---|---|---|---|
| 1 | SDA | CODEC_SDA | I²C data to Pi GPIO2 |
| 2 | MIC1LP | CODEC_MIC1LP | Mic hot input after coupling cap |
| 3 | MIC1LM | GND | Differential mic not used; invert input to GND |
| 4 | MIC1RP | CODEC_UNUSED_IN | Unused input, terminated via C26 1 µF to GND |
| 5 | MIC1RM | CODEC_UNUSED_IN | |
| 6 | MIC2L | CODEC_UNUSED_IN | |
| 7 | MICBIAS | MICBIAS_OUT | Bias generator output, routes through R8 to mic signal |
| 8 | MIC2R | CODEC_UNUSED_IN | |
| 9 | AVSS1 | GND | |
| 10 | DRVDD | +3V3 | |
| 11 | HPLOUT | EAR_P | Capless BTL + side to handset earpiece |
| 12 | HPLCOM | EAR_N | Capless BTL − side to handset earpiece |
| 13 | DRVSS | GND | |
| 14 | HPRCOM | (NC) | Right channel unused |
| 15 | HPROUT | (NC) | |
| 16 | DRVDD | +3V3 | |
| 17 | AVDD | +3V3 | |
| 18 | AVSS2 | GND | |
| 19 | LEFT_LOP | (NC) | Line outs unused |
| 20 | LEFT_LOM | (NC) | |
| 21 | RIGHT_LOP | (NC) | |
| 22 | RIGHT_LOM | (NC) | |
| 23 | ~RESET | CODEC_RESET | Pi GPIO22 drives via Linux DT `reset-gpios` |
| 24 | DVDD | CODEC_DVDD | Internal 1.8 V digital regulator output |
| 25 | MCLK | CODEC_MCLK | From Pi GPCLK0 (GPIO4) |
| 26 | BCLK | CODEC_BCLK | I²S bit clock from Pi |
| 27 | WCLK | CODEC_WCLK | I²S word clock from Pi |
| 28 | DIN | CODEC_DIN | Audio data in from Pi |
| 29 | DOUT | CODEC_DOUT | Audio data out to Pi |
| 30 | DVSS | GND | |
| 31 | IOVDD | +3V3 | |
| 32 | SCL | CODEC_SCL | I²C clock from Pi GPIO3 |
| 33 | GND (exposed pad) | GND | Die thermal pad, also analog/digital GND bond |

Reset design: R7 (10 kΩ) pulls `/RESET` to +3V3. C27 (1 nF) provides ESD protection per TI SLAS510C recommendation. The Pi explicitly drives `/RESET` low then releases during Linux codec driver probe, so the pullup is a safety net rather than the primary reset source.

`CODEC_DVDD` is the codec's own internal digital regulator output (1.8 V) — it is *not* tied to `DVDD_1V1` (the RP2040's 1.1 V rail). Keep them as separate nets.

## Audio analog paths

### Mic kill switch path

```
                             ┌── C3 100 nF ──┐ (RFI filter across raw mic)
                             │               │
 Handset MIC+  →  J8.1 ── J9.1 ── [external kill switch] ── J9.2 ──┬── C23 100 nF ── U6.2 MIC1LP
                                                                    │                (DC block)
                                                       R8 2.2 kΩ ───┤
                                                                    │
                                                       U6.7 MICBIAS ┘
                                                       (parallel bias inject)

 Handset MIC−  →  J8.4 ── J9.3 ── GND
```

The mic signal leaves J8 as `MIC_HOT`, goes out through J9 to an external kill-switch lever on the phone cradle, and returns on `MIC_FROM_SW`. Mic bias from `U6.7 MICBIAS` is injected into the post-switch `MIC_FROM_SW` node via R8 (2.2 kΩ series, parallel branch — R8 is *not* in the signal path), and the same node is AC-coupled into the codec's `MIC1LP` input through C23 (100 nF). The 100 nF cap into the codec's 20 kΩ input impedance gives an 80 Hz high-pass corner per the TLV320AIC3104 datasheet recommendation.

C3 (100 nF) is an RFI suppression cap across the raw mic signal at the connector, before the kill-switch cable.

### Earpiece BTL path

```
 U6.11 HPLOUT ──── EAR_P ──── J8.2, J10.1
                                (handset earpiece +)
 U6.12 HPLCOM ──── EAR_N ──── J8.3, J10.2
                                (handset earpiece −)
```

The TLV320AIC3104 drives the earpiece in **capless Bridge-Tied Load** mode: both `HPLOUT` and `HPLCOM` are biased to mid-rail (~1.65 V), and the ~150 Ω earpiece sees only the AC difference between them. No DC-blocking capacitor is needed because there is no DC current through the load. This gives ~26 mW into 150 Ω from a 3.3 V rail — roughly 4× more than a single-ended capacitor-coupled output could deliver from the same supply. TI datasheet SLAS510C confirms capless BTL works down to 16 Ω, and 150 Ω is an easier load than that.

J8 and J10 both expose the earpiece pair to allow either to be used as the primary handset connector.

## Crystal oscillator

12 MHz fundamental crystal between `U3.20 XIN` and `U3.21 XOUT`. Y1 is explicitly specified as **Abracon ABM8-272-T3** (CL = 10 pF, ESR ≤ 50 Ω, 3.2 × 2.5 mm 4-pad SMD) per RP2040 datasheet §2.16.1.1, which states the Pico's clock tree has been tuned specifically for this part. Substituting a different crystal is a blocker without re-validation.

Load caps C5/C6 = **15 pF C0G 0402** computed as `C_load = 2·(CL − C_stray)` with CL = 10 pF and C_stray ≈ 2.5 pF (typical for a clean 4-pad SMD layout). Previous revisions of this doc specified 22 pF, which corresponds to no real-world crystal and is a must-not-use value.

A 1 kΩ series damping resistor (**R9**) sits on the XOUT side between the RP2040 and the crystal. This is mandated by Raspberry Pi's *Hardware design with RP2040* guide §2 for designs running IOVDD = 3.3 V and limits the drive level into the crystal. Do not omit it or substitute 0 Ω.

```
 U3.20 XIN ────────────────────── Y1.1 (Xi)
                                   Y1 ABM8-272-T3 (12 MHz, CL=10 pF)
 U3.21 XOUT ── R9 1 kΩ ────────── Y1.3 (Xo)
 C5 15 pF ── GND                   C6 15 pF ── GND
                                   Y1.2, Y1.4 ── GND (case shield)
```

**Schematic symbol:** `Device:Crystal_GND24` (NOT `Device:Crystal`). The 2-pin `Device:Crystal` symbol is incompatible with the 4-pad `Crystal_SMD_3225-4Pin_3.2x2.5mm` footprint used by the ABM8: pads 1 and 3 are diagonal signal pads (Xi/Xo), pads 2 and 4 are the case shield (GND). Using the 2-pin symbol lands net XOUT on footprint pad 2 (which is GND, not Xo), leaving the actual Xo pin (pad 3) floating — the crystal never oscillates. Keep `Device:Crystal_GND24`; do not substitute.

## RP2040 cluster per-pin decoupling placement

The schematic places 13 decoupling caps (C12–C16, C28 on IOVDD; C29, C30 on DVDD_1V1; C31 on VREG_VIN; C32 on USB_VDD; C33 on ADC_AVDD; C34 on W25Q16 VCC) on the correct rails, but the KiCad schematic symbol collapses all IOVDD pins and both DVDD pins into single nodes — so electrical connectivity alone does not prove each cap is physically adjacent to its named pin after PCB layout. RP2040 DS §2.9 requires each cap within a few millimetres of the pin it decouples. The per-pin target mapping is derived from the Raspberry Pi Minimal-KiCAD reference geometry (3.05 mm uniform radial decoupling ring around the QFN) and enforced at placement time.

## USB

R3 (27 Ω) and R4 (27 Ω) provide series termination on the RP2040's USB data lines. On this revision there is no USB connector on the board. The `USB_DM` and `USB_DP` nets run from the RP2040 to R3/R4; the far-side pads of R3/R4 are unlabeled NC pins (flagged with explicit no-connect markers so they pass ERC). This leaves the option to add a USB connector in a future rev by labeling those pads and running them to a connector, without changing the RP2040-side topology.

```
 U3.46 USB_DM ── R3.2 ── R3.1 (NC, reserved for future USB connector)
 U3.47 USB_DP ── R4.2 ── R4.1 (NC, reserved for future USB connector)
```

## SWD programming

The Pi programs the RP2040 via bit-banged SWD over two GPIO pins using openocd's `raspberrypi-native` configuration. No dedicated SWD header is present on this revision — debugging is done by wiring the Pi to the RP2040 through the 40-pin header.

- `SWD_SWDIO` — U3 pin 25 ↔ J1.18 (Pi GPIO24)
- `SWD_SWCLK` — U3 pin 24 ↔ J1.22 (Pi GPIO25)

## Keypad, hookswitch, and LED

```
 J4 Conn_01x07     →  U3 RP2040
   J4.1 KP_COL0       U3.36 GPIO24
   J4.2 KP_COL1       U3.35 GPIO23
   J4.3 KP_COL2       U3.34 GPIO22
   J4.4 KP_ROW3       U3.37 GPIO25
   J4.5 KP_ROW2       U3.32 GPIO21
   J4.6 KP_ROW1       U3.38 GPIO26
   J4.7 KP_ROW0       U3.39 GPIO27

 SW1 hookswitch
   SW1.1 HOOK_SW  →  U3.31 GPIO20
   SW1.2 GND      →  closes hookswitch to ground when pressed

 Status LED
   U3.27 GPIO16 →  LED_OUT  →  R1 220 Ω  →  J6.1 (LED anode)
                                             J6.2 → GND (LED cathode)
```

J4 is rotated 180° on the schematic; keep the pin-to-row/column mapping fixed when updating so the keypad cable wiring stays consistent. Pin numbering on the connector is independent of rotation — the table above refers to connector pin numbers.

## Ringer

The RP2040 drives two GPIOs into the DRV8871 H-bridge inputs to produce an AC current through the bell coil.

```
 U3.30 GPIO19 → RINGER_IN1 → U2.3 IN1
 U3.18 GPIO15 → RINGER_IN2 → U2.2 IN2
 U2.6 OUT1 → BELL_A → J7.1 (bell coil +)
 U2.8 OUT2 → BELL_B → J7.2 (bell coil −)
 U2.4 ILIM → R2 33 kΩ → GND   (sets ~0.94 A peak current)
```

Bell ringing is driven by alternating PWM on IN1 and IN2 from the RP2040 firmware; the DRV8871 internally sequences the full-bridge.

## Mounting holes

Three M3 mechanical mounts, positions locked by the phone enclosure:

| Ref | Position (mm) | Footprint |
|---|---|---|
| MH1 | (23.4, 47.96) | MountingHole_3.2mm_M3 |
| MH2 | (82.3, 61.16) | MountingHole_3.2mm_M3 |
| MH3 | (87.4, 30.46) | MountingHole_3.2mm_M3 |

No electrical connections. These holes also carry no ground stitching on this revision.

## References

- [TLV320AIC3104 datasheet (SLAS510C)](https://www.ti.com/lit/ds/symlink/tlv320aic3104.pdf)
- [TLV320AIC3104EVM user guide (SLAU218A)](https://www.ti.com/lit/ug/slau218a/slau218a.pdf)
- [TI DRV8871 datasheet](https://www.ti.com/lit/ds/symlink/drv8871.pdf)
- [Winbond W25Q16JV datasheet](https://www.winbond.com/resource-files/w25q16jv%20spi%20revg%2003222018%20plus.pdf)
- [Raspberry Pi 40-pin header pinout](https://pinout.xyz/)
- [RP2040 datasheet](https://datasheets.raspberrypi.com/rp2040/rp2040-datasheet.pdf)
