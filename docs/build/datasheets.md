# Digits — Component Datasheet Reference

Pre-fetched specs for all key components. **Read this before giving wiring instructions.**

---

## NOYITO FR120N Isolated MOSFET Module (HW-532 type) — RETIRED

> **Status:** Evaluated in Phase 1 but **replaced by L298N H-bridge** for bell drive.
> The NOYITO is a DC switch — it can't produce the AC waveform needed for the bell coil.
> The L298N alternates polarity at 20Hz to generate AC through the step-up transformer.
> See `docs/wiring.md` for the current bell ringer circuit.

<details>
<summary>Original specs (kept for reference)</summary>

**What it is:** Optocoupler-isolated N-channel MOSFET switch module. Low-side switching.

**Signal side (2-pin: PWM, GND):**
- PWM: 3.3V or 5V logic input (drives optocoupler LED through ~1kΩ resistor)
- GND: Signal ground (isolated from load ground)
- Optocoupler: PC817 — max 50mA input, tested at 20mA

**Load side (3-pin: +, LOAD, -):**
- **+**: Load power supply positive (also powers MOSFET gate via optocoupler phototransistor)
- **LOAD**: Connect to one terminal of your load (drain side of MOSFET)
- **-**: Load power supply negative / ground (source side of MOSFET)
- Load wiring: Power supply → **+** → external load → **LOAD** → (internal MOSFET) → **-** → supply return

**FR120N MOSFET specs:**
- Vds: 100V max
- Id: 9.4A (rated), 15A max
- **Vgs threshold: 2–4V** (needs 10V Vgs for full Rds_on of 0.11Ω)
- Rds_on @ Vgs=10V: 0.11Ω
- Rds_on @ Vgs=4.5V: much higher (partial conduction)

**Critical limitation:** Module has 4.7kΩ pull-down on MOSFET gate. Combined with FR120N's threshold, **load supply must be >7V for reliable full switching.** At 5V, the MOSFET barely conducts. At 12V+, it works great.

</details>

---

## Raspberry Pi Codec Zero (DA7212 — Audio pHAT for Pi Zero 2 W)

**What it is:** Pi Zero-sized audio codec HAT with DA7212 chip. External electret mic input (3.5mm TRS jack), mono speaker amp (screw terminals), MEMS mic, AUX IN/OUT pads. First-party Raspberry Pi product.

**Replaces:** Waveshare WM8960 Audio HAT (retired — MEMS mics hardwired to analog inputs, no external mic path).

**Key specs:**
- Codec: Dialog Semiconductor DA7212
- Control interface: I2C (address **0x1A**)
- Audio interface: I2S
- Speaker output: 1.2W into 8Ω (mono, differential, screw terminal P3)
- External mic: 3.5mm TRS jack with MICBIAS (auto-disables onboard MEMS on insert)
- AUX IN (P1) / AUX OUT (P2): stereo solder pads for RCA connectors
- Bonus GPIOs: 23 (green LED), 24 (red LED), 27 (button)
- Form factor: 65×31mm (Pi Zero pHAT)
- Sample rates: 8–96 kHz

**Pi connection:** Stacks directly onto Pi Zero 2 W GPIO header (40-pin). Uses:
- I2C: GPIO2 (SDA), GPIO3 (SCL) — for codec control registers
- I2S: GPIO18 (BCLK), GPIO19 (LRCLK), GPIO20 (DIN/ADC), GPIO21 (DOUT/DAC)
- UART GPIO14/GPIO15 remain free for Pico communication

**Driver:** Mainline kernel driver `snd-soc-da7213`. Overlay: `dtoverlay=rpi-codeczero` in `/boot/firmware/config.txt`. ALSA profiles: https://github.com/raspberrypi/Pi-Codec

**Verification:** `i2cdetect -y 1` should show device at 0x1a. `arecord -l` / `aplay -l` should list card "Zero".

---

## V-153-1C25 Microswitch (Hook Switch Replacement)

**What it is:** SPDT snap-action microswitch with long straight hinge lever arm (51mm).

**Key specs:**
- Rating: 15A @ 125/250VAC; 0.6A @ 125VDC; 0.3A @ 250VDC
- Contact configuration: 1NO + 1NC (SPDT — 3 terminals)
- Actuator: Long straight hinge lever, momentary action
- Operating force: 0.69N
- Contact resistance: 15mΩ max (initial)
- Insulation resistance: 100MΩ+ @ 500VDC
- Mechanical life: rated for high cycle count

**Terminal pinout (looking at the switch body):**
- **C** (Common): Center terminal
- **NO** (Normally Open): Open when lever is not pressed, closed when pressed
- **NC** (Normally Closed): Closed when lever is not pressed, open when pressed

**For Digits project:** Used as phone hook switch. Wire **C** to GPIO (GP10) and **NC** to GND. When handset is on cradle, lever is pressed → NC opens → GP10 pulled high (on-hook). When handset lifted, lever released → NC closes → GP10 pulled to GND (off-hook).

*Note: Verify NO vs NC behavior with multimeter before wiring — the lever orientation in the phone chassis determines which contact to use.*

---

## Raspberry Pi Zero 2 W — GPIO / UART

**Key specs:**
- GPIO voltage: **3.3V** (max — never exceed this on any GPIO pin)
- GPIO max current: 16mA per pin
- UART: PL011 on GPIO14 (TXD0, pin 8) and GPIO15 (RXD0, pin 10)
- UART default baud: configurable, we use 115200
- I2C: GPIO2 (SDA, pin 3), GPIO3 (SCL, pin 5) — used by Codec Zero (DA7212)

**UART connection to Pico:**
- Pi GPIO14/TX (pin 8) → Pico GP1/RX (pin 2)
- Pi GPIO15/RX (pin 10) → Pico GP0/TX (pin 1)
- Pi GND (pin 6) → Pico GND (pin 3)
- **TX↔RX cross-connect is mandatory**

**Both Pico and Pi are 3.3V logic — no level shifter needed for UART.**

**UART setup requirement:** Must disable serial console via `raspi-config` (Interface Options → Serial Port → No login shell, Yes hardware enabled). Device appears at `/dev/serial0`.

---

## RP2040 Pico H

**Key specs:**
- GPIO voltage: 3.3V
- GPIO max current: ~12mA per pin (absolute max ~16mA)
- VBUS (pin 40): 5V from USB
- 3V3 (pin 36): 3.3V regulated output (max 300mA)
- UART0: GP0 (TX), GP1 (RX) — used for Pi communication
- PWM: Available on all GPIO pins
- ADC: GP26-28 (not used in this project)

**Pin reference:** See `docs/phases/phase-1-component-validation.md` for full pinout diagram and project pin assignments.
