# Digits Carrier Board V2 -- Component Reference

Every component on the board, what it does, what it connects to, and why it exists.

**Source of truth:** the KiCad schematic at `kicad/digits-pcb.kicad_sch` plus the two hierarchical sub-sheets `kicad/codec.kicad_sch` and `kicad/ringer.kicad_sch`. This document mirrors them; if it disagrees with the schematic, the schematic wins. The PCB at `kicad/digits-pcb.kicad_pcb` matches the schematic 1:1 plus three mounting holes (MH1/MH2/MH3) added directly to the board as mechanical-only items.

**Total electrical components:** 68 (45 in the root sheet, 22 in `/codec/`, 4 in `/ringer/`). Plus 3 mounting holes on the PCB only.

---

## Power Input and Protection

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| J3 | JST ZH 2-pin SMD (`VIN_BARREL_PIGTAIL`) | JST_ZH_B2B-ZR-SM4-TF | C265284 | +12V, GND | 12V DC power input. The board uses a 2-pin JST that mates with an external pigtail terminating in a barrel jack. The phone runs on a 12V supply because the ringer bell needs 12V, and the LM2596 buck converter steps it down for everything else. |
| F1 | 1.5A PTC fuse, 16V rated | 1210 | C207048 | J3 -> +12V rail | Resettable overcurrent protection on the +12V input. Trips at 1.5A to protect the board from shorts or excessive draw. Resets automatically when the fault clears. Placed between J3 and all downstream 12V consumers. Part is Littelfuse 1210L150/16WR — 16V max working voltage gives proper headroom above the 12V rail (earlier selection was a 6V-rated part which could arc under fault). |
| C4 | 100nF 50V X7R | 0805 | C49678 | +12V, GND | High-frequency bypass on the +12V rail. Filters supply noise visible to U2 (DRV8871) and other +12V consumers. Pairs with C1 (470uF +12V bulk) and the ringer-local C54/C55 pair on the ringer sheet. |

## 12V to 5V Buck Converter

Converts the 12V wall adapter input to 5V for the Pi Zero 2 W and the 3.3V LDO. Uses the Texas Instruments LM2596 in a standard application circuit per the datasheet.

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| U1 | LM2596S-5 | TO-263-5 | C347421 | +12V in, +5V out, L1, D1, C1, C2 | 5V 3A fixed-output switching step-down regulator. Converts 12V to 5V at up to 3A. The fixed-output version eliminates the feedback resistor divider. |
| C1 | 470uF 25V electrolytic | 10x10.5mm SMD | C248607 | +12V, GND | Input bulk capacitor for U1 (Panasonic EEEFK1E471P, FK series HIGH TEMP REFLOW, 260C rated, 80mOhm ESR). Absorbs input voltage ripple and provides instantaneous energy during switching transients. 470uF matches TI's LM2596 datasheet reference value (SNVS124 Fig 9-13). |
| C2 | 220uF 25V electrolytic | 8x6.5mm SMD | C2895286 | +5V, GND | Output bulk capacitor for U1. Smooths the 5V output ripple. 220uF is the LM2596 datasheet minimum for stable regulation. |
| L1 | 33uH shielded inductor | 12x12mm SMD | C9400 | U1 SW pin, D1 cathode / C2 junction | Energy storage inductor for the buck topology. During U1's on-time, current ramps up through L1 storing energy in the magnetic field. During off-time, L1 releases energy through D1 to maintain current flow. 33uH is sized per the LM2596 datasheet for 5V/3A output from 12V input. Shielded to reduce EMI. |
| D1 | SS54 Schottky diode | SMA (DO-214AC) | C22452 | L1 / U1 SW junction, GND | Freewheeling (catch) diode for the buck converter. When U1's internal switch turns off, L1's magnetic field collapses and current must continue flowing -- D1 provides that path. Schottky type for low forward voltage drop (0.5V vs 0.7V for standard diodes), improving efficiency and reducing heat. 5A/40V rated, matching the LM2596's current capability. |
| C56 | 100nF 50V X7R | 0402 | C307331 | +5V, GND | High-frequency bypass at the buck output, complementing C2's bulk role. Added in Phase G ringer work to give the +5V rail a short-loop HF return path before it fans out to the Pi header and U5. |

## 5V to 3.3V LDO

Linear regulator converting the 5V buck output to 3.3V for the RP2040 I/O, flash, crystal circuit, and audio codec. Replaces the Pico H module's onboard RT6150 regulator now that the RP2040 is bare on this board.

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| U5 | AMS1117-3.3 | SOT-223-3 | C6186 | +5V in, +3V3 out | 3.3V 1A fixed-output low-dropout regulator. Dropout voltage is ~1.1V, so the 5V input provides comfortable headroom. Powers RP2040 IOVDD/DVDD/USB_VDD/ADC_AVDD, W25Q16 flash, and TLV320AIC3104 codec analog/IO supplies (plus the codec's own U7 LDO input). |
| C9 | 10uF 6.3V X5R | 0603 | C109455 | +3V3, GND (U5 output side) | Output bulk cap for U5 AMS1117 LDO. Required by the AMS1117 datasheet for stable operation -- without it the LDO can oscillate. Also provides local charge reservoir for transient loads on the 3.3V rail. |
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
| Y1 | Abracon ABM8-272-T3 (12MHz, CL=10pF, ESR<=50Ohm) | 3225 (3.2x2.5mm) | C20625731 | U3 XIN (pin 20), Y1.3 -> R9 -> U3 XOUT (pin 21) | 12MHz crystal mandated by RP2040 datasheet section 2.16.1.1 ("Raspberry Pi Pico has been specifically tuned for the specifications of the Abracon ABM8-272-T3"). Do not substitute — CL=10pF is load-matched to C5/C6 = 15pF. A CL=20pF alternate (YXC X322512MSB4SI, C9002) would need 33pF load caps and is not datasheet-tuned for RP2040. |
| C5 | 15pF 50V C0G | 0402 | C1548 | Y1.1 (Xi signal pad), GND | Crystal load cap (XIN side). C_load = 2*(CL - C_stray) = 2*(10 - 2.5) = 15pF for CL = 10pF and typical 2.5pF stray. C0G dielectric for temperature stability. Y1.1 is the Xi signal pad per Abracon ABM8 pinout (pads 1/3 signal, 2/4 case GND). |
| C6 | 15pF 50V C0G | 0402 | C1548 | Y1.3 (Xo signal pad), GND | Crystal load cap (XOUT side). Y1.3 is the Xo signal pad. The RP2040's XOUT drive reaches Y1.3 through the R9 damping resistor, NOT directly. |
| R9 | 1k 1% | 0402 | C11702 | U3.21 (XOUT) in series to Y1.3 | XOUT series damping resistor. RPi "Hardware design with RP2040" section 2 explicitly specifies this for IOVDD = 3.3V designs. Limits crystal drive level and prevents overdrive. Net topology: U3.21 -> XOUT_MCU -> R9.1, R9.2 -> XOUT -> Y1.3, C6.1. Y1.2 and Y1.4 are case GND, NOT signal. |

## QSPI Flash

External flash memory storing the RP2040's firmware. Connected via a 4-bit QSPI bus for fast boot.

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| U4 | W25Q16JVSSIQ | SOIC-8 | C131025 | U3 QSPI pins (SS, SCLK, SD0-3), +3V3, GND | 2MB SPI NOR flash. Stores the RP2040 firmware image (UF2 format). Connected via 4-bit QSPI for fast sequential reads during boot. The RP2040 boots by reading code from this chip into internal SRAM. |
| C34 | 100nF 50V X7R | 0402 | C307331 | +3V3, GND, at U4.8 VCC | Flash VCC decoupling cap. Winbond W25Q16JV datasheet requires a 100nF ceramic as close as possible to pin 8. QSPI read bursts draw ~20mA transients; without this cap they couple back into +3V3 and can corrupt XIP instruction fetches. |

**QSPI_SS has no external pullup.** The RP2040 bootrom drives /CS actively within nanoseconds of reset, and neither the Pico nor any of the Raspberry Pi Press RP2040 references populate a pullup here.

**There is no USB on this board.** No USB connector, no D+/D- termination resistors, no ESD network. Firmware is flashed by the Pi over SWD (`SWD_SWDIO`/`SWD_SWCLK`). USB_VDD on U3 is still tied to +3V3 with C32 because the internal USB PHY remains powered, but the differential pair lines are unconnected.

## Audio Codec (`/codec/` hierarchical sheet)

Onboard audio codec replacing the external Codec Zero HAT (~$20/unit). Provides I2S ADC/DAC with built-in mic preamp and headphone amplifier. Controlled by the Pi over I2C, audio data streams over I2S. Detailed in `codec-module-spec.md`.

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| U6 | TLV320AIC3104IRHBR | VQFN-32 5x5mm (Texas_RHB0032E) | C181753 | I2S bus (CODEC_BCLK/WCLK/DIN/DOUT/MCLK), I2C bus (CODEC_SDA/SCL), mic frontend, earpiece BTL outputs, +3V3, +1V8 (DVDD), GND | Low-power stereo audio codec with 24-bit ADC/DAC, 8-96kHz sample rates. Mic preamp with 59.5dB gain and AGC (ideal for telephony). Headphone amp drives the earpiece directly in BTL mode. Clocks derived from BCLK via internal PLL (no MCLK strictly required, though GPCLK0 is wired as fallback). I2C address fixed at 0x18. Linux driver: `tlv320aic3x.c` (mainline). |
| U7 | XC6206P182MR | SOT-23-3 | C347373 | +3V3 in, +1V8 out (-> U6.32 DVDD), GND | 1.8V fixed-output LDO. Generates the codec's DVDD rail externally. The TLV320AIC3104 has an internal DVDD LDO option, but we feed DVDD from this external part to keep the digital supply quiet and allow the internal LDO to be disabled in software. |
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
| C46 | 0.47uF 10V X5R | 0402 | C47339 | U6.10 (MIC1LP) <- MIC_FROM_SW | Mic input AC coupling, hot side. Blocks DC bias from R10/MICBIAS while passing the audio signal from the electret mic through to the codec's mic preamp input. 0.47uF gives a ~3.4Hz HPF corner well below the telephony band. |
| C47 | 0.47uF 10V X5R | 0402 | C47339 | U6.11 (MIC1LM) <- AGND | Mic input AC coupling, return side. Maintains differential symmetry with C46. |
| C48 | 100nF 50V X7R | 0402 | C307331 | U6.15 (MICBIAS), GND | MICBIAS bypass per SLAS510G section 10.3.9 / Fig 11-1. Cleans up the codec's bias output before it reaches the mic. |
| C49 | 100nF 50V X7R | 0402 | C307331 | U6.12 (MIC1RP), GND | Unused mic input termination. Per TI guidance, floating analog inputs can pick up noise that couples into the active signal path. |
| C50 | 100nF 50V X7R | 0402 | C307331 | U6.13 (MIC1RM), GND | Unused mic input termination (companion to C49). |
| C51 | 100nF 50V X7R | 0402 | C307331 | U6.14 (MIC2L), GND | Unused mic input termination. |
| C52 | 100nF 50V X7R | 0402 | C307331 | U6.16 (MIC2R), GND | Unused mic input termination. |
| C53 | 1nF 50V X7R | 0402 | C1523 | U6.31 (RESET), GND | RESET ESD protection cap. Filters transient voltage spikes on the active-low RESET pin. |
| R10 | 2.2k 1% | 0402 | C25879 | U6.15 (MICBIAS) -> MIC_FROM_SW | MICBIAS series resistor. The codec's MICBIAS output provides ~2V DC through R10 to bias the electret mic element. R10 limits current and sets the bias point. |
| R11 | 10k 1% | 0402 | C60490 | U6.31 (RESET), +3V3 | RESET pullup. Keeps the codec out of hardware reset during normal operation. The driver can soft-reset via I2C register writes if needed. |

## Motor Driver / Ringer (`/ringer/` hierarchical sheet)

H-bridge motor driver that drives the phone's mechanical bell. The RP2040 generates a square wave on IN1/IN2 to make the bell hammer oscillate. Detailed in `ringer-module-spec.md`.

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| U2 | DRV8871DDAR | HTSOP-8 with EP (SOIC-8 footprint) | C75864 | +12V (VM pin 5), RP2040 GPIO19/15 (IN1/IN2 via RINGER_IN1/RINGER_IN2), R2 (ILIM pin 4), J7 (OUT1/OUT2 pins 6/8), GND | H-bridge motor driver rated 3.6A, 6.5-45V. Drives the phone's bell mechanism bidirectionally. Receives PWM/square wave from the RP2040 on IN1 and IN2 to alternate the bell hammer direction. Built-in current limiting via the ILIM pin and R2. Sleep mode when both inputs are low (50us wake-up, imperceptible). |
| C54 | 100nF 50V X7R | 0402 | C307331 | +12V (U2.5 VM), GND | VM HF bypass for U2, placed within 3mm of U2.5 per `ringer-module-spec.md`. |
| C55 | 47uF 25V electrolytic (Panasonic EEEFT1E470AR, D5×H5.8mm) | 5x5.3mm SMD footprint | C336270 | +12V (U2.5 VM), GND | VM bulk reservoir for U2, placed within 6mm of U2.5. Absorbs the inductive kickback from the bell coil during direction reversals. |
| R2 | 33k 1% | 0402 | C25779 | U2.4 (ILIM), GND | Current regulation resistor. Sets the DRV8871's current chopping threshold per TI DRV8871 datasheet (SLVSCY9B) §7.3.3 Equation 1: I_TRIP = V_ILIM / R_ILIM = 64 / 33 ≈ 1.94A typical (range ~1.77-2.11A including V_ILIM part-to-part tolerance and 1% resistor tolerance). Design intent: set the trip well above the 150-400mA expected peak ringing current so it only fires on a fault condition (e.g. bell coil short). The "~0.94A" figure recorded in an earlier revision was a stale note from a different R_ILIM candidate; it is not consistent with 33k under any tolerance stacking and has been superseded. |

## Connectors

All connectors except J1 are SMD JST ZH (1.5mm pitch), converted from earlier through-hole/Phoenix parts in Phase D Task #24. They sit on the top side and carry signals between the carrier board and the phone's physical components.

| Ref | Part | Package | Connects | Purpose |
|-----|------|---------|----------|---------|
| J1 | 2x20 Female Header 2.54mm (ZHOURI 2.54-2x20, LCSC C2977589) | THT, body on F.Cu side | Pi Zero 2 W GPIO | The main interconnect between the carrier board and the Raspberry Pi. Carries: UART (GPIO14/15 for serial communication with RP2040), SWD (GPIO24/25 for firmware flashing), I2S (GPIO18-21 for audio codec data), I2C (GPIO2/3 for codec control), GPCLK0 (GPIO4 for optional MCLK), CODEC_RESET (GPIO22), +5V power to the Pi (pin 2), and GND. J1 body mounts on the top (F.Cu) side; the Pi plugs in face-down above the carrier board. Female socket to mate with the Pi's male 2x20 pin header. |
| J4 | JST ZH 7-pin SMD | JST_ZH_B7B-ZR-SM4-TF | Keypad connector. Connects to the phone's 4x3 button matrix. Four row lines (KP_ROW0-3) are scanned as outputs by the RP2040; three column lines (KP_COL0-2) are read as inputs to detect which button is pressed. |
| J6 | JST ZH 2-pin SMD | JST_ZH_B2B-ZR-SM4-TF | LED connector. Drives an indicator LED in the phone housing through R1 (220 ohm current limiter). The RP2040 controls the LED via GPIO16 (LED_OUT). |
| J7 | JST ZH 2-pin SMD (carrying screw-terminal labels in schematic) | JST_ZH_B2B-ZR-SM4-TF | Bell/ringer output. Connects to the phone's mechanical bell mechanism. The DRV8871 (U2) drives this bidirectionally to make the bell hammer oscillate. |
| J8 | JST ZH 4-pin SMD | JST_ZH_B4B-ZR-SM4-TF | Handset connector. Four-wire cable to the phone handset carrying mic signal (MIC_HOT, return to GND) and earpiece audio (EAR_P/EAR_N). The mic signal routes through J9 (kill switch) before reaching the codec. Earpiece audio comes directly from the codec's headphone amp in capless BTL mode (no coupling cap needed). |
| J9 | JST ZH 3-pin SMD | JST_ZH_B3B-ZR-SM4-TF | Microphone kill switch connector. A physical switch interrupts the mic signal path. When the switch is open, the mic is muted at the hardware level -- no software can override it. MIC_FROM_SW is the signal after the switch, which feeds both R10 (DC bias from codec) and C46/C47 (AC-coupled to codec input). |

There is no J10. Earlier revisions referenced an earpiece-only header that was never added; EAR_P/EAR_N reach the handset only through J8.

## Other

| Ref | Part | Package | LCSC | Connects | Purpose |
|-----|------|---------|------|----------|---------|
| SW1 | 6mm tact switch | THT (mounted on B.Cu side) | RP2040 GPIO20 (HOOK_SW = U3.31), GND | Hook switch. Detects whether the handset is on or off the cradle. When the handset is lifted, the switch opens and GPIO20 reads high (via internal pull-up). When replaced, the switch closes and pulls GPIO20 to ground. The firmware uses this to answer/end calls. Position is fixed by the phone enclosure; mounted on the bottom copper layer. |
| R1 | 220 ohm | 0805 | C17557 | RP2040 GPIO16 (LED_OUT = U3.27) -> J6 | Current limiting resistor for the indicator LED. At 3.3V with a typical 2V LED forward voltage, R1 limits current to ~6mA -- bright enough to see, safe for the GPIO pin (max 12mA per pin on RP2040). |
| C3 | 100nF 50V X7R | 0805 | C49678 | MIC_HOT, GND (at J8.1) | RF filter capacitor on the microphone hot signal. Provides a low-impedance path to ground for high-frequency interference picked up by the mic cable. Placed within 1mm of J8.1. |
| MH1, MH2, MH3 | M3 mounting holes | 3.2mm | -- | Mechanical only (PCB only, not in schematic) | Board mounting points. Three M3 holes whose positions are locked by the phone enclosure's screw posts. These cannot be moved without modifying the phone housing. |

---

## Power Rails

| Rail | Voltage | Source | Consumers | Decoupling |
|------|---------|--------|-----------|------------|
| +12V | 12V | J3 -> F1 | U1 (buck input), U2 (motor driver VM) | C1 (470uF +12V bulk), C4 (100nF +12V HF), C54 (100nF VM HF, ringer-local), C55 (47uF VM bulk, ringer-local) |
| +5V | 5V | U1 LM2596S-5 | Pi Zero 2 W via J1 pin 2/4, U5 (LDO input) | C2 (220uF bulk), C56 (100nF HF), C11 (10uF LDO input) |
| +3V3 | 3.3V | U5 AMS1117-3.3 | U3 (RP2040 IOVDD/USB_VDD/ADC_AVDD/VREG_VIN), U4 (flash VCC), U6 (codec AVDD/DRVDD/IOVDD), U7 (codec DVDD LDO input), R5/R11 (pullups) | C9 (10uF LDO output bulk), C12-C16 + C28 (RP2040 IOVDD per-pin), C31 (VREG_VIN 1uF), C32 (USB_VDD), C33 (ADC_AVDD), C34 (flash VCC), C35 (RUN POR), C36 (U7 VIN), C40-C43 (codec rails), C44/C45 (codec +3V3 bulk near EP) |
| +1V8 | 1.8V | U7 XC6206P182MR | U6.32 (codec DVDD only) | C37 (10uF U7 VOUT bulk), C38 (100nF DVDD close-in), C39 (1uF DVDD bulk close-in) |
| DVDD_1V1 | 1.1V | U3 internal VREG (VREG_VOUT pin 45) | U3 (RP2040 digital core only) | C10 (1uF), C29, C30 (100nF per-pin) |
| GND | 0V | Common return | All components | Copper pour on F.Cu and B.Cu |

## Signal Summary

**`NET_TOPOLOGY.md` is the authoritative net mapping.** This summary restates the RP2040-to-everything-else and Pi-to-codec connections for quick lookup; if it disagrees with `NET_TOPOLOGY.md`, `NET_TOPOLOGY.md` wins.

Naming convention: `UART_TX_PI` / `UART_RX_PI` are **Pi-centric** -- the name describes which direction the Pi is driving. `UART_TX_PI` is the wire carrying the Pi's TX signal (Pi out -> RP2040 RX in). `UART_RX_PI` is the wire carrying the Pi's RX signal (RP2040 TX out -> Pi RX in).

| Signal | From | To | Purpose |
|--------|------|----|---------|
| UART_TX_PI | J1 pin 8 (Pi GPIO14 TXD0 out) | U3 pin 41 (GPIO29 RX in) | Pi-to-RP2040 serial. Alt UART0 pinout. |
| UART_RX_PI | U3 pin 40 (GPIO28 TX out) | J1 pin 10 (Pi GPIO15 RXD0 in) | RP2040-to-Pi serial. Alt UART0 pinout. |
| SWD_SWDIO | J1 pin 18 (Pi GPIO24) | U3 pin 25 | Bit-banged SWD data line (openocd raspberrypi-native). |
| SWD_SWCLK | J1 pin 22 (Pi GPIO25) | U3 pin 24 | Bit-banged SWD clock line. |
| KP_COL0..2 | J4.1..3 | U3 pins 36/35/34 (GPIO24/23/22) | Keypad column reads. Active row pulls these low when a button is pressed. |
| KP_ROW0..3 | U3 pins 39/38/32/37 (GPIO27/26/21/25) | J4.7/6/5/4 | Keypad row scan outputs. |
| HOOK_SW | SW1.1 | U3 pin 31 (GPIO20) | Hook switch state. Low when the handset is on the cradle. High via RP2040 internal pull-up when lifted. |
| LED_OUT | U3 pin 27 (GPIO16) | R1 -> J6.2 | Indicator LED drive. High = LED on. |
| RINGER_IN1 | U3 pin 30 (GPIO19) | U2 IN1 | Motor driver control line 1. |
| RINGER_IN2 | U3 pin 18 (GPIO15) | U2 IN2 | Motor driver control line 2. Square wave on IN1/IN2 alternates bell hammer. |
| BELL_A / BELL_B | U2 OUT1 / OUT2 | J7.1 / J7.2 | Ringer mechanism drive. |
| QSPI_SS | U3 pin 56 | U4 pin 1 /CS | QSPI flash chip select. |
| QSPI_SCLK | U3 pin 52 | U4 pin 6 CLK | QSPI clock. |
| QSPI_SD0..3 | U3 pins 53/55/54/51 | U4 pins 5/2/3/7 | QSPI 4-bit data bus. |
| XIN | Y1.1 (Xi signal) | U3 pin 20 | 12MHz crystal input. Load cap C5 sits next to Y1.1. |
| XOUT_MCU | U3 pin 21 | R9.1 | RP2040 drives XOUT into the R9 damping resistor first. |
| XOUT | R9.2 | Y1.3 (Xo signal), C6.1 | Net between R9 and the crystal. Load cap C6 sits next to Y1.3. |
| CODEC_SDA | J1 pin 3 (Pi GPIO2) | U6 pin 9 | I2C data for codec register control. Pi's I2C1 bus. Internal 1.8k pullups on the Pi. |
| CODEC_SCL | J1 pin 5 (Pi GPIO3) | U6 pin 8 | I2C clock for codec control. |
| CODEC_BCLK | J1 pin 12 (Pi GPIO18) | U6 pin 2 | I2S bit clock. |
| CODEC_WCLK | J1 pin 35 (Pi GPIO19) | U6 pin 3 | I2S word clock (LRCLK). |
| CODEC_DIN | J1 pin 40 (Pi GPIO21) | U6 pin 4 | I2S data Pi -> codec (playback). |
| CODEC_DOUT | U6 pin 5 | J1 pin 38 (Pi GPIO20) | I2S data codec -> Pi (capture). |
| CODEC_MCLK | J1 pin 7 (Pi GPIO4 / GPCLK0) | U6 pin 1 | Master clock (optional fallback). |
| CODEC_RESET | J1 pin 15 (Pi GPIO22) | U6 pin 31 | Active-low codec reset. Driven by the Pi during boot, tristated normally; held high by R11. |
| MIC_HOT | J8.1 | C3, J9.1 | Microphone hot signal from handset. Passes through C3 (RF filter) and J9 (kill switch). |
| MIC_FROM_SW | J9.2 | R10 (bias), C46 (AC coupling) | Mic signal after kill switch. Carries both the DC bias from MICBIAS via R10 and the AC audio signal. C46 strips the DC and passes only the audio to U6.10 (MIC1LP). |
| MICBIAS | U6 pin 15 | R10, C48 | Mic bias voltage output from codec. ~2V through R10 to power the electret mic element. |
| EAR_P | U6 HPLOUT (pin 19) | J8 pin 3 | Earpiece positive. Direct capless BTL output from the codec's headphone amplifier. |
| EAR_N | U6 HPLCOM (pin 20) | J8 pin 4 | Earpiece negative/return (BTL second leg). |
| RUN | R5 pullup, C35 POR cap | U3 pin 26 | RP2040 reset (active low). Held high by R5; C35 smooths power-up. |

**U6 power-pin assignments (U6.7 IOVDD, U6.18/24 DRVDD, U6.25 AVDD, U6.32 DVDD) and I²S/I²C pin numbers are cross-checked against the TLV320AIC3104 RHB0032E datasheet, the schematic symbol definition, and the PCB footprint pad-to-net assignments: all three sources agree.**
