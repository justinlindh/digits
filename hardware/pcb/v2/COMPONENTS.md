# Digits Carrier Board V2 -- Component Reference

Every component on the board, what it does, and why it exists.

---

## Power Input and Protection

| Ref | Part | Package | LCSC | Purpose |
|-----|------|---------|------|---------|
| J3 | Barrel Jack 2.1x5.5mm | THT | - | 12V DC power input from wall adapter |
| F1 | 1.5A PTC Fuse | 1210 | C369159 | Resettable overcurrent protection on +12V input. Trips at 1.5A, resets when fault clears |

## 12V to 5V Buck Converter

| Ref | Part | Package | LCSC | Purpose |
|-----|------|---------|------|---------|
| U1 | LM2596S-5 | TO-263-5 | C347421 | 5V 3A switching step-down regulator. Converts 12V input to 5V rail for the Pi and board |
| C1 | 680uF 25V electrolytic | 10x10.5mm | C976031 | Input bulk capacitor for U1. Absorbs input ripple and provides energy during switching transients |
| C2 | 220uF 25V electrolytic | 8x6.5mm | C2895286 | Output bulk capacitor for U1. Smooths 5V output ripple |
| L1 | 33uH inductor | 12x12mm | C9400 | Energy storage inductor for U1's buck topology. Sized per LM2596 datasheet for 5V/3A output |
| D1 | SS54 Schottky | SMA | C22452 | Freewheeling diode for U1. Provides current path for L1 when U1's switch is off. Schottky for low forward drop |

## 5V to 3.3V LDO

| Ref | Part | Package | LCSC | Purpose |
|-----|------|---------|------|---------|
| U5 | AMS1117-3.3 | SOT-223 | C6186 | 3.3V 1A linear regulator. Powers the RP2040 I/O, flash, and crystal from the 5V rail |
| C9 | 10uF 6.3V X5R | 0603 | C15525 | Input capacitor for U5. Stabilizes 5V input to the LDO |
| C11 | 10uF 6.3V X5R | 0603 | C15525 | Output capacitor for U5. Required for LDO stability, smooths 3.3V output |

## RP2040 Microcontroller

| Ref | Part | Package | LCSC | Purpose |
|-----|------|---------|------|---------|
| U3 | RP2040 | QFN-56 | C2040 | Dual-core ARM Cortex-M0+ MCU. Runs the phone firmware: keypad scanning, audio routing, UART to Pi, LED control, hook switch detection |
| C12-C16 | 5x 100nF 50V X7R | 0805 | C49678 | Bypass/decoupling capacitors for U3. One per IOVDD/DVDD pin per RP2040 datasheet. Filters high-frequency noise from power pins |
| C10 | 10uF 6.3V X5R | 0603 | C15525 | Bulk bypass for RP2040. Provides local charge reservoir for transient current demands |
| R5 | 10k | 0402 | C25744 | Pull-up on RUN pin. Keeps RP2040 out of reset |

## RP2040 Crystal Oscillator

| Ref | Part | Package | LCSC | Purpose |
|-----|------|---------|------|---------|
| Y1 | 12MHz crystal | 3225 | C9002 | Clock source for RP2040. The PLL multiplies this to the CPU operating frequency (typically 125MHz) |
| C5, C6 | 2x 22pF 50V C0G | 0402 | C1555 | Crystal load capacitors. Matched to Y1's specified load capacitance for accurate oscillation |

## QSPI Flash

| Ref | Part | Package | LCSC | Purpose |
|-----|------|---------|------|---------|
| U4 | W25Q16JVSSIQ | SOIC-8 | C131025 | 2MB SPI NOR flash. Stores RP2040 firmware. Connected via QSPI (4-bit) bus for fast boot |
| R6 | 10k | 0402 | C25744 | Pull-up on QSPI_SS (chip select). Holds flash CS high during boot to prevent bus glitches |

## USB

| Ref | Part | Package | LCSC | Purpose |
|-----|------|---------|------|---------|
| R3, R4 | 2x 27 ohm | 0402 | C25100 | USB series termination resistors on DP/DM. Required by USB spec for impedance matching on Full Speed (12Mbps) |

## Motor Driver (Ringer)

| Ref | Part | Package | LCSC | Purpose |
|-----|------|---------|------|---------|
| U2 | DRV8871DDAR | SOIC-8-EP | C75864 | H-bridge motor driver. Drives the phone's ringer mechanism bidirectionally |
| C4 | 100nF 50V X7R | 0805 | C49678 | Decoupling capacitor for U2 |
| R2 | 0.1 ohm 2W | 2512 | C160587 | Current sense resistor for U2. The DRV8871 measures voltage across this to limit motor current |

## Connectors

| Ref | Part | Package | Purpose |
|-----|------|---------|---------|
| J1 | 2x20 Female Header 2.54mm | THT | Pi Zero 2 W GPIO header. Carries UART, SWD, power, keypad signals between Pi and RP2040 |
| J4 | JST ZH 7-pin | SMD | Keypad connector. KP_ROW0-3, KP_COL0-2 for the 4x3 matrix |
| J6 | JST ZH 2-pin | SMD | LED connector. LED_OUT signal and ground |
| J7 | Phoenix 2-pos screw terminal | THT | Bell/ringer output. BELL_A and BELL_B from U2 to the ringer mechanism |
| J8 | JST ZH 4-pin | SMD | Handset connector. MIC_HOT, EAR_P, EAR_N, MIC_GND |
| J9 | 1x3 pin header 2.54mm | THT | Microphone input. MIC_HOT, MIC_FROM_SW, MIC_GND |
| J10 | 1x2 pin header 2.54mm | THT | Earpiece output. EAR_P, EAR_N |

## Audio Codec

| Ref | Part | Package | LCSC | Purpose |
|-----|------|---------|------|---------|
| U6 | TLV320AIC3104IRHBR | QFN-32 (5x5mm) | C181753 | Onboard audio codec. Replaces Codec Zero HAT. I2S ADC/DAC with mic preamp and headphone amp |
| C17 | 10uF 6.3V X5R | 0402 | C15525 | AVDD decoupling (U6 pin 17) |
| C18 | 100nF 50V X7R | 0805 | C49678 | DRVDD decoupling (U6 pin 10) |
| C19 | 10uF 6.3V X5R | 0402 | C15525 | DRVDD decoupling (U6 pin 16) |
| C20 | 100nF 50V X7R | 0805 | C49678 | IOVDD decoupling (U6 pin 31) |
| C21 | 100nF 50V X7R | 0805 | C49678 | AVDD additional decoupling (U6 pin 17) |
| C22 | 10uF 6.3V X5R | 0402 | C15525 | AVSS analog ground bulk capacitor |
| C23 | 1uF 25V X7R | 0402 | C52923 | AC coupling, mic signal to MIC1LP input |
| C24 | 10uF 6.3V X5R | 0402 | C15525 | AC coupling, HPLOUT to earpiece |
| C25 | 10uF 6.3V X5R | 0402 | C15525 | DVDD decoupling. Internal 1.8V LDO output -- do NOT connect to +3.3V |
| R7 | 10k | 0402 | C25744 | RESET pullup to +3.3V. Keeps codec out of reset |
| R8 | 2.2k | 0402 | C25879 | MICBIAS series resistor. Provides DC bias to electret mic element |
| C26 | 470nF 25V X7R | 0402 | C1046 | Unused analog input grounding cap. Ties MIC1RP/MIC1RM/MIC2L/MIC2R to GND to prevent noise coupling |
| C27 | 1nF 50V C0G | 0402 | C52923 | RESET pin ESD protection. Prevents spurious resets from ESD events |

## Other

| Ref | Part | Package | LCSC | Purpose |
|-----|------|---------|------|---------|
| SW1 | 6mm tact switch | THT | - | Hook switch. Detects handset on/off hook. Connected to RP2040 GPIO |
| R1 | 220 ohm | 0805 | C17557 | Current limiting resistor for LED output (LED_OUT to J6) |
| C3 | 100nF 50V X7R | 0805 | C49678 | Filter capacitor on microphone circuit (MIC_HOT net) |
| H1, H2, H3 | M3 mounting holes | - | - | Mechanical mounting to phone enclosure. Positions locked by physical constraints |

## Power Rails

| Rail | Voltage | Source | Consumers |
|------|---------|--------|-----------|
| +12V | 12V | J3 barrel jack via F1 fuse | U1 (buck input), U2 (motor driver) |
| +5V | 5V | U1 LM2596S-5 | Pi Zero 2 W (via J1), U5 (LDO input) |
| +3V3 | 3.3V | U5 AMS1117-3.3 | U3 (RP2040 IOVDD), U4 (flash), Y1 circuit, U6 (codec AVDD/DRVDD/IOVDD) |
| DVDD_1V1 | 1.1V | U3 internal VREG | U3 (RP2040 core/DVDD) |
| CODEC_DVDD | 1.8V | U6 internal LDO | U6 (codec digital core) |
| GND | 0V | Common return | All components, copper pour on both layers |

## Signal Summary

| Signal | From | To | Purpose |
|--------|------|----|---------|
| UART_TX_PI | U3 GPIO0 | J1 (Pi) | RP2040 TX to Pi RX, serial communication |
| UART_RX_PI | J1 (Pi) | U3 GPIO1 | Pi TX to RP2040 RX, serial communication |
| SWD_SWDIO | J1 (Pi GPIO) | U3 pin 25 | Firmware flashing data line |
| SWD_SWCLK | J1 (Pi GPIO) | U3 pin 24 | Firmware flashing clock line |
| KP_ROW0-3 | U3 GPIO2-5 | J4 | Keypad matrix row scan outputs |
| KP_COL0-2 | U3 GPIO6-8 | J4 | Keypad matrix column read inputs |
| HOOK_SW | SW1 | U3 GPIO10 | Hook switch state (on/off hook) |
| LED_OUT | U3 GPIO14 | R1, J6 | Indicator LED drive |
| RINGER_IN1/2 | U3 GPIO11-12 | U2 | Motor driver control for ringer |
| BELL_A/B | U2 OUT1/2 | J7 | Ringer mechanism drive signals |
| ILIM | U2/R2 junction | - | Current sense feedback internal to motor driver |
| USB_DP/DM | U3 GPIO | R3/R4 | USB data lines (Full Speed) |
| QSPI_* | U3 | U4 | 4-bit flash interface (SCLK, SD0-3, SS) |
| XIN/XOUT | Y1 | U3 | 12MHz crystal oscillator |
| MIC_HOT | J8/J9 | Audio circuit | Microphone hot signal |
| MIC_FROM_SW | J9 | C23, U6 MIC1LP | Mic signal after kill switch, AC-coupled to codec |
| EAR_P/N | U6 HPLOUT via C24 | J8/J10 | Earpiece audio from codec headphone amp |
| CODEC_SDA | J1 (Pi GPIO2) | U6 pin 1 | I2C data for codec control |
| CODEC_SCL | J1 (Pi GPIO3) | U6 pin 32 | I2C clock for codec control |
| CODEC_BCLK | J1 (Pi GPIO18) | U6 pin 26 | I2S bit clock |
| CODEC_WCLK | J1 (Pi GPIO19) | U6 pin 27 | I2S word clock (LRCLK) |
| CODEC_DIN | J1 (Pi GPIO21) | U6 pin 28 | I2S data, Pi TX to codec RX |
| CODEC_DOUT | U6 pin 29 | J1 (Pi GPIO20) | I2S data, codec TX to Pi RX |
| CODEC_MCLK | J1 (Pi GPIO4) | U6 pin 25 | Master clock (optional, PLL-from-BCLK default) |
| MICBIAS_OUT | U6 pin 7 | R8 | Mic bias voltage for electret element |
| RUN | R5 pull-up | U3 pin 26 | RP2040 reset (active low, held high) |
