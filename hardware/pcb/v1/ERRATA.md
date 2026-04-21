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

### 3. MIC_GND is a floating net -- no connection to board GND

The schematic uses a local label `MIC_GND` for the handset mic return instead of the global `GND` power symbol. The net connects only to C3 pin 2, J8 pin 4 (RJ9 mic return), and J9 pin 3 (kill-switch GND terminal). It does not reach the board's GND pour anywhere. This was verified in both the schematic netlist and the PCB trace list (10 segments on B.Cu, all within the J8/J9/C3 cluster; no via to GND).

**Symptom:** on a build where the handset mic return depends on MIC_GND reaching the codec's ground (i.e., any build where the HAT/codec ground only reaches the handset through a single external wire), capture alone works and playback alone works, but any full-duplex audio (real call, digitsd audio test with denoise off) produces pure noise on the captured stream. The mic capsule's return current shares its single ground wire with the codec's playback current, and ground bounce from the DAC modulates the mic reference. Discovered on Digits 3 on 2026-04-19 after several hours of software debugging.

**Workaround for fabricated boards:** solder a short bodge wire from any MIC_GND-net pad (J8 pin 4 solder side is easiest) to any real GND pad on V1 PCB (e.g., U1 LM2596 pin 3, or a decoupling cap's GND leg, or a grounded mounting hole). 20-22 AWG, short as possible. This makes MIC_GND a proper low-impedance return and the duplex-capture failure goes away.

**Fix for future revisions:** Use the global `GND` power symbol for the RJ9 mic-return pin, not a local label. (V2 already does this correctly -- verified: J8 pin 4 in V2 is on the global GND net, and the whole MIC_GND-style local-label pattern does not recur.)

### 4. J7 bell screw terminal is electrically disconnected

J7 (Phoenix 5mm screw terminal intended for the bell coil) has no trace to anything else on the board. Its two pins drop onto single-pin nets `BELL_1` and `BELL_2` that no other component touches. Verified against both the schematic netlist and the PCB trace list. Plugging a bell coil into J7 produces no signal at all.

**Root cause:** V1's ringer design uses an external L298N driver module that plugs into J5 (IN1 / IN2 logic from Pico GP11/GP15, plus +12V / GND). The L298N's OUT1 / OUT2 terminals drive a step-up transformer whose secondary drives the bell coil (`docs/build/wiring.md` has the canonical wire-color map: yellow pair = primary to L298N, red pair = secondary to bell). J7 was placed on the board as a convenience onboard terminal for the bell coil but was never wired to any driver output, and there is no onboard H-bridge to drive it. The footprint is vestigial.

**Workaround for fabricated boards:** Do not use J7. Wire the transformer primary (yellow) directly to the L298N OUT1 / OUT2, and the transformer secondary (red) directly to the bell coil, matching the off-PCB layout in `docs/build/wiring.md`.

**Fix for V1.1:** Remove J7 from the schematic and PCB, or relabel it as a passive wire-junction block if keeping a physical terminal for the bell wires is useful. V2 addresses the root cause properly by bringing the H-bridge onboard (DRV8871) and routing its outputs to J7.

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
