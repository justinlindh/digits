# Digits Carrier Board V2 -- Component Reference

Every component on the board, what it does, what it connects to, and why it exists.

---

## Power Input and Protection

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| J3 | Barrel Jack 2.1x5.5mm | THT | - | +12V rail, GND | 12V DC power input from wall adapter. The phone runs on a 12V supply because the ringer bell needs 12V and the LM2596 buck converter steps it down for everything else. |
| F1 | 1.5A PTC Fuse | 1210 | C70102 | J3 -> +12V rail | Resettable overcurrent protection on the +12V input. Trips at 1.5A to protect the board from shorts or excessive draw. Resets automatically when the fault clears. Placed between J3 and all downstream 12V consumers. |

## 12V to 5V Buck Converter

Converts the 12V wall adapter input to 5V for the Raspberry Pi and the 3.3V LDO. Uses the Texas Instruments LM2596 in a standard application circuit per the datasheet.

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| U1 | LM2596S-5 | TO-263-5 | C347421 | +12V in, +5V out, L1, D1, C1, C2 | 5V 3A fixed-output switching step-down regulator. Converts 12V to 5V at up to 3A. The fixed-output version eliminates the feedback resistor divider. |
| C1 | 470uF 25V electrolytic | 10x10.2mm SMD | C248607 | +12V rail, GND | Input bulk capacitor for U1 (Panasonic EEEFK1E471P, FK series HIGH TEMP REFLOW, 260°C rated, 80mΩ ESR). Absorbs input voltage ripple and provides instantaneous energy during switching transients. 470uF matches TI's LM2596 datasheet reference value (SNVS124 Fig 9-13). **Previously C976031 (DMBJ RVT1E681M1010, 680uF) — rejected by JLCPCB due to 235°C reflow limit below lead-free reflow profile.** |
| C2 | 220uF 25V electrolytic | 8x6.5mm SMD | C2895286 | +5V rail, GND | Output bulk capacitor for U1. Smooths the 5V output ripple. 220uF is the LM2596 datasheet minimum for stable regulation. |
| L1 | 33uH shielded inductor | 12x12mm SMD | C9400 | U1 output pin, D1/C2 junction | Energy storage inductor for the buck topology. During U1's on-time, current ramps up through L1 storing energy in the magnetic field. During off-time, L1 releases energy through D1 to maintain current flow. 33uH is sized per the LM2596 datasheet for 5V/3A output from 12V input. Shielded to reduce EMI. |
| D1 | SS54 Schottky diode | SMA (DO-214AC) | C22452 | L1/U1 junction, GND | Freewheeling (catch) diode for the buck converter. When U1's internal switch turns off, L1's magnetic field collapses and current must continue flowing -- D1 provides that path. Schottky type for low forward voltage drop (0.5V vs 0.7V for standard diodes), improving efficiency and reducing heat. 5A/40V rated, matching the LM2596's current capability. |

## 5V to 3.3V LDO

Linear regulator converting the 5V buck output to 3.3V for the RP2040 I/O, flash, crystal circuit, and audio codec.

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| U5 | AMS1117-3.3 | SOT-223 | C6186 | +5V in, +3V3 out | 3.3V 1A fixed-output low-dropout regulator. Replaces the Pico H module's onboard RT6150 regulator. Dropout voltage is ~1.1V, so the 5V input provides comfortable headroom. Powers RP2040 IOVDD/DVDD/USB_VDD/ADC_AVDD, W25Q16 flash, and TLV320AIC3104 codec (AVDD/DRVDD/IOVDD). |
| C9 | 10uF 6.3V X5R | 0603 | C109455 | +3V3 rail (U5 output), GND | Output bulk cap for U5 AMS1117 LDO. Required by the AMS1117 datasheet for stable operation -- without it the LDO can oscillate. Also provides local charge reservoir for transient loads on the 3.3V rail. |
| C11 | 10uF 6.3V X5R | 0603 | C109455 | +5V rail (U5 input), GND | Input bulk cap for U5. Stabilizes the 5V supply at the LDO's input pin. |

## RP2040 Microcontroller

Bare RP2040 chip (replacing the Pico H module from V1) with its minimal support circuit: flash, crystal, bypass caps, and pullup resistors.

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| U3 | RP2040 | QFN-56 (7x7mm) | C2040 | Everything | Dual-core ARM Cortex-M0+ microcontroller. Runs the phone firmware: scans the 4x3 keypad matrix (GPIO21-27 split across columns/rows per NET_TOPOLOGY.md § RP2040 pin table), detects hook switch state (GPIO20), drives the ringer bell via DRV8871 (GPIO19 IN1, GPIO15 IN2), controls the indicator LED (GPIO16), and communicates with the Pi over UART (GPIO28 TX, GPIO29 RX). Also provides SWD debug pins (pin 24 SWCLK, pin 25 SWDIO) for firmware flashing from the Pi. |
| C12-C16, C28 | 6x 100nF 50V X7R | 0402 | C1525 | +3V3 to GND, one per RP2040 IOVDD pin (1, 10, 22, 33, 42, 49) | Per-pin high-frequency decoupling for U3 IOVDD pins per RP2040 datasheet §2.9.1. The RP2040 has 6 IOVDD pins and the datasheet mandates one 100 nF cap near each. |
| C29, C30 | 2x 100nF 50V X7R | 0402 | C1525 | DVDD_1V1 to GND, at U3 pins 23 and 50 | Per-pin decoupling for U3 DVDD pins per RP2040 §2.9.2. DVDD_1V1 is the RP2040's internal 1.1V rail sourced at VREG_VOUT (pin 45). |
| C10 | 1uF 10V X7R | 0402 | C52923 | DVDD_1V1 at U3.45 VREG_VOUT to GND | Bulk cap on the DVDD_1V1 rail at the internal regulator output. Matches the Pico reference design. Previous rev spec'd 10uF 0603 which is overkill and wrong package. |
| C31 | 1uF 10V X7R | 0402 | C52923 | +3V3 to GND, at U3.44 VREG_VIN | Local cap at VREG_VIN per RP2040 §2.9.3 ("a 1 µF capacitor should be connected between VREG_VIN and ground close to the chip's VREG_VIN pin"). |
| C32 | 100nF 50V X7R | 0402 | C1525 | +3V3 to GND, at U3.48 USB_VDD | USB_VDD decap per §2.9.4. |
| C33 | 100nF 50V X7R | 0402 | C1525 | +3V3 to GND, at U3.43 ADC_AVDD | ADC_AVDD decap per §2.9.5. Even though the ADC is unused on this board, the datasheet mandates the decap because ADC_AVDD powers internal bandgap and reset/brownout reference circuitry. |
| R5 | 10k 1% | 0402 | C25744 | RUN pin (U3 pin 26) to +3V3 | Pull-up resistor on the RUN (reset) pin. Keeps the RP2040 out of reset during normal operation. The RUN pin is active-low -- pulling it to GND resets the chip. Without R5, the pin could float and cause intermittent resets. |
| C35 | 100nF 50V X7R | 0402 | C1525 | RUN (U3 pin 26) to GND | POR filter cap on RUN. Together with R5 (10k) forms an RC with ~1 µs time constant that smooths the power-up ramp so the RP2040 releases reset cleanly after +3V3 stabilizes. Matches the official Pico reference schematic. |

## RP2040 Crystal Oscillator

Provides the 12MHz reference clock that the RP2040's PLL multiplies up to the operating frequency (typically 125MHz). Also used as the USB SOF clock reference.

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| Y1 | Abracon ABM8-272-T3 (12MHz, CL=10pF, ESR≤50Ω) | 3225 (3.2x2.5mm) | C9002 | U3 XIN (pin 20), XOUT (pin 21) | 12MHz crystal mandated by RP2040 datasheet §2.16.1.1 ("Raspberry Pi Pico has been specifically tuned for the specifications of the Abracon ABM8-272-T3"). Do not substitute. |
| C5 | 15pF 50V C0G | 0402 | -- | Y1.1 (Xi signal pad), GND | Crystal load cap (XIN side). `C_load = 2·(CL − C_stray) = 2·(10 − 2.5) = 15 pF` for CL = 10 pF and typical 2.5 pF stray. C0G dielectric for temperature stability. Y1.1 is the Xi signal pad per Abracon ABM8 pinout (pads 1/3 signal, 2/4 case GND). Previous rev spec'd 22pF which corresponds to no real crystal. |
| C6 | 15pF 50V C0G | 0402 | -- | Y1.3 (Xo signal pad), GND | Crystal load cap (XOUT side). Y1.3 is the Xo signal pad. The RP2040's XOUT drive reaches Y1.3 through the R9 damping resistor, NOT directly. |
| R9 | 1k 1% | 0402 | -- | U3.21 (XOUT) in series to Y1.3 (Xo pad) | XOUT series damping resistor. RPi "Hardware design with RP2040" §2 explicitly specifies this for IOVDD = 3.3 V designs. Limits crystal drive level and prevents overdrive. Net topology: U3.21 -> XOUT_MCU -> R9.1, R9.2 -> XOUT -> Y1.3 (Xo), C6.1. Y1.2 and Y1.4 are case GND, NOT signal. |

## QSPI Flash

External flash memory storing the RP2040's firmware. Connected via a 4-bit QSPI bus for fast boot.

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| U4 | W25Q16JVSSIQ | SOIC-8 | C131025 | U3 QSPI pins (SS, SCLK, SD0-3), +3V3, GND | 2MB SPI NOR flash. Stores the RP2040 firmware image (UF2 format). Connected via 4-bit QSPI for fast sequential reads during boot. The RP2040 boots by reading code from this chip into internal SRAM. |
| C34 | 100nF 50V X7R | 0402 | C1525 | +3V3 to GND, at U4.8 VCC | Flash VCC decoupling cap. Winbond W25Q16JV datasheet requires a 100 nF ceramic as close as possible to pin 8. QSPI read bursts draw ~20 mA transients; without this cap they couple back into +3V3 and can corrupt XIP instruction fetches. All 3 Raspberry Pi Press RP2040 references (ch05, ch10, ch11) and the official Pico include this cap. |

**QSPI_SS has no external pullup.** The RP2040 bootrom drives /CS actively within nanoseconds of reset, and neither the Pico nor any of the 3 Raspberry Pi Press RP2040 references populate a pullup here. Earlier revs of this board had R6 = 10k pullup with a note about BOOTSEL — that note was incorrect: a pullup to +3V3 does not enable BOOTSEL mode (BOOTSEL on the Pico is a *button that grounds* /CS through a series resistor, relying on the RP2040's internal pull to restore it). BOOTSEL mode also requires a USB connector, which this board does not have; the RP2040 is programmed via SWD from the Pi. R6 has been removed.

## USB

Series termination resistors for USB Full Speed (12Mbps). No USB connector is populated -- flashing is done via SWD from the Pi. The resistors are present in case a USB connector is bodge-wired for debugging.

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| R3 | 27 ohm | 0402 | C25100 | U3 USB_DM (pin 46), USB D- pad | USB D- series termination. Required by the USB 2.0 specification for impedance matching on Full Speed signaling. |
| R4 | 27 ohm | 0402 | C25100 | U3 USB_DP (pin 47), USB D+ pad | USB D+ series termination. Matches R3. |

## Motor Driver (Ringer)

H-bridge motor driver that drives the phone's mechanical bell. The RP2040 generates a square wave on IN1/IN2 to make the bell hammer oscillate.

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| U2 | DRV8871DDAR | HTSOP-8 with EP | C75864 | +12V (VM pin 5), RP2040 GPIO19/15 (IN1/IN2 via RINGER_IN1/RINGER_IN2), R2 (ILIM pin 4), J7 (OUT1/OUT2 pins 6/8), GND | H-bridge motor driver rated 3.6A, 6.5-45V. Drives the phone's bell mechanism bidirectionally. Receives PWM/square wave from the RP2040 on IN1 and IN2 to alternate the bell hammer direction. Built-in current limiting via the ILIM pin and R2. Sleep mode when both inputs are low (50us wake-up, imperceptible). |
| C4 | 100nF 50V X7R | 0805 | C49678 | +12V (near U2 VM pin 5), GND | VCC decoupling capacitor for U2. Filters high-frequency noise on the 12V supply to U2. Note: for heavy bell loads, the PCB trace to C1 (470uF bulk cap) should be low-impedance for inductive kickback absorption. Separate ringer bulk cap C55 (47uF electrolytic) sits local to U2.5 for the same purpose. |
| R2 | 33k 1% | 2512 | C25779 | U2 ILIM (pin 4), GND | Current regulation resistor. Sets the DRV8871's current chopping threshold per TI DRV8871 datasheet Equation 1. The 33 kΩ value and the resulting I_TRIP (A) are TBD pending a fresh datasheet recalculation -- previous revisions of this doc recorded both ~0.94 A and ~1.94 A and the two are not reconcilable without the datasheet in hand. **Do not rely on the numeric I_TRIP until Phase B4 cluster audit re-derives it.** Minimum allowed R_ILIM is 15 kΩ per the datasheet. |

## Audio Codec

Onboard audio codec replacing the external Codec Zero HAT ($20/unit). Provides I2S ADC/DAC with built-in mic preamp and headphone amplifier. Controlled by the Pi over I2C, audio data streams over I2S.

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| U6 | TLV320AIC3104IRHBR | QFN-32 (5x5mm) | C181753 | I2S bus (Pi GPIO18-21), I2C bus (Pi GPIO2/3), mic circuit, earpiece, +3V3, GND | Low-power stereo audio codec with 24-bit ADC/DAC, 8-96kHz sample rates. Mic preamp with 59.5dB gain and AGC (ideal for telephony). Headphone amp drives the earpiece directly. Clocks derived from BCLK via internal PLL (no MCLK required, though GPIO4/GPCLK0 is wired as fallback). I2C address fixed at 0x18. Linux driver: `tlv320aic3x.c` (mainline). |
| C17 | 10uF 6.3V X5R | 0402 | C15525 | U6 AVDD (pin 17), GND | Analog supply decoupling. Placed close to U6's AVDD pin to filter noise on the analog power supply. 10uF provides bulk energy for the ADC/DAC analog circuits. |
| C21 | 100nF 50V X7R | 0805 | C49678 | U6 AVDD (pin 17), GND | Additional high-frequency AVDD bypass. 100nF responds faster than the 10uF C17 to filter high-frequency switching noise. |
| C18 | 100nF 50V X7R | 0805 | C49678 | U6 DRVDD (pin 10), GND | Driver supply decoupling (pin 10 side). The TLV320AIC3104 has two DRVDD pins -- each gets its own bypass cap for clean power to the headphone and line output drivers. |
| C19 | 10uF 6.3V X5R | 0402 | C15525 | U6 DRVDD (pin 16), GND | Driver supply bulk decoupling (pin 16 side). Provides energy reservoir for the headphone amp output stage. |
| C20 | 100nF 50V X7R | 0805 | C49678 | U6 IOVDD (pin 31), GND | Digital I/O supply decoupling. Filters noise on the 3.3V supply to the I2S and I2C interface logic. |
| C25 | 10uF 6.3V X5R | 0402 | C15525 | U6 DVDD (pin 24), GND | Internal LDO output decoupling. DVDD is the output of U6's internal 1.8V digital LDO -- do NOT connect to +3V3. This cap stabilizes the internal regulator's output. |
| C22 | 10uF 6.3V X5R | 0402 | C15525 | +3V3, GND (near AVSS pins) | Analog ground section bulk capacitor. Provides additional energy storage near the codec's analog ground pins (AVSS1/AVSS2) to reduce noise coupling between digital and analog sections. |
| C23 | 100nF 50V X7R | 0402 | C1525 | U6 MIC1LP (pin 2), MIC_FROM_SW net | AC coupling capacitor for mic input. Blocks the DC bias voltage from R8/MICBIAS while passing the audio signal from the electret mic through to the codec's mic preamp input. 100 nF into the codec's ~20 kΩ input impedance gives a -3 dB high-pass corner around 80 Hz, below the telephony band (300 Hz–3.4 kHz). |
| R7 | 10k 1% | 0402 | C25744 | U6 ~{RESET} (pin 23), +3V3 | RESET pullup. Keeps the codec out of hardware reset during normal operation. Active-low reset -- the driver can soft-reset via I2C register writes if needed. |
| C27 | 1nF 50V X7R | 0402 | C1523 | U6 ~{RESET} (pin 23), GND | RESET ESD protection capacitor. Filters transient voltage spikes on the RESET pin that could cause spurious resets. Recommended by TI application notes. |
| R8 | 2.2k | 0402 | C25879 | U6 MICBIAS (pin 7), MIC_FROM_SW net | MICBIAS series resistor. The codec's MICBIAS output provides ~2V DC through R8 to bias the electret microphone element. The mic needs DC bias to operate -- the electret's internal FET modulates current flow in response to sound pressure, creating the audio signal. R8 limits current and sets the bias point. |
| C26 | 1uF 10V X7R | 0402 | C52923 | U6 pins 4/5/6/8 (CODEC_UNUSED_IN net), GND | Unused analog input grounding capacitor. All four unused mic/line inputs (MIC1RP, MIC1RM, MIC2L, MIC2R) are tied together through this cap to ground. Per TI's application guidance, floating analog inputs can pick up noise that couples into the active signal path through the internal analog mux. The cap provides a low-impedance AC ground path. |

## Connectors

All connectors are through-hole, hand-soldered after JLCPCB assembly. They carry signals between the carrier board and the phone's physical components.

| Ref | Part | Package | Connects | Purpose |
|-----|------|---------|----------|---------|
| J1 | 2x20 Female Header 2.54mm | THT (B.Cu side) | Pi Zero 2 W GPIO | The main interconnect between the carrier board and the Raspberry Pi. Carries: UART (GPIO14/15 for serial communication with RP2040), SWD (for firmware flashing), I2S (GPIO18-21 for audio codec data), I2C (GPIO2/3 for codec control), GPCLK0 (GPIO4 for optional MCLK), +5V power to the Pi (pin 2), and GND. Mounted on the bottom side of the board so the Pi sits below. |
| J4 | JST ZH 7-pin | SMD | RP2040 GPIO2-8 | Keypad connector. Connects to the phone's 4x3 button matrix. Four row lines (KP_ROW0-3) are scanned as outputs by the RP2040; three column lines (KP_COL0-2) are read as inputs to detect which button is pressed. |
| J6 | JST ZH 2-pin | SMD | R1 (LED_OUT), GND | LED connector. Drives an indicator LED in the phone housing through R1 (220 ohm current limiter). The RP2040 controls the LED via GPIO14. |
| J7 | Phoenix 2-pos screw terminal (5mm) | THT | U2 OUT1/OUT2 (BELL_A, BELL_B) | Bell/ringer output. Connects to the phone's mechanical bell mechanism. The DRV8871 (U2) drives this bidirectionally to make the bell hammer oscillate. Screw terminals for easy connection to the existing bell wiring. |
| J8 | JST ZH 4-pin | SMD | MIC_HOT, EAR_P, EAR_N, MIC_GND | Handset connector. Four-wire cable to the phone handset carrying mic signal (MIC_HOT/MIC_GND) and earpiece audio (EAR_P/EAR_N). The mic signal routes through J9 (kill switch) before reaching the codec. Earpiece audio comes directly from the codec's headphone amp in capless BTL mode (no coupling cap needed -- see NET_TOPOLOGY.md "Earpiece BTL path"). |
| J9 | 1x3 pin header 2.54mm | THT | MIC_HOT, MIC_FROM_SW, MIC_GND | Microphone kill switch connector. A physical switch interrupts the mic signal path. When the switch is open, the mic is muted at the hardware level -- no software can override it. MIC_FROM_SW is the signal after the switch, which feeds both R8 (DC bias from codec) and C23 (AC-coupled to codec input). |
| J10 | 1x2 pin header 2.54mm | THT | EAR_P, EAR_N | Earpiece output connector. Directly parallels J8's earpiece pins for an alternative wiring path. EAR_P carries the AC-coupled audio from the codec headphone amp. EAR_N is the return path through the handset cable. |

## Other

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| SW1 | 6mm tact switch | THT (B.Cu side) | RP2040 GPIO20 (HOOK_SW = U3.31), GND | Hook switch. Detects whether the handset is on or off the cradle. When the handset is lifted, the switch opens and GPIO20 reads high (via internal pullup). When replaced, the switch closes and pulls GPIO20 to ground. The firmware uses this to answer/end calls. Position is fixed by the phone enclosure; mounted on the bottom copper layer. |
| R1 | 220 ohm | 0805 | C17557 | RP2040 GPIO16 (LED_OUT = U3.27), J6 | Current limiting resistor for the indicator LED. At 3.3V with a typical 2V LED forward voltage, R1 limits current to ~6mA -- bright enough to see, safe for the GPIO pin (max 12mA per pin on RP2040). |
| C3 | 100nF 50V X7R | 0805 | C49678 | MIC_HOT net, GND | Filter capacitor on the microphone hot signal. Provides a low-impedance path to ground for RF/high-frequency interference that might be picked up by the mic cable acting as an antenna. Prevents noise from entering the audio path. |
| MH1, MH2, MH3 | M3 mounting holes | 3.2mm | - | Mechanical only | Board mounting points. Three M3 holes whose positions are locked by the phone enclosure's screw posts. These cannot be moved without modifying the phone housing. |

---

## Power Rails

| Rail | Voltage | Source | Consumers | Decoupling |
|------|---------|--------|-----------|------------|
| +12V | 12V | J3 barrel jack via F1 fuse | U1 (buck input), U2 (motor driver VCC) | C1 (470uF bulk), C4 (100nF HF), C54 (100nF ringer HF), C55 (47uF ringer bulk) |
| +5V | 5V | U1 LM2596S-5 | Pi Zero 2 W (via J1 pin 2), U5 (LDO input) | C2 (220uF bulk), C11 (10uF LDO input) |
| +3V3 | 3.3V | U5 AMS1117-3.3 | U3 (RP2040 IOVDD/DVDD/USB_VDD/ADC_AVDD), U4 (flash VCC), U6 (codec AVDD/DRVDD/IOVDD) | C9 (10uF LDO output bulk), C12-C16 + C28 (RP2040 IOVDD per-pin), C31 (VREG_VIN 1uF), C32 (USB_VDD), C33 (ADC_AVDD), C34 (flash VCC), C35 (RUN POR), C17-C22 (codec) |
| DVDD_1V1 | 1.1V | U3 internal VREG | U3 (RP2040 digital core) | Internal to U3 |
| CODEC_DVDD | 1.8V | U6 internal LDO | U6 (codec digital core) | C25 (10uF) |
| GND | 0V | Common return | All components | Copper pour on F.Cu and B.Cu |

## Signal Summary

**NET_TOPOLOGY.md § "RP2040 (U3)" pin table is the authoritative mapping.** This summary restates the RP2040-to-everything-else connections for quick lookup; if it disagrees with `NET_TOPOLOGY.md`, `NET_TOPOLOGY.md` wins.

Naming convention: `UART_TX_PI` / `UART_RX_PI` are **Pi-centric** — the name describes which direction the Pi is driving. `UART_TX_PI` is the wire carrying the Pi's TX signal (Pi out → RP2040 RX in). `UART_RX_PI` is the wire carrying the Pi's RX signal (RP2040 TX out → Pi RX in).

| Signal | From | To | Purpose |
|--------|------|----|---------|
| UART_TX_PI | J1 pin 8 (Pi GPIO14 TXD0 out) | U3 pin 41 (GPIO29 RX in) | Pi-to-RP2040 serial. Alt UART0 pinout. |
| UART_RX_PI | U3 pin 40 (GPIO28 TX out) | J1 pin 10 (Pi GPIO15 RXD0 in) | RP2040-to-Pi serial. Alt UART0 pinout. |
| SWD_SWDIO | J1 pin 18 (Pi GPIO24) | U3 pin 25 | Bit-banged SWD data line (openocd raspberrypi-native). Pi flashes firmware onto the RP2040 over these two pins. |
| SWD_SWCLK | J1 pin 22 (Pi GPIO25) | U3 pin 24 | Bit-banged SWD clock line. Paired with SWDIO. |
| KP_COL0 | J4.1 | U3 pin 36 (GPIO24) | Keypad column 0 read input. Active row pulls this low when a button is pressed. |
| KP_COL1 | J4.2 | U3 pin 35 (GPIO23) | Keypad column 1. |
| KP_COL2 | J4.3 | U3 pin 34 (GPIO22) | Keypad column 2. |
| KP_ROW0 | U3 pin 39 (GPIO27/ADC1) | J4.7 | Keypad row 0 scan output. |
| KP_ROW1 | U3 pin 38 (GPIO26/ADC0) | J4.6 | Keypad row 1. |
| KP_ROW2 | U3 pin 32 (GPIO21) | J4.5 | Keypad row 2. |
| KP_ROW3 | U3 pin 37 (GPIO25) | J4.4 | Keypad row 3. |
| HOOK_SW | SW1.1 | U3 pin 31 (GPIO20) | Hook switch state. Low when the handset is on the cradle (switch closed to GND). High via RP2040 internal pull-up when the handset is lifted. |
| LED_OUT | U3 pin 27 (GPIO16) | R1 → J6.1 | Indicator LED drive. High = LED on. |
| RINGER_IN1 | U3 pin 30 (GPIO19) | U2 pin 3 (IN1) | Motor driver control line 1. |
| RINGER_IN2 | U3 pin 18 (GPIO15) | U2 pin 2 (IN2) | Motor driver control line 2. Square wave on IN1/IN2 alternates bell hammer. |
| BELL_A / BELL_B | U2 OUT1 / OUT2 (pins 6 / 8) | J7.1 / J7.2 | Ringer mechanism drive. Bidirectional current through the bell coil. |
| USB_DM | U3 pin 46 | R3.2 (far pad NC) | USB D- series termination (27 Ω). No connector populated. |
| USB_DP | U3 pin 47 | R4.2 (far pad NC) | USB D+ series termination (27 Ω). |
| QSPI_SS | U3 pin 56 | U4 pin 1 /CS | 4-bit QSPI flash chip select. |
| QSPI_SCLK | U3 pin 52 | U4 pin 6 CLK | QSPI clock. |
| QSPI_SD0 | U3 pin 53 | U4 pin 5 DI/IO0 | QSPI data bit 0. |
| QSPI_SD1 | U3 pin 55 | U4 pin 2 DO/IO1 | QSPI data bit 1. |
| QSPI_SD2 | U3 pin 54 | U4 pin 3 /WP/IO2 | QSPI data bit 2 (write-protect). |
| QSPI_SD3 | U3 pin 51 | U4 pin 7 /HOLD/IO3 | QSPI data bit 3 (hold). |
| XIN | Y1.1 (Xi signal) | U3 pin 20 | 12 MHz crystal input. Load cap C5 sits next to Y1.1. |
| XOUT_MCU | U3 pin 21 | R9.1 | RP2040 drives XOUT into the R9 damping resistor first. |
| XOUT | R9.2 | Y1.3 (Xo signal), C6.1 | Net between R9 and the crystal. Load cap C6 sits next to Y1.3. |
| CODEC_SDA | J1 pin 3 (Pi GPIO2) | U6 pin 1 | I2C data for codec register control. Pi's I2C1 bus. Internal 1.8k pullups on Pi. |
| CODEC_SCL | J1 pin 5 (Pi GPIO3) | U6 pin 32 | I2C clock for codec control. Paired with SDA. |
| CODEC_BCLK | J1 pin 12 (Pi GPIO18) | U6 pin 26 | I2S bit clock. Pi drives at sample_rate x bits_per_sample x 2 channels. |
| CODEC_WCLK | J1 pin 35 (Pi GPIO19) | U6 pin 27 | I2S word clock (LRCLK). Toggles per audio sample to select left/right channel. |
| CODEC_DIN | J1 pin 40 (Pi GPIO21) | U6 pin 28 | I2S data from Pi to codec (playback). Pi's PCM_DOUT drives codec's DIN. |
| CODEC_DOUT | U6 pin 29 | J1 pin 38 (Pi GPIO20) | I2S data from codec to Pi (capture). Codec's DOUT drives Pi's PCM_DIN. |
| CODEC_MCLK | J1 pin 7 (Pi GPIO4) | U6 pin 25 | Master clock (optional). The codec can derive clocks from BCLK via its internal PLL, so MCLK is a fallback if PLL-from-BCLK doesn't work. GPIO4 configured as GPCLK0. |
| MIC_HOT | J8 pin 1 | C3, J9 pin 1 | Microphone hot signal from handset. Passes through C3 (RF filter) and J9 (kill switch). |
| MIC_FROM_SW | J9 pin 2 | R8 (bias), C23 (AC coupling to U6) | Mic signal after kill switch. This node carries both the DC bias from MICBIAS (via R8) and the AC audio signal. C23 strips the DC and passes only the audio to the codec's MIC1LP input. |
| MICBIAS_OUT | U6 pin 7 | R8 | Mic bias voltage output from codec. ~2V through R8 to power the electret mic element. |
| EAR_P | U6 HPLOUT (pin 11) | J8 pin 2, J10 pin 1 | Earpiece positive. Direct capless BTL output from the codec's headphone amplifier. |
| EAR_N | J8 pin 3 | J10 pin 2 | Earpiece negative/return. Ground reference for the earpiece, routed through the handset cable. |
| CODEC_UNUSED_IN | U6 pins 4/5/6/8 | C26 to GND | Grounded unused analog inputs. Prevents noise pickup per TI guidance. |
| RUN | R5 pullup | U3 pin 26 | RP2040 reset (active low). Held high by R5 for normal operation. |
