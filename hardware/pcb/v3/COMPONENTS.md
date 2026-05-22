# Digits Carrier Board V3 -- Component Reference

Every component on the board, what it does, what it connects to, and why it exists.

**Source of truth:** the KiCad schematic at `kicad/digits-pcb.kicad_sch` plus the two hierarchical sub-sheets `kicad/codec.kicad_sch` and `kicad/ringer.kicad_sch`. This document mirrors them; if it disagrees with the schematic, the schematic wins. The PCB at `kicad/digits-pcb.kicad_pcb` matches the schematic 1:1 plus three mounting holes (MH1/MH2/MH3) added directly to the board as mechanical-only items.

---

## Power Input and Protection

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| PWR | JST XH B2B-XH-A 2-pin (`VIN_BARREL_PIGTAIL`) | JST_XH_B2B-XH-A 2.5mm vertical | C158012 | /VIN_RAW, GND | +5V DC power input. JST XH (2.5mm pitch, ~3A per contact) mates with an external pigtail. Upsized from V2's JST ZH, which sat at its 1A contact limit during ringer peaks. The board runs on +5V; the bell voltage is generated on-board by the U10 boost converter. |
| F1 | 1.5A PTC fuse, 16V rated | 1210 | C207048 | /VIN_RAW -> +5V | Resettable overcurrent protection on the input. Trips at 1.5A to protect the board from shorts or excessive draw. Resets automatically when the fault clears. Placed between PWR and the +5V rail. Part is Littelfuse 1210L150/16WR. |
| C1 | 470uF 25V electrolytic | 10x10.5mm SMD | C248607 | +5V, GND | Input bulk capacitor on +5V (Panasonic EEEFK1E471P, FK series HIGH TEMP REFLOW, 260C rated, 80mOhm ESR). Absorbs input ripple and supplies bulk current to the boost converter and the Pi during transients. |
| C4 | 100nF 50V X7R | 0805 | C49678 | +5V, GND | High-frequency bypass on the +5V rail. Filters supply noise before it fans out to U10 (boost), U5 (LDO), and the Pi header. |

## On-board Boost Converter (ringer supply, `/ringer/` sheet)

Steps the +5V rail up to ~37V (`VBOOST`) to drive the DRV8871 motor supply. This replaces V2's external 120V:12V mains step-up transformer. Detailed in `ringer-module-spec.md` and `PLANNED.md`.

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| U10 | XL6019E1 | TO-263-5 | C73018 | +5V in (pins 2/4), SW_NODE (pin 3 + tab pad 6), FB_NODE (pin 5), GND (pin 1) | Wide-input boost converter set to ~37V out. The metal tab (pad 6) is on SW_NODE, NOT GND. Vout = 1.25 * (1 + R20/R21) ~= 37.25V. |
| L10 | 47uH inductor | 12.3x12.3mm SMD | C9906 | +5V, SW_NODE | Boost energy-storage inductor. Current ramps through L10 during U10's on-time; energy transfers to VBOOST through D10 during off-time. |
| D10 | SS56 Schottky diode | SMA (DO-214AC) | C65009 | SW_NODE (anode), VBOOST (cathode) | Boost rectifier. Low forward drop Schottky, 5A/60V rated for the ~37V output. |
| C100 | 100uF 63V electrolytic | 10mm dia SMD | C28241 | VBOOST, GND | VBOOST bulk reservoir. Holds the boosted rail up under the DRV8871's bell-coil current pulses. |
| C101 | 1uF 50V | 0805 | C105952 | VBOOST, GND | VBOOST high-frequency bypass. |
| R20 | 57.6k 1% | 0402 | C26983 | VBOOST -> FB_NODE | Feedback divider top leg. Sets VBOOST with R21. |
| R21 | 2k 1% | 0402 | C4109 | FB_NODE -> GND | Feedback divider bottom leg. |

## 5V to 3.3V LDO

Linear regulator converting the 5V input to 3.3V for the RP2040 I/O, flash, crystal circuit, and audio codec. Replaces the Pico H module's onboard RT6150 regulator now that the RP2040 is bare on this board.

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| U5 | AMS1117-3.3 | SOT-223-3 | C6186 | +5V in, +3V3 out | 3.3V 1A fixed-output low-dropout regulator. Dropout voltage is ~1.1V, so the 5V input provides comfortable headroom. Powers RP2040 IOVDD/DVDD/USB_VDD/ADC_AVDD, W25Q16 flash, and TLV320AIC3104 codec analog/IO supplies (plus the codec's own U7 LDO input). |
| C9 | 10uF 6.3V X5R | 0603 | C109455 | +3V3, GND (U5 output side) | Output bulk cap for U5 AMS1117 LDO. Required by the AMS1117 datasheet for stable operation; without it the LDO can oscillate. Also provides local charge reservoir for transient loads on the 3.3V rail. |
| C11 | 10uF 6.3V X5R | 0603 | C109455 | +5V, GND (U5 input side) | Input bulk cap for U5. Stabilizes the 5V supply at the LDO's input pin. |

## RP2040 Microcontroller

Bare RP2040 chip (replacing the Pico H module from V1) with its minimal support circuit: flash, crystal, bypass caps, and reset network.

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| U3 | RP2040 | QFN-56 (7x7mm) | C2040 | Everything | Dual-core ARM Cortex-M0+ microcontroller. Runs the phone firmware: scans the 4x3 keypad matrix, detects hook switch state, drives the ringer bell via DRV8871, controls the indicator LED, and communicates with the Pi over UART. Also exposes SWD pins for firmware flashing from the Pi. |
| C12-C16, C28 | 6x 100nF 50V X7R | 0402 | C307331 | +3V3 to GND, one per RP2040 IOVDD pin (1, 10, 22, 33, 42, 49) | Per-pin high-frequency decoupling for U3 IOVDD pins per RP2040 datasheet section 2.9.1. The RP2040 has 6 IOVDD pins and the datasheet mandates one 100nF cap near each. |
| C29, C30 | 2x 100nF 50V X7R | 0402 | C307331 | DVDD_1V1 to GND, at U3 pins 23 and 50 | Per-pin decoupling for U3 DVDD pins per RP2040 section 2.9.2. DVDD_1V1 is the RP2040's internal 1.1V rail sourced at VREG_VOUT (pin 45). |
| C10 | 1uF 10V X7R | 0402 | C52923 | DVDD_1V1, GND, at U3.45 VREG_VOUT | Bulk cap on the DVDD_1V1 rail at the internal regulator output. Matches the Pico reference design. |
| C31 | 1uF 10V X7R | 0402 | C52923 | +3V3, GND, at U3.44 VREG_VIN | Local cap at VREG_VIN per RP2040 section 2.9.3 ("a 1uF capacitor should be connected between VREG_VIN and ground close to the chip's VREG_VIN pin"). |
| C32 | 100nF 50V X7R | 0402 | C307331 | +3V3, GND, at U3.48 USB_VDD | USB_VDD decap per section 2.9.4. Even though no USB connector is populated, USB_VDD is still tied to +3V3 and needs decoupling because the internal USB PHY remains powered. |
| C33 | 100nF 50V X7R | 0402 | C307331 | +3V3, GND, at U3.43 ADC_AVDD | ADC_AVDD decap per section 2.9.5. Even though the ADC is unused on this board, the datasheet mandates the decap because ADC_AVDD powers internal bandgap and reset/brownout reference circuitry. |
| R5 | 10k 1% | 0402 | C60490 | RUN (U3.26), +3V3 | Pull-up resistor on the RUN (reset) pin. Keeps the RP2040 out of reset during normal operation. The RUN pin is active-low, pulling it to GND resets the chip. Without R5 the pin could float and cause intermittent resets. |
| C35 | 100nF 50V X7R | 0402 | C307331 | RUN (U3.26), GND | POR filter cap on RUN. Together with R5 (10k) forms an RC with ~1us time constant that smooths the power-up ramp so the RP2040 releases reset cleanly after +3V3 stabilizes. Matches the official Pico reference schematic. |

## RP2040 Crystal Oscillator

Provides the 12MHz reference clock that the RP2040's PLL multiplies up to the operating frequency (typically 125MHz). Also used as the USB SOF clock reference.

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| Y1 | Abracon ABM8-272-T3 (12MHz, CL=10pF, ESR<=50Ohm) | 3225 (3.2x2.5mm) | C20625731 | U3 XIN (pin 20), Y1.3 -> R9 -> U3 XOUT (pin 21) | 12MHz crystal mandated by RP2040 datasheet section 2.16.1.1 ("Raspberry Pi Pico has been specifically tuned for the specifications of the Abracon ABM8-272-T3"). Do not substitute: CL=10pF is load-matched to C5/C6 = 15pF. A CL=20pF alternate (YXC X322512MSB4SI, C9002) would need 33pF load caps and is not datasheet-tuned for RP2040. |
| C5 | 15pF 50V C0G | 0402 | C1548 | Y1.1 (Xi signal pad), GND | Crystal load cap (XIN side). C_load = 2*(CL - C_stray) = 2*(10 - 2.5) = 15pF for CL = 10pF and typical 2.5pF stray. C0G dielectric for temperature stability. Y1.1 is the Xi signal pad per Abracon ABM8 pinout (pads 1/3 signal, 2/4 case GND). |
| C6 | 15pF 50V C0G | 0402 | C1548 | Y1.3 (Xo signal pad), GND | Crystal load cap (XOUT side). Y1.3 is the Xo signal pad. The RP2040's XOUT drive reaches Y1.3 through the R9 damping resistor, NOT directly. |
| R9 | 1k 1% | 0402 | C11702 | U3.21 (XOUT) in series to Y1.3 | XOUT series damping resistor. RPi "Hardware design with RP2040" section 2 explicitly specifies this for IOVDD = 3.3V designs. Limits crystal drive level and prevents overdrive. Net topology: U3.21 -> XOUT_MCU -> R9.1, R9.2 -> XOUT -> Y1.3, C6.1. Y1.2 and Y1.4 are case GND, NOT signal. |

## QSPI Flash

External flash memory storing the RP2040's firmware. Connected via a 4-bit QSPI bus for fast boot.

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| U4 | W25Q16JVSSIQ | SOIC-8 | C131025 | U3 QSPI pins (SS, SCLK, SD0-3), +3V3, GND | 2MB SPI NOR flash. Stores the RP2040 firmware image (UF2 format). Connected via 4-bit QSPI for fast sequential reads during boot. The RP2040 boots by reading code from this chip into internal SRAM. |
| C34 | 100nF 50V X7R | 0402 | C307331 | +3V3, GND, at U4.8 VCC | Flash VCC decoupling cap. Winbond W25Q16JV datasheet requires a 100nF ceramic as close as possible to pin 8. QSPI read bursts draw ~20mA transients; without this cap they couple back into +3V3 and can corrupt XIP instruction fetches. |
| SW2 | 6mm tact switch | THT SW_PUSH_6mm | (assign before fab) | QSPI_SS, GND | BOOTSEL button. Held during power-on, it pulls QSPI_SS low so the RP2040 bootrom enters USB/SWD bootloader mode. Eliminates V2's paperclip-on-U4-pin-1 procedure. |

**QSPI_SS has no external pullup.** The RP2040 bootrom drives /CS actively within nanoseconds of reset, and neither the Pico nor any of the Raspberry Pi Press RP2040 references populate a pullup here. SW2 momentarily grounds QSPI_SS only during power-on for BOOTSEL entry.

**There is no USB on this board.** No USB connector, no D+/D- termination resistors, no ESD network. Firmware is flashed by the Pi over SWD (`SWD_SWDIO`/`SWD_SWCLK`). USB_VDD on U3 is still tied to +3V3 with C32 because the internal USB PHY remains powered, but USB_DM/USB_DP (U3 pins 46/47) are left unconnected by design.

## Audio Codec (`/codec/` hierarchical sheet)

Onboard audio codec replacing the external Codec Zero HAT (~$20/unit). Provides I2S ADC/DAC with built-in mic preamp and headphone amplifier. Controlled by the Pi over I2C, audio data streams over I2S. Detailed in `codec-module-spec.md`.

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| U6 | TLV320AIC3104IRHBR | VQFN-32 5x5mm (Texas_RHB0032E) | C181753 | I2S bus (CODEC_BCLK/WCLK/DIN/DOUT/MCLK), I2C bus (CODEC_SDA/SCL), mic frontend, earpiece BTL outputs, +3V3, +1V8 (DVDD), GND | Low-power stereo audio codec with 24-bit ADC/DAC, 8-96kHz sample rates. Mic preamp with 59.5dB gain and AGC (ideal for telephony). Headphone amp drives the earpiece directly in BTL mode. Clocks derived from BCLK via internal PLL (no MCLK strictly required, though GPCLK0 is wired as fallback). I2C address fixed at 0x18. Linux driver: `tlv320aic3x.c` (mainline). |
| U7 | XC6206P182MR | SOT-23-3 | C347373 | +3V3 in, +1V8 out (-> U6.32 DVDD), GND | 1.8V fixed-output LDO. Generates the codec's DVDD rail externally on the `/codec/+1V8` net. The TLV320AIC3104 has an internal DVDD LDO option, but we feed DVDD from this external part to keep the digital supply quiet and allow the internal LDO to be disabled in software. |
| C36 | 100nF 50V X7R | 0402 | C307331 | +3V3, GND, at U7.3 (VIN) | LDO input decoupling for U7. Per XC6206 datasheet typical application. |
| C37 | 10uF 6.3V X7R | 0402 | C15525 | +1V8, GND, at U7.2 (VOUT) | LDO output bulk for U7. Stabilizes the +1V8 rail and supplies bulk current for U6's digital core. |
| C38 | 100nF 50V X7R | 0402 | C307331 | +1V8, GND, at U6.32 (DVDD) | DVDD close-in decoupling per SLAS510G section 13.1. Tightly coupled to the DVDD pin to suppress digital switching noise. |
| C39 | 1uF 10V X7R | 0402 | C52923 | +1V8, GND, at U6.32 (DVDD) | DVDD close-in bulk per SLAS510G Fig 11-1. Second cap on the same pin; lower-frequency complement to C38. |
| C40 | 100nF 50V X7R | 0402 | C307331 | +3V3, GND, at U6.7 (IOVDD) | IOVDD close-in decoupling per SLAS510G section 13.1. |
| C41 | 100nF 50V X7R | 0402 | C307331 | +3V3, GND, at U6.18 (DRVDD) | First DRVDD pin close-in decoupling. Drives one half of the headphone amp output stage. |
| C42 | 100nF 50V X7R | 0402 | C307331 | +3V3, GND, at U6.24 (DRVDD) | Second DRVDD pin close-in decoupling. Drives the other half of the headphone amp output stage. |
| C43 | 100nF 50V X7R | 0402 | C307331 | +3V3, GND, at U6.25 (AVDD) | AVDD close-in decoupling. Filters analog supply noise for the ADC/DAC analog stages. |
| C44 | 1uF 10V X7R | 0402 | C52923 | +3V3, GND, near U6 EP | +3V3 rail bulk cap shared across U6 power pins. Placed near the EP / chip center where it can serve all power pins with short loops. |
| C45 | 10uF 6.3V X7R | 0402 | C15525 | +3V3, GND, near U6 EP | +3V3 rail main bulk. Larger sibling of C44 for low-frequency reservoir. |
| C46 | 0.47uF 10V X5R | 0402 | C47339 | U6.10 (MIC1LP) <- MIC_FROM_SW | Mic input AC coupling, hot side. Blocks DC bias from R10/MICBIAS while passing the audio signal from the electret mic through to the codec's mic preamp input. 0.47uF gives a ~3.4Hz HPF corner well below the telephony band. Codec-side net is `/codec/MIC_P_INT`. |
| C47 | 0.47uF 10V X5R | 0402 | C47339 | U6.11 (MIC1LM) <- GND | Mic input AC coupling, return side. Maintains differential symmetry with C46. Codec-side net is `/codec/MIC_N_INT`. |
| C48 | 100nF 50V X7R | 0402 | C307331 | U6.15 (MICBIAS), GND | MICBIAS bypass per SLAS510G section 10.3.9 / Fig 11-1. Cleans up the codec's bias output before it reaches the mic. |
| C49 | 100nF 50V X7R | 0402 | C307331 | U6.12 (MIC1RP), GND | Unused mic input termination. Per TI guidance, floating analog inputs can pick up noise that couples into the active signal path. |
| C50 | 100nF 50V X7R | 0402 | C307331 | U6.13 (MIC1RM), GND | Unused mic input termination (companion to C49). |
| C51 | 100nF 50V X7R | 0402 | C307331 | U6.14 (MIC2L), GND | Unused mic input termination. |
| C52 | 100nF 50V X7R | 0402 | C307331 | U6.16 (MIC2R), GND | Unused mic input termination. |
| C53 | 1nF 50V X7R | 0402 | C1523 | U6.31 (RESET), GND | RESET ESD protection cap. Filters transient voltage spikes on the active-low RESET pin. |
| R10 | 2.2k 1% | 0402 | C25879 | U6.15 (MICBIAS) -> MIC_FROM_SW | MICBIAS series resistor. The codec's MICBIAS output provides ~2V DC through R10 to bias the electret mic element. R10 limits current and sets the bias point. |
| R11 | 10k 1% | 0402 | C60490 | U6.31 (RESET), +3V3 | RESET pullup. Keeps the codec out of hardware reset during normal operation. The driver can soft-reset via I2C register writes if needed. |

## Motor Driver / Ringer (`/ringer/` hierarchical sheet)

H-bridge motor driver that drives the phone's mechanical bell. The RP2040 generates a square wave on IN1/IN2 to make the bell hammer oscillate. Its motor supply VM is the on-board ~37V VBOOST rail (see boost section above). Detailed in `ringer-module-spec.md`.

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| U2 | DRV8871DDAR | SOIC-8 with EP | C75864 | VBOOST (VM pin 5), RP2040 GPIO19/15 (IN1/IN2 via RINGER_IN1/RINGER_IN2), R2 (ILIM pin 4), BELL connector (OUT1/OUT2 pins 6/8), GND | H-bridge motor driver rated 3.6A, 6.5-45V. Drives the phone's bell mechanism bidirectionally. Receives PWM/square wave from the RP2040 on IN1 and IN2 to alternate the bell hammer direction. Built-in current limiting via the ILIM pin and R2. Sleep mode when both inputs are low (50us wake-up, imperceptible). |
| C54 | 100nF 50V X7R | 0402 | C307331 | VBOOST (U2.5 VM), GND | VM HF bypass for U2, placed within 3mm of U2.5 per `ringer-module-spec.md`. |
| C55 | 47uF 25V electrolytic (Panasonic EEEFT1E470AR, D5xH5.8mm) | 5x5.3mm SMD footprint | C336270 | VBOOST (U2.5 VM), GND | VM bulk reservoir for U2, placed within 6mm of U2.5. Absorbs the inductive kickback from the bell coil during direction reversals. |
| R2 | 33k 1% | 0402 | C25779 | U2.4 (ILIM), GND | Current regulation resistor. Sets the DRV8871's current chopping threshold per TI DRV8871 datasheet (SLVSCY9B) section 7.3.3 Equation 1: I_TRIP = V_ILIM / R_ILIM = 64 / 33 ~= 1.94A typical (range ~1.77-2.11A including V_ILIM part-to-part tolerance and 1% resistor tolerance). Design intent: set the trip well above the 150-400mA expected peak ringing current so it only fires on a fault condition (e.g. bell coil short). |

## Connectors

The off-board signal connectors are SMD JST ZH (1.5mm pitch). The Pi header is a 2x20 2.54mm THT female header. Connector references are semantic.

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| Pi Zero W 2 | 2x20 Female Header 2.54mm | THT, body on F.Cu side | C2977589 | Pi Zero 2 W GPIO | The main interconnect between the carrier board and the Raspberry Pi. Carries: UART (Pi GPIO14/15 serial to RP2040), SWD (Pi GPIO24/25 for firmware flashing), I2S (codec data), I2C (codec control), GPCLK0 (Pi GPIO4 optional MCLK), CODEC_RESET (Pi GPIO22), +5V power to the Pi (pins 2/4), and GND. Female socket to mate with the Pi's male 2x20 pin header. |
| KEYPAD | JST ZH 7-pin SMD | JST_ZH_B7B-ZR-SM4-TF | C265294 | KP_COL0-2, KP_ROW0-3 | Keypad connector. Connects to the phone's 4x3 button matrix. Four row lines (KP_ROW0-3) are scanned as outputs by the RP2040; three column lines (KP_COL0-2) are read as inputs to detect which button is pressed. |
| LED | JST ZH 2-pin SMD | JST_ZH_B2B-ZR-SM4-TF | C265284 | pin1 GND, pin2 LED_A | LED connector. Drives an indicator LED in the phone housing through R1 (220 ohm current limiter). The RP2040 controls the LED via GPIO16 (LED_OUT). Pin 1/2 polarity matches the stock phone LED cable directly. |
| BELL | JST ZH 2-pin SMD (carrying screw-terminal labels in schematic) | JST_ZH_B2B-ZR-SM4-TF | C265284 | BELL_A, BELL_B | Bell/ringer output. Connects to the phone's mechanical bell mechanism. The DRV8871 (U2) drives this bidirectionally from the VBOOST rail to make the bell hammer oscillate. |
| J8 | JST ZH 4-pin SMD | JST_ZH_B4B-ZR-SM4-TF | C265083 | pin1 MIC_HOT, pin2 GND, pin3 EAR_P, pin4 EAR_N | Handset connector. Four-wire cable to the phone handset carrying mic signal (MIC_HOT, return to GND) and earpiece audio (EAR_P/EAR_N). The mic hot signal passes through SW1 pole 2 (kill switch) before reaching the codec. Earpiece audio comes directly from the codec's headphone amp in capless BTL mode (no coupling cap needed). Pinout matches the stock Sangyn Retro 2500 RJ9 cable directly. |

## Switches and Indicators

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| SW1 | 6-pin DPDT telephone hook switch | SW_DPDT_Hook_24.2x17.1mm (custom, B.Cu side) | (assign before fab) | Pole 1: pin2 HOOK_SW, pin3 GND, pin1 unused. Pole 2: pin5 MIC_HOT, pin4 MIC_FROM_SW, pin6 unused. | Hook + mic-kill cradle switch. Mounted alone on the BACK copper side so it can press the cradle plunger. Pole 1 grounds/opens HOOK_SW for hook sense (off-hook reads high via RP2040 internal pull-up; on-hook grounds it). Pole 2 series-interrupts the mic path so the mic is dead on-hook (privacy: no GPIO can override it). Position is fixed by the phone enclosure. Retires V2's separate tactile hookswitch and the J9 mic-kill connector. |
| SW2 | 6mm tact switch | THT SW_PUSH_6mm | (assign before fab) | QSPI_SS, GND | See QSPI Flash section. BOOTSEL entry on power-on. |
| D2 | Red LED | 0603 | (assign before fab) | +5V via R12, /LED12V_K | Power indicator on the +5V rail. The net `/LED12V_K` is a legacy label; it sits on +5V on this revision. |
| D3 | Green LED | 0603 | (assign before fab) | +3V3 via R13, /LED3V3_K | Power indicator on the +3V3 rail. |
| R12 | 300 ohm | 0402 | (assign before fab) | D2 cathode (/LED12V_K) -> GND | Current limit for D2. |
| R13 | 330 ohm | 0402 | (assign before fab) | D3 cathode (/LED3V3_K) -> GND | Current limit for D3. |

D2/D3/R12/R13 and the SW1/SW2 switches have no LCSC part assigned in the schematic; assign before fab.

## Other

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| R1 | 220 ohm | 0805 | C17557 | RP2040 GPIO16 (LED_OUT = U3.27) -> LED_A | Current limiting resistor for the indicator LED. At 3.3V with a typical 2V LED forward voltage, R1 limits current to ~6mA: bright enough to see, safe for the GPIO pin (max 12mA per pin on RP2040). |
| C3 | 100nF 50V X7R | 0805 | C49678 | MIC_HOT, GND (at J8.1) | RF filter capacitor on the microphone hot signal. Provides a low-impedance path to ground for high-frequency interference picked up by the mic cable. Placed within 1mm of J8.1. |
| MH1, MH2, MH3 | M3 mounting holes | 3.2mm | -- | Mechanical only (PCB only, not in schematic) | Board mounting points. Three M3 holes whose positions are locked by the phone enclosure's screw posts. These cannot be moved without modifying the phone housing. |

---

## Power Rails

| Rail | Voltage | Source | Consumers | Decoupling |
|------|---------|--------|-----------|------------|
| +5V | 5V | PWR -> /VIN_RAW -> F1 | Pi Zero 2 W via header pins 2/4, U5 (LDO input), U10 (boost input), D2 indicator | C1 (470uF bulk), C4 (100nF HF), C11 (10uF LDO input) |
| VBOOST | ~37V | U10 XL6019 boost (+5V -> L10 -> SW_NODE -> D10 -> VBOOST) | U2 (DRV8871 VM) | C100 (100uF bulk), C101 (1uF HF), C54 (100nF VM HF), C55 (47uF VM bulk) |
| +3V3 | 3.3V | U5 AMS1117-3.3 | U3 (RP2040 IOVDD/USB_VDD/ADC_AVDD/VREG_VIN), U4 (flash VCC), U6 (codec AVDD/DRVDD/IOVDD), U7 (codec DVDD LDO input), R5/R11 (pullups), D3 indicator | C9 (10uF LDO output bulk), C12-C16 + C28 (RP2040 IOVDD per-pin), C31 (VREG_VIN 1uF), C32 (USB_VDD), C33 (ADC_AVDD), C34 (flash VCC), C35 (RUN POR), C36 (U7 VIN), C40-C43 (codec rails), C44/C45 (codec +3V3 bulk near EP) |
| +1V8 | 1.8V | U7 XC6206P182MR | U6.32 (codec DVDD only) | C37 (10uF U7 VOUT bulk), C38 (100nF DVDD close-in), C39 (1uF DVDD bulk close-in) |
| DVDD_1V1 | 1.1V | U3 internal VREG (VREG_VOUT pin 45) | U3 (RP2040 digital core only) | C10 (1uF), C29, C30 (100nF per-pin) |
| GND | 0V | Common return | All components | Copper pour on F.Cu and B.Cu |

The +1V8 net is named `/codec/+1V8` inside the codec sheet; the VBOOST/SW_NODE/FB_NODE nets are named under `/ringer/`.

## Signal Summary

**`NET_TOPOLOGY.md` is the authoritative net mapping.** This summary restates the RP2040-to-everything-else and Pi-to-codec connections for quick lookup; if it disagrees with `NET_TOPOLOGY.md`, `NET_TOPOLOGY.md` wins.

Naming convention: `UART_TX_PI` / `UART_RX_PI` are **Pi-centric**: the name describes which direction the Pi is driving. `UART_TX_PI` is the wire carrying the Pi's TX signal (Pi out -> RP2040 RX in). `UART_RX_PI` is the wire carrying the Pi's RX signal (RP2040 TX out -> Pi RX in).

| Signal | From | To | Purpose |
|--------|------|----|---------|
| UART_TX_PI | Pi header pin 8 (GPIO14 TXD0 out) | U3 pin 41 (GPIO29 RX in) | Pi-to-RP2040 serial. Alt UART0 pinout. |
| UART_RX_PI | U3 pin 40 (GPIO28 TX out) | Pi header pin 10 (GPIO15 RXD0 in) | RP2040-to-Pi serial. Alt UART0 pinout. |
| SWD_SWDIO | Pi header pin 18 (GPIO24) | U3 pin 25 | Bit-banged SWD data line (openocd raspberrypi-native). |
| SWD_SWCLK | Pi header pin 22 (GPIO25) | U3 pin 24 | Bit-banged SWD clock line. |
| KP_COL0..2 | KEYPAD.1..3 | U3 pins 36/35/34 (GPIO24/23/22) | Keypad column reads. Active row pulls these low when a button is pressed. |
| KP_ROW0..3 | U3 pins 39/38/32/37 (GPIO27/26/21/25) | KEYPAD.7/6/5/4 | Keypad row scan outputs. |
| HOOK_SW | SW1.2 | U3 pin 31 (GPIO20) | Hook switch state. Low when the handset is on the cradle. High via RP2040 internal pull-up when lifted. |
| LED_OUT | U3 pin 27 (GPIO16) | R1 -> LED_A (LED connector pin 2) | Indicator LED drive. High = LED on. |
| RINGER_IN1 | U3 pin 30 (GPIO19) | U2 IN1 | Motor driver control line 1. |
| RINGER_IN2 | U3 pin 18 (GPIO15) | U2 IN2 | Motor driver control line 2. Square wave on IN1/IN2 alternates bell hammer. |
| BELL_A / BELL_B | U2 OUT1 / OUT2 | BELL.1 / BELL.2 | Ringer mechanism drive. |
| QSPI_SS | U3 pin 56 | U4 pin 1 /CS, SW2 | QSPI flash chip select; also pulled low by SW2 for BOOTSEL. |
| QSPI_SCLK | U3 pin 52 | U4 pin 6 CLK | QSPI clock. |
| QSPI_SD0..3 | U3 pins 53/55/54/51 | U4 pins 5/2/3/7 | QSPI 4-bit data bus. |
| XIN | Y1.1 (Xi signal) | U3 pin 20 | 12MHz crystal input. Load cap C5 sits next to Y1.1. |
| XOUT_MCU | U3 pin 21 | R9.1 | RP2040 drives XOUT into the R9 damping resistor first. |
| XOUT | R9.2 | Y1.3 (Xo signal), C6.1 | Net between R9 and the crystal. Load cap C6 sits next to Y1.3. |
| CODEC_SDA | Pi header pin 3 (GPIO2) | U6 pin 9 | I2C data for codec register control. Pi's I2C1 bus. Internal 1.8k pullups on the Pi. |
| CODEC_SCL | Pi header pin 5 (GPIO3) | U6 pin 8 | I2C clock for codec control. |
| CODEC_BCLK | Pi header pin 12 (GPIO18) | U6 pin 2 | I2S bit clock. |
| CODEC_WCLK | Pi header pin 35 (GPIO19) | U6 pin 3 | I2S word clock (LRCLK). |
| CODEC_DIN | Pi header pin 40 (GPIO21) | U6 pin 4 | I2S data Pi -> codec (playback). |
| CODEC_DOUT | U6 pin 5 | Pi header pin 38 (GPIO20) | I2S data codec -> Pi (capture). |
| CODEC_MCLK | Pi header pin 7 (GPIO4 / GPCLK0) | U6 pin 1 | Master clock (optional fallback). |
| CODEC_RESET | Pi header pin 15 (GPIO22) | U6 pin 31 | Active-low codec reset. Driven by the Pi during boot, tristated normally; held high by R11. |
| MIC_HOT | J8.1 | C3, SW1.5 | Microphone hot signal from handset. Passes through C3 (RF filter) and SW1 pole 2 (kill switch). |
| MIC_FROM_SW | SW1.4 | R10 (bias), C46 (AC coupling) | Mic signal after the SW1 kill pole. Carries both the DC bias from MICBIAS via R10 and the AC audio signal. C46 strips the DC and passes only the audio to U6.10 (MIC1LP). |
| MICBIAS | U6 pin 15 | R10, C48 | Mic bias voltage output from codec. ~2V through R10 to power the electret mic element. |
| EAR_P | U6 HPLOUT (pin 19) | J8 pin 3 | Earpiece positive. Direct capless BTL output from the codec's headphone amplifier. |
| EAR_N | U6 HPLCOM (pin 20) | J8 pin 4 | Earpiece negative/return (BTL second leg). |
| RUN | R5 pullup, C35 POR cap | U3 pin 26 | RP2040 reset (active low). Held high by R5; C35 smooths power-up. |

**U6 power-pin assignments (U6.7 IOVDD, U6.18/24 DRVDD, U6.25 AVDD, U6.32 DVDD) and I2S/I2C pin numbers are cross-checked against the TLV320AIC3104 RHB0032E datasheet, the schematic symbol definition, and the PCB footprint pad-to-net assignments: all three sources agree.**
