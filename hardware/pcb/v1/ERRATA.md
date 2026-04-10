# PCB V1 Errata and Assembly Notes

Issues discovered during board review (2026-04-09). These apply to all 30 fabricated v1 boards.

---

## Must Fix Before Assembly

### 1. Switching node trace undersized (Net-(D1-K))

The trace connecting U1 OUT (pin 2), D1 cathode, and L1 is routed at 0.25mm (Default net class). This net carries the full LM2596 switching current (3A+ peak). IPC-2221 rates 0.25mm at ~0.3-0.5A for 1oz copper with 10C rise.

**Root cause:** The net is auto-named `Net-(D1-K)` by KiCad, so it didn't match the Power net class patterns (`+5V`, `+12V`, `GND`). The Power class is correctly set to 0.75mm.

**Workaround for fabricated boards:** Run a solder bead along the trace to thicken it, or tack a short wire across the trace to carry the current. Focus on the segment between U1 pin 2, D1 cathode, and L1 pin 1.

**Fix for future revisions:** Add `Net-(D1*` to the Power net class patterns in the project file, or give the switching node an explicit label (e.g., `SW_NODE`) and add it to the Power pattern list.

### 2. J1 (Pi header) pin 1 orientation -- column swap when mounting Pi underneath

The 2x20 header (J1) is placed on B.Cu with 180-degree rotation. The combination of KiCad's automatic B.Cu mirror and the 180-degree rotation causes the odd/even pin columns to swap compared to what's needed for correct Pi mating from below.

**Workaround:** Mount the entire PCB "upside down" from the original plan. The Pi + Codec Zero stack goes on top through a standard female stacking header. Move the hook switch (SW1) and any connectors that need physical access to the opposite side of the board.

**Fix for future revisions:** Remove the 180-degree rotation on J1. The B.Cu mirror alone produces correct mating orientation.

---

## Recommended Improvements

### 3. Add reverse polarity protection on 12V input

J3 (barrel jack) connects directly to the +12V rail with no protection. A misidentified adapter during a 30-unit build could damage the LM2596 and everything on the 12V rail.

**Workaround:** Solder a 1N5822 Schottky diode in series with the barrel jack positive lead before it reaches the board. Can be done inline on the wire.

### 4. Add decoupling cap near Pico VSYS

The Pico receives +5V on VSYS with no local decoupling cap on the carrier board. The LM2596 switching noise could affect the Pico's onboard regulator.

**Workaround:** Tack a 100nF ceramic cap between VSYS and GND on the back of the Pico header or on the carrier board near the Pico's power pins.

### 5. Connect J1 pin 4 (+5V) in addition to pin 2

Only pin 2 carries +5V to the Pi. Connecting pin 4 as well halves contact resistance and adds redundancy. Not critical at current draw levels (~500mA peak for Pi + Codec Zero).

---

## Verified Correct

The following were audited and confirmed correct:

- **Power rail separation:** +12V only reaches LM2596 VIN and J5 (L298N). +5V only reaches Pico VSYS and J1 pin 2. No overvoltage on any GPIO.
- **UART crossover:** Pico GP0 (TX) -> Pi GPIO15 (RX), Pico GP1 (RX) -> Pi GPIO14 (TX). Correct.
- **SWD debug wiring:** J2 pin 1 = SWDIO -> J1 pin 15 (GPIO22), J2 pin 3 = SWCLK -> J1 pin 22 (GPIO25). Correct.
- **Keypad ribbon mapping:** J4 pins 1-7 match Col0, Col1, Col2, Row3, Row2, Row1, Row0. Matches wiring.md.
- **All GPIO assignments:** Verified against firmware pin map in wiring.md. All correct.
- **Ground plane:** B.Cu fill zone covers entire board. All GND nets connected.
- **Schottky diode polarity:** D1 anode to GND, cathode to switching node. Correct.
- **LM2596 ON/OFF pin:** Grounded (always on). Correct.
- **Capacitor voltage ratings:** C1 (680uF/25V on 12V), C2 (220uF/25V on 5V). Adequate margin.

---

## Assembly Order (updated)

1. **Reinforce switching node trace** (solder bead or wire -- see errata #1)
2. Solder U1 (LM2596, SMD D2PAK) -- hardest part
3. Solder through-hole power components (C1, C2, D1, L1, J3)
4. **Power up with bench supply -- verify 5V output before proceeding**
5. Solder remaining through-hole components (R1, C3, SW1, all connectors)
6. Decide board orientation (see errata #2) and solder hook switch on appropriate side
7. Plug in Pico, verify UART (PING/PONG)
8. Plug in Pi + Codec Zero stack, verify boot and audio
9. Test each subsystem: keypad, hook switch, ringer, LED, handset audio
