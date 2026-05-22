# PCB v3 net topology reference

Authoritative description of how every IC, connector, and passive on the digits carrier board (`hardware/pcb/v3/kicad/digits-pcb.kicad_sch` plus the `/codec/` and `/ringer/` sub-sheets) is wired. Use this as the source of truth when debugging connectivity, when spec'ing a new revision, or when auditing whether a schematic change stayed faithful to the design intent.

## What this board does

The digits v3 PCB is a carrier that sits under a Raspberry Pi Zero 2 W. It:

- Takes +5 V in through the `PWR` JST XH connector, runs it through a 1.5 A polyfuse (F1) onto the +5V rail, and drops to +3V3 via an AMS1117 LDO (U5). The +5 V rail feeds the Pi via the 40-pin header.
- Boosts +5 V up to ~37 V (`VBOOST`) on-board with an XL6019 boost converter (U10). VBOOST is the DRV8871 motor supply for the bell. This replaces V1/V2's external mains step-up transformer.
- Hosts a TLV320AIC3104 stereo audio codec (U6) that replaces the Pi Codec Zero HAT in the v1 prototype. The codec talks to the Pi over I2C (control) and I2S (audio), drives the handset earpiece in capless BTL mode, and receives the handset mic through a DC-blocking cap after the SW1 series mic-kill pole. Its 1.8 V digital rail comes from an external XC6206 LDO (U7).
- Hosts an RP2040 microcontroller (U3) with a W25Q16 SPI flash (U4). The RP2040 handles the keypad matrix, hook sense, a status LED, and the bell ringer (driven through a DRV8871 H-bridge). The RP2040 talks to the Pi over UART and is programmed via SWD from a Pi GPIO pair.
- Senses hook state and series-interrupts the mic with a single 6-pin DPDT cradle switch (SW1).
- Provides mechanical mounting through three M3 holes at positions locked by the phone enclosure.

## Components

All component references, values, footprints, and placements are fixed by the phone enclosure and the existing PCB. Changing any of them requires a board respin. Full per-component detail is in `COMPONENTS.md`.

| Ref | Value | Purpose | Footprint |
|---|---|---|---|
| `U2` | DRV8871DDAR | Bell ringer H-bridge | SOIC-8-1EP |
| `U3` | RP2040 | Keypad/hook/ringer/UART MCU | QFN-56-1EP |
| `U4` | W25Q16JVSSIQ | RP2040 QSPI boot flash | SOIC-8 |
| `U5` | AMS1117-3.3 | +5V to +3V3 LDO | SOT-223-3 |
| `U6` | TLV320AIC3104IRHBR | Stereo audio codec | VQFN-32 5x5 mm |
| `U7` | XC6206P182MR | Codec +1V8 LDO | SOT-23 |
| `U10` | XL6019E1 | +5V to ~37V bell boost | TO-263-5 |
| `Y1` | ABM8-272-T3 (12 MHz) | RP2040 crystal, per RP2040 DS sec 2.16.1.1 | Crystal_SMD_3225-4Pin |
| `Pi Zero W 2` | Conn_02x20_Odd_Even | Raspberry Pi 40-pin header | PinHeader 2x20 2.54 mm |
| `PWR` | VIN_BARREL_PIGTAIL | +5 V power input | JST XH B2B-XH-A 2.5 mm |
| `KEYPAD` | Conn_01x07 | Keypad flex ribbon | JST ZH B7B-ZR-SM4-TF |
| `LED` | Conn_01x02 | Status LED | JST ZH B2B-ZR-SM4-TF |
| `BELL` | Screw_Terminal_01x02 | Bell coil | JST ZH B2B-ZR-SM4-TF |
| `J8` | Conn_01x04 | Handset (mic + earpiece) | JST ZH B4B-ZR-SM4-TF |
| `SW1` | Hook_DPDT | Hook sense + mic kill cradle switch | SW_DPDT_Hook_24.2x17.1mm (B.Cu) |
| `SW2` | SW_Push | BOOTSEL tact switch | SW_PUSH_6mm |
| `D2` | LED_RED | +5V power indicator | LED 0603 |
| `D3` | LED_GREEN | +3V3 power indicator | LED 0603 |
| `D10` | SS56 | Boost rectifier | SMA |
| `L10` | 47 uH | Boost inductor | IND-SMD 12.3x12.3 |
| `F1` | 1.5 A | Polyfuse on +5 V input | Fuse_1210_3225Metric |
| `R1` | 220 ohm | Status LED current limit | 0805 |
| `R2` | 33 kohm | DRV8871 ILIM | 0402 |
| `R5` | 10 kohm | RP2040 RUN pullup | 0402 |
| `R9` | 1 kohm | XOUT series damping (per RPi "Hardware design with RP2040" sec 2 for IOVDD = 3.3 V) | 0402 |
| `R10` | 2.2 kohm | Mic bias series (MICBIAS to mic) | 0402 |
| `R11` | 10 kohm | Codec /RESET pullup | 0402 |
| `R12` | 300 ohm | D2 (+5V LED) current limit | 0402 |
| `R13` | 330 ohm | D3 (+3V3 LED) current limit | 0402 |
| `R20` | 57.6 kohm | Boost FB divider top | 0402 |
| `R21` | 2 kohm | Boost FB divider bottom | 0402 |
| `C1` | 470 uF | +5 V bulk input | CP_Elec 10x10.5 |
| `C3` | 100 nF | MIC_HOT to GND RFI filter | 0805 |
| `C4` | 100 nF | +5 V HF bypass | 0805 |
| `C5, C6` | 15 pF C0G | Crystal load caps, C_load = 2*(CL - C_stray) for 10 pF CL | 0402 |
| `C9` | 10 uF | +3V3 bulk near LDO output | 0603 |
| `C10` | 1 uF | DVDD_1V1 bulk at VREG_VOUT | 0402 |
| `C11` | 10 uF | +5 V bulk near LDO input | 0603 |
| `C12-C16, C28` | 100 nF | RP2040 IOVDD per-pin HF decap (sec 2.9.1; 6 total) | 0402 |
| `C29, C30` | 100 nF | RP2040 DVDD per-pin decap on DVDD_1V1 (sec 2.9.2; pins 23, 50) | 0402 |
| `C31` | 1 uF | RP2040 VREG_VIN local cap (sec 2.9.3; pin 44) | 0402 |
| `C32` | 100 nF | RP2040 USB_VDD decap (sec 2.9.4; pin 48) | 0402 |
| `C33` | 100 nF | RP2040 ADC_AVDD decap (sec 2.9.5; pin 43) | 0402 |
| `C34` | 100 nF | W25Q16JV flash VCC decap | 0402 |
| `C35` | 100 nF | RUN pin POR filter at U3.26 | 0402 |
| `C36` | 100 nF | U7 +1V8 LDO input decap | 0402 |
| `C37` | 10 uF | U7 +1V8 LDO output bulk | 0402 |
| `C38` | 100 nF | Codec DVDD (+1V8) HF decap at U6.32 | 0402 |
| `C39` | 1 uF | Codec DVDD (+1V8) bulk at U6.32 | 0402 |
| `C40` | 100 nF | Codec IOVDD HF decap at U6.7 | 0402 |
| `C41, C42` | 100 nF | Codec DRVDD HF decap at U6.18, U6.24 | 0402 |
| `C43` | 100 nF | Codec AVDD HF decap at U6.25 | 0402 |
| `C44` | 1 uF | Codec +3V3 bulk near EP | 0402 |
| `C45` | 10 uF | Codec +3V3 bulk near EP | 0402 |
| `C46, C47` | 0.47 uF | Mic1L AC-coupling pair into the codec | 0402 |
| `C48` | 100 nF | MICBIAS bypass | 0402 |
| `C49-C52` | 100 nF | Unused codec mic-input terminations | 0402 |
| `C53` | 1 nF | Codec /RESET ESD cap | 0402 |
| `C54` | 100 nF | DRV8871 VM (VBOOST) HF bypass | 0402 |
| `C55` | 47 uF | DRV8871 VM (VBOOST) bulk | CP_Elec 5x5.3 |
| `C100` | 100 uF | VBOOST bulk | 10 mm dia SMD |
| `C101` | 1 uF | VBOOST HF bypass | 0805 |
| `MH1, MH2, MH3` | -- | M3 mechanical mounting | MountingHole_3.2mm_M3 |

## Power tree

```
PWR JST XH        ──┬── PWR.1 ── /VIN_RAW ── F1 (1.5 A polyfuse) ── +5 V
                    │                                                 │
                    └── PWR.2 ── GND                                  ├── C1 470 uF bulk
                                                                      ├── C4 100 nF HF bypass
                                                                      ├── C11 10 uF (at LDO input)
                                                                      ├── U5.3 VI (AMS1117-3.3 input)
                                                                      ├── U10.2 / U10.4 VIN (boost input)
                                                                      ├── L10.1 (boost inductor)
                                                                      ├── D2 anode (red power LED via R12)
                                                                      └── Pi header pin 2, pin 4 (Pi +5 V)

U10 XL6019E1 boost (set to ~37 V)
    pin 1 GND         <- GND
    pin 2, 4 VIN      <- +5 V
    pin 3 SW          -> SW_NODE  (also the metal tab, pad 6 -> SW_NODE, NOT GND)
    pin 5 FB          <- FB_NODE
L10 47 uH             : 1 -> +5 V, 2 -> SW_NODE
D10 SS56              : anode -> SW_NODE, cathode -> VBOOST
R20 57.6k             : VBOOST -> FB_NODE
R21 2k                : FB_NODE -> GND
Vout = 1.25 * (1 + R20/R21) ~= 37.25 V

VBOOST rail (~37 V)   ├── C100 100 uF bulk
                      ├── C101 1 uF HF
                      ├── C54 100 nF (DRV8871 VM HF)
                      ├── C55 47 uF (DRV8871 VM bulk)
                      └── U2.5 VM (DRV8871 motor supply)

U5 AMS1117-3.3
    pin 1 GND         <- GND
    pin 2 VO          -> +3V3 rail
    pin 3 VI          <- +5 V

+3V3 rail            ├── C9 10 uF bulk
                     ├── C12, C13, C14, C15, C16, C28 (6 x 100 nF, one per RP2040 IOVDD pin, sec 2.9.1)
                     ├── C31 1 uF local at U3.44 VREG_VIN (sec 2.9.3)
                     ├── C32 100 nF at U3.48 USB_VDD (sec 2.9.4)
                     ├── C33 100 nF at U3.43 ADC_AVDD (sec 2.9.5)
                     ├── C34 100 nF at U4.8 W25Q16 VCC
                     ├── C36 100 nF at U7.3 (codec +1V8 LDO input)
                     ├── C40 100 nF at U6.7 IOVDD; C41/C42 at U6.18/24 DRVDD; C43 at U6.25 AVDD
                     ├── C44 1 uF, C45 10 uF (codec +3V3 bulk near EP)
                     ├── R5 pullup  -> U3.26 RUN
                     ├── R11 pullup -> CODEC_RESET
                     ├── D3 anode (green power LED via R13)
                     ├── U3 IOVDD x6, ADC_AVDD, VREG_VIN, USB_VDD
                     ├── U6 AVDD, DRVDD x2, IOVDD
                     └── U7.3 VIN

+1V8 rail (/codec/+1V8, from U7 XC6206P182MR)
                     ├── C37 10 uF bulk at U7.2 VOUT
                     ├── C38 100 nF, C39 1 uF at U6.32 DVDD
                     └── U6.32 DVDD (codec digital core)

DVDD_1V1 (RP2040 internal 1.1 V regulator output)
                     ├── C10 1 uF bulk at U3.45 VREG_VOUT
                     ├── C29 100 nF local at U3.23 DVDD (sec 2.9.2)
                     ├── C30 100 nF local at U3.50 DVDD (sec 2.9.2)
                     ├── U3 pin 45 VREG_VOUT (source)
                     └── U3 pins 23, 50 DVDD (sink)
```

## Power indicator LEDs

Two on-board LEDs confirm the two main rails are up. Both are wired anode-to-rail, cathode-through-resistor-to-GND.

```
 +5 V  ── D2 (red)   ── /LED12V_K ── R12 300 ── GND
 +3V3  ── D3 (green) ── /LED3V3_K ── R13 330 ── GND
```

`/LED12V_K` is a legacy net label carried over from the 12 V era; on v3 the D2 anode is on +5 V. The net name is cosmetic and does not change the connectivity.

## Raspberry Pi 40-pin header (`Pi Zero W 2`)

The reference designator is `Pi Zero W 2`; it is the standard Raspberry Pi 40-pin header. Pi pin numbering matches the physical pinout. It is not rotated or mirrored in the schematic.

| Pin | Pi function | Net | Notes |
|---|---|---|---|
| 1 | +3V3 | (NC) | We don't draw from Pi +3V3; the carrier provides its own rail |
| 2 | +5V | **+5V** | Carrier supplies +5 V to the Pi |
| 3 | GPIO2 / SDA1 | **CODEC_SDA** | Codec I2C data |
| 4 | +5V | **+5V** | Second carrier-supplied +5 V pin |
| 5 | GPIO3 / SCL1 | **CODEC_SCL** | Codec I2C clock |
| 6 | GND | **GND** | |
| 7 | GPIO4 / GCLK0 | **CODEC_MCLK** | Codec master clock from Pi GPCLK0 |
| 8 | GPIO14 / TXD0 | **UART_TX_PI** | Pi-side TX; lands on RP2040 GPIO29 (RX) |
| 9 | GND | **GND** | |
| 10 | GPIO15 / RXD0 | **UART_RX_PI** | Pi-side RX; comes from RP2040 GPIO28 (TX) |
| 11 | GPIO17 | (NC) | |
| 12 | GPIO18 / PCM_CLK | **CODEC_BCLK** | I2S bit clock |
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
| 27 | ID_SD | (NC) | (HAT EEPROM pin, no EEPROM on this carrier) |
| 28 | ID_SC | (NC) | |
| 29 | GPIO5 | (NC) | |
| 30 | GND | **GND** | |
| 31 | GPIO6 | (NC) | |
| 32 | GPIO12 | (NC) | |
| 33 | GPIO13 | (NC) | |
| 34 | GND | **GND** | |
| 35 | GPIO19 / PCM_FS | **CODEC_WCLK** | I2S word/frame clock |
| 36 | GPIO16 | (NC) | |
| 37 | GPIO26 | (NC) | |
| 38 | GPIO20 / PCM_DIN | **CODEC_DOUT** | Codec audio output to Pi audio input |
| 39 | GND | **GND** | |
| 40 | GPIO21 / PCM_DOUT | **CODEC_DIN** | Pi audio output to codec audio input |

The audio I2S/I2C pin choices follow the standard Pi audio HAT convention used by IQaudIO Codec Zero, HiFiBerry, Innomaker, etc. No dtoverlay gymnastics required on the Pi: use the stock `i2s-mmap` plus a simple `reset-gpios = <&gpio 22 GPIO_ACTIVE_LOW>` binding in the codec overlay.

## RP2040 (U3)

KiCad symbol `MCU_RaspberryPi:RP2040`; pin numbering matches the RP2040 datasheet (QFN-56).

| Pin | Name | Net | Notes |
|---|---|---|---|
| 1 | IOVDD | +3V3 | |
| 2-9 | GPIO0-GPIO7 | (NC) | Reserved for future use |
| 10 | IOVDD | +3V3 | |
| 11-17 | GPIO8-GPIO14 | (NC) | Reserved |
| 18 | GPIO15 | RINGER_IN2 | To DRV8871 IN2 |
| 19 | TESTEN | GND | Normal operation |
| 20 | XIN | XIN | 12 MHz crystal input |
| 21 | XOUT | XOUT_MCU | Drives XOUT side of crystal through R9 (1k series damping) to net `XOUT` at Y1 |
| 22 | IOVDD | +3V3 | |
| 23 | DVDD | DVDD_1V1 | RP2040 core supply from internal regulator |
| 24 | SWCLK | SWD_SWCLK | To Pi GPIO25 |
| 25 | SWDIO | SWD_SWDIO | To Pi GPIO24 |
| 26 | RUN | RUN | Pulled up to +3V3 via R5 10k, decoupled to GND via C35 100 nF (POR filter, Pico reference) |
| 27 | GPIO16 | LED_OUT | Via R1 220 ohm to status LED on the LED connector |
| 28, 29 | GPIO17, GPIO18 | (NC) | Reserved |
| 30 | GPIO19 | RINGER_IN1 | To DRV8871 IN1 |
| 31 | GPIO20 | HOOK_SW | Hook sense input (SW1 pole 1) |
| 32 | GPIO21 | KP_ROW2 | Keypad row 2 |
| 33 | IOVDD | +3V3 | |
| 34 | GPIO22 | KP_COL2 | Keypad column 2 |
| 35 | GPIO23 | KP_COL1 | Keypad column 1 |
| 36 | GPIO24 | KP_COL0 | Keypad column 0 |
| 37 | GPIO25 | KP_ROW3 | Keypad row 3 |
| 38 | GPIO26/ADC0 | KP_ROW1 | Keypad row 1 |
| 39 | GPIO27/ADC1 | KP_ROW0 | Keypad row 0 |
| 40 | GPIO28/ADC2 | UART_RX_PI | UART0 TX alt to Pi GPIO15 (RXD0) |
| 41 | GPIO29/ADC3 | UART_TX_PI | UART0 RX alt from Pi GPIO14 (TXD0) |
| 42 | IOVDD | +3V3 | |
| 43 | ADC_AVDD | +3V3 | C33 100 nF local decap (sec 2.9.5); short trace, reference ground plane |
| 44 | VREG_VIN | +3V3 | Internal LDO input |
| 45 | VREG_VOUT | DVDD_1V1 | Internal LDO output (1.1 V), drives DVDD pins |
| 46 | USB_DM | USB_DM | Unconnected (no USB connector on this board) |
| 47 | USB_DP | USB_DP | Unconnected (no USB connector on this board) |
| 48 | USB_VDD | +3V3 | |
| 49 | IOVDD | +3V3 | |
| 50 | DVDD | DVDD_1V1 | |
| 51 | QSPI_SD3 | QSPI_SD3 | Flash IO3 |
| 52 | QSPI_SCLK | QSPI_SCLK | Flash CLK |
| 53 | QSPI_SD0 | QSPI_SD0 | Flash DI/IO0 |
| 54 | QSPI_SD2 | QSPI_SD2 | Flash WP/IO2 |
| 55 | QSPI_SD1 | QSPI_SD1 | Flash DO/IO1 |
| 56 | ~QSPI_SS | QSPI_SS | Flash /CS; SW2 grounds this for BOOTSEL |
| 57 | GND (exposed pad) | GND | Die thermal pad |

UART naming is Pi-centric: `UART_TX_PI` is the wire on which the Pi's TX signal travels (Pi GPIO14 out to RP2040 GPIO29 RX in). `UART_RX_PI` is the wire on which the Pi's RX signal travels (RP2040 GPIO28 TX out to Pi GPIO15 RX in).

## W25Q16JV flash (U4)

Winbond SPI NOR flash, SOIC-8 (5.3x5.3 mm) package.

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

No external pullup on `QSPI_SS`. The RP2040 bootrom actively drives /CS within nanoseconds of reset, and the official Pico plus the Raspberry Pi Press reference designs omit it. SW2 (6 mm tact) momentarily grounds `QSPI_SS` during power-on for BOOTSEL entry. C34 (100 nF, 0402) decouples the flash VCC directly at U4.8 per the Winbond W25Q16JV datasheet.

## DRV8871 ringer driver (U2) and XL6019 boost (U10)

The bell is driven by a DRV8871DDA H-bridge running off the on-board ~37 V `VBOOST` rail. The RP2040 alternates IN1/IN2 to produce an AC current waveform in the coil; the DRV8871 decodes the full-bridge automatically.

DRV8871 (U2):

| Pin | Name | Net | Notes |
|---|---|---|---|
| 1 | GND | GND | |
| 2 | IN2 | RINGER_IN2 | From RP2040 GPIO15 |
| 3 | IN1 | RINGER_IN1 | From RP2040 GPIO19 |
| 4 | ILIM | Net-(U2-ILIM) | To R2 33k to GND |
| 5 | VM | VBOOST | Motor supply, ~37 V from U10 |
| 6 | OUT1 | BELL_A | To BELL connector pin 1 |
| 7 | GND | GND | |
| 8 | OUT2 | BELL_B | To BELL connector pin 2 |
| EP | GND | GND | Thermal pad |

R2 (33k) sets the DRV8871 current chopping threshold per TI datasheet equation 1: I_TRIP = 64 / 33 ~= 1.94 A typical, intended as a fault trip well above the 150-400 mA peak ringing current.

XL6019 boost (U10):

| Pin | Name | Net |
|---|---|---|
| 1 | GND | GND |
| 2 | VIN | +5V |
| 3 | SW | SW_NODE |
| 4 | VIN | +5V |
| 5 | FB | FB_NODE |
| 6 | tab | SW_NODE |

The metal tab (pad 6) is on SW_NODE, NOT GND. Wiring it to GND would dead-short the switch node.

## TLV320AIC3104 codec (U6)

TI stereo audio codec in VQFN-32 5x5 mm (RHB0032E). KiCad symbol matches datasheet pin naming. Pin map below is read from the PCB pad-to-net assignments.

| Pin | Name | Net | Notes |
|---|---|---|---|
| 1 | MCLK | CODEC_MCLK | From Pi GPCLK0 (GPIO4), optional fallback |
| 2 | BCLK | CODEC_BCLK | I2S bit clock from Pi |
| 3 | WCLK | CODEC_WCLK | I2S word clock from Pi |
| 4 | DIN | CODEC_DIN | Audio data in from Pi |
| 5 | DOUT | CODEC_DOUT | Audio data out to Pi |
| 6 | DVSS | GND | |
| 7 | IOVDD | +3V3 | C40 close-in decap |
| 8 | SCL | CODEC_SCL | I2C clock from Pi GPIO3 |
| 9 | SDA | CODEC_SDA | I2C data from Pi GPIO2 |
| 10 | MIC1LP | /codec/MIC_P_INT | Mic hot input after C46 coupling cap |
| 11 | MIC1LM | /codec/MIC_N_INT | Mic return after C47 coupling cap (to GND) |
| 12 | MIC1RP | /codec/MIC1RP_INT | Unused input, terminated via C49 100 nF to GND |
| 13 | MIC1RM | /codec/MIC1RM_INT | Unused input, terminated via C50 |
| 14 | MIC2L | /codec/MIC2L_INT | Unused input, terminated via C51 |
| 15 | MICBIAS | /codec/MICBIAS | Bias generator output; through R10 to MIC_FROM_SW, bypassed by C48 |
| 16 | MIC2R | /codec/MIC2R_INT | Unused input, terminated via C52 |
| 17 | AVDD | +3V3 | |
| 18 | DRVDD | +3V3 | C41 close-in decap |
| 19 | HPLOUT | EAR_P | Capless BTL + side to handset earpiece |
| 20 | HPLCOM | EAR_N | Capless BTL - side to handset earpiece |
| 21 | DRVSS | GND | |
| 22 | HPRCOM | (NC) | Right channel unused |
| 23 | HPROUT | (NC) | |
| 24 | DRVDD | +3V3 | C42 close-in decap |
| 25 | AVDD | +3V3 | C43 close-in decap |
| 26 | AVSS | GND | |
| 27-30 | LEFT/RIGHT LOP/LOM | (NC) | Line outs unused |
| 31 | ~RESET | CODEC_RESET | Pi GPIO22 drives via Linux DT `reset-gpios`; held high by R11, ESD cap C53 |
| 32 | DVDD | /codec/+1V8 | Digital core supply from external U7 LDO |
| 33 | GND (exposed pad) | GND | Die thermal pad / analog-digital GND bond |

Reset design: R11 (10k) pulls `/RESET` to +3V3. C53 (1 nF) provides ESD protection. The Pi drives `/RESET` low then releases during Linux codec driver probe, so the pullup is a safety net rather than the primary reset source.

The codec DVDD (pin 32) is fed from the external U7 XC6206P182 LDO on the `/codec/+1V8` net. The codec's own internal DVDD LDO is left disabled in software so the two do not fight (see `SOFTWARE_CONFIG.md`).

## Audio analog paths

### J8 handset cable pinout

J8 mates to the stock Sangyn Retro 2500 handset RJ9 cable via a 4-pin JST ZH adapter. The pin order below matches that cable directly, so no per-unit wire swap is needed.

| J8 pin | Net | Stock cable wire | Handset function |
|---|---|---|---|
| 1 | `MIC_HOT` | Black | Mic + |
| 2 | `GND` | Yellow | Mic return |
| 3 | `EAR_P` | Red | Earpiece |
| 4 | `EAR_N` | Green | Earpiece |

Mic pair is on the inner-left pins (1, 2); earpiece pair is on the inner-right pins (3, 4). V2 shipped with the mic/earpiece pairs swapped (assumed mic on outer pins 1, 4), which tied `EAR_P` to the mic return wire and coupled playback into the mic capsule. See `hardware/pcb/v2/ERRATA.md` section 1.

### Mic path and SW1 series kill

The mic signal leaves J8 as `MIC_HOT`, runs through SW1 pole 2 (the cradle kill switch), and returns as `MIC_FROM_SW`.

```
                              ┌── C3 100 nF ── GND   (RFI filter at the connector)
                              │
 Handset MIC+  →  J8.1 ── MIC_HOT ── SW1.5 ──(pole 2)── SW1.4 ── MIC_FROM_SW ──┬── C46 0.47 uF ── U6.10 MIC1LP
                                                                               │                (/codec/MIC_P_INT, DC block)
                                                                  R10 2.2k ────┤
                                                                               │
                                                       U6.15 MICBIAS (/codec/MICBIAS)
                                                                  (parallel bias inject)

 U6.11 MIC1LM ── C47 0.47 uF ── GND   (/codec/MIC_N_INT, differential return)
 Handset MIC-  →  J8.2 ── GND
```

SW1 pole 2 is the mic kill: when the handset is on-hook the pole opens and MIC_HOT is disconnected from MIC_FROM_SW, so the mic is dead. This is a hardware privacy property; no GPIO can override it. Mic bias from `U6.15 MICBIAS` is injected into the post-switch `MIC_FROM_SW` node via R10 (2.2k series, parallel branch, not in the signal path), and the same node is AC-coupled into the codec's `MIC1LP` input through C46 (0.47 uF). C3 (100 nF) is an RFI suppression cap across the raw mic signal at the connector.

### Earpiece BTL path

```
 U6.19 HPLOUT ──── EAR_P ──── J8.3   (handset earpiece +)
 U6.20 HPLCOM ──── EAR_N ──── J8.4   (handset earpiece -)
```

The TLV320AIC3104 drives the earpiece in **capless Bridge-Tied Load** mode: both `HPLOUT` and `HPLCOM` are biased to mid-rail (~1.65 V), and the ~140 ohm earpiece sees only the AC difference between them. No DC-blocking capacitor is needed because there is no DC current through the load. This gives ~28 mW into 140 ohm from a 3.3 V rail, roughly 4x more than a single-ended capacitor-coupled output could deliver from the same supply.

## Crystal oscillator

12 MHz fundamental crystal between `U3.20 XIN` and `U3.21 XOUT`. Y1 is explicitly specified as **Abracon ABM8-272-T3** (CL = 10 pF, ESR <= 50 ohm, 3.2 x 2.5 mm 4-pad SMD) per RP2040 datasheet sec 2.16.1.1, which states the Pico's clock tree has been tuned specifically for this part. Substituting a different crystal is a blocker without re-validation.

Load caps C5/C6 = **15 pF C0G 0402** computed as `C_load = 2*(CL - C_stray)` with CL = 10 pF and C_stray ~= 2.5 pF.

A 1 kohm series damping resistor (**R9**) sits on the XOUT side between the RP2040 and the crystal. This is mandated by Raspberry Pi's *Hardware design with RP2040* guide sec 2 for designs running IOVDD = 3.3 V and limits the drive level into the crystal. Do not omit it or substitute 0 ohm.

```
 U3.20 XIN ────────────────────── Y1.1 (Xi)
                                   Y1 ABM8-272-T3 (12 MHz, CL=10 pF)
 U3.21 XOUT ── R9 1k ──────────── Y1.3 (Xo)
 C5 15 pF ── GND                   C6 15 pF ── GND
                                   Y1.2, Y1.4 ── GND (case shield)
```

**Schematic symbol:** `Device:Crystal_GND24` (NOT `Device:Crystal`). The 2-pin `Device:Crystal` symbol is incompatible with the 4-pad `Crystal_SMD_3225-4Pin_3.2x2.5mm` footprint used by the ABM8: pads 1 and 3 are diagonal signal pads (Xi/Xo), pads 2 and 4 are the case shield (GND). Using the 2-pin symbol lands net XOUT on footprint pad 2 (which is GND, not Xo), leaving the actual Xo pin (pad 3) floating, and the crystal never oscillates. Keep `Device:Crystal_GND24`; do not substitute.

## RP2040 cluster per-pin decoupling placement

The schematic places the RP2040 decoupling caps (C12-C16, C28 on IOVDD; C29, C30 on DVDD_1V1; C31 on VREG_VIN; C32 on USB_VDD; C33 on ADC_AVDD; C34 on W25Q16 VCC) on the correct rails, but the KiCad schematic symbol collapses all IOVDD pins and both DVDD pins into single nodes, so electrical connectivity alone does not prove each cap is physically adjacent to its named pin after PCB layout. RP2040 DS sec 2.9 requires each cap within a few millimetres of the pin it decouples. The per-pin target mapping is derived from the Raspberry Pi Minimal-KiCAD reference geometry (3.05 mm uniform radial decoupling ring around the QFN) and enforced at placement time.

## USB

There is no USB connector on this board and no USB termination resistors. `USB_DM` (U3.46) and `USB_DP` (U3.47) are left unconnected by design. USB_VDD (U3.48) is still tied to +3V3 with C32 because the internal USB PHY remains powered. Firmware is flashed by the Pi over SWD; BOOTSEL is entered with SW2.

## SWD programming

The Pi programs the RP2040 via bit-banged SWD over two GPIO pins using openocd's `raspberrypi-native` configuration. No dedicated SWD header is present; debugging is done by wiring the Pi to the RP2040 through the 40-pin header.

- `SWD_SWDIO`: U3 pin 25 to Pi header pin 18 (Pi GPIO24)
- `SWD_SWCLK`: U3 pin 24 to Pi header pin 22 (Pi GPIO25)

## Keypad, hook, LED, and BOOTSEL

```
 KEYPAD Conn_01x07  →  U3 RP2040
   KEYPAD.1 KP_COL0     U3.36 GPIO24
   KEYPAD.2 KP_COL1     U3.35 GPIO23
   KEYPAD.3 KP_COL2     U3.34 GPIO22
   KEYPAD.4 KP_ROW3     U3.37 GPIO25
   KEYPAD.5 KP_ROW2     U3.32 GPIO21
   KEYPAD.6 KP_ROW1     U3.38 GPIO26
   KEYPAD.7 KP_ROW0     U3.39 GPIO27

 SW1 hook sense (pole 1)
   SW1.2 HOOK_SW  →  U3.31 GPIO20
   SW1.3 GND      →  grounds HOOK_SW on-hook; off-hook opens it and the RP2040 internal pull-up reads high
   SW1.1          →  unused

 Status LED
   U3.27 GPIO16 →  LED_OUT  →  R1 220 ohm  →  LED.2 (LED_A, anode)
                                              LED.1 → GND (cathode)

 SW2 BOOTSEL
   SW2  QSPI_SS to GND   (hold during power-on to enter the RP2040 bootrom)
```

## Ringer

The RP2040 drives two GPIOs into the DRV8871 H-bridge inputs to produce an AC current through the bell coil, powered from the ~37 V VBOOST rail.

```
 U3.30 GPIO19 → RINGER_IN1 → U2.3 IN1
 U3.18 GPIO15 → RINGER_IN2 → U2.2 IN2
 U2.6 OUT1 → BELL_A → BELL.1 (bell coil +)
 U2.8 OUT2 → BELL_B → BELL.2 (bell coil -)
 U2.4 ILIM → R2 33k → GND   (fault trip ~1.94 A)
 U2.5 VM <- VBOOST (~37 V from U10)
```

Bell ringing is driven by alternating PWM on IN1 and IN2 from the RP2040 firmware; the DRV8871 internally sequences the full-bridge.

## Mounting holes

Three M3 mechanical mounts, positions locked by the phone enclosure:

| Ref | Position (mm) | Footprint |
|---|---|---|
| MH1 | (23.4, 47.96) | MountingHole_3.2mm_M3 |
| MH2 | (82.3, 61.16) | MountingHole_3.2mm_M3 |
| MH3 | (87.4, 30.46) | MountingHole_3.2mm_M3 |

No electrical connections.

## References

- [TLV320AIC3104 datasheet (SLAS510)](https://www.ti.com/lit/ds/symlink/tlv320aic3104.pdf)
- [TI DRV8871 datasheet](https://www.ti.com/lit/ds/symlink/drv8871.pdf)
- [Winbond W25Q16JV datasheet](https://www.winbond.com/resource-files/w25q16jv%20spi%20revg%2003222018%20plus.pdf)
- [Raspberry Pi 40-pin header pinout](https://pinout.xyz/)
- [RP2040 datasheet](https://datasheets.raspberrypi.com/rp2040/rp2040-datasheet.pdf)
