# User TODO — PCB v2 path to first fab

Everything left for you (not an agent) to do before pressing "place order" at JLC, then after the boards arrive. Items marked **[BLOCKER]** must be done before ordering; others are cleanup or bring-up.

## Before ordering

### [BLOCKER] 1. Stitching via for the old MIC_GND copper
The MIC_GND → GND net rename left one residual DRC airline: two existing copper segments are now on net GND but don't physically touch the main GND pour.

- Open `hardware/pcb/v2/kicad/digits-pcb.kicad_pcb` in pcbnew
- Fill zones (`B` key or Edit → Fill All Zones)
- Run DRC (`Inspect → Design Rules Checker → Run DRC`, with "Refill all zones" ticked)
- One "Unconnected item" between two GND tracks near J8 (approximately x=89.5, y=47.5 on B.Cu and x=89.7, y=41.5 on F.Cu)
- Add a single stitching via on one of those tracks at a location inside the B.Cu GND pour. ~10 seconds of work.
- Re-run DRC; confirm 0 unconnected

### [BLOCKER] 2. Re-export Gerbers, CPL, and BOM
After the stitching via is in:
```
cd hardware/pcb/v2/kicad
kicad-cli pcb export gerbers --output production/gerbers/ digits-pcb.kicad_pcb
kicad-cli pcb export drill --output production/gerbers/ digits-pcb.kicad_pcb
kicad-cli pcb export pos --output production/positions.csv --units mm digits-pcb.kicad_pcb
# BOM is already regenerated; re-run if the schematic changed:
# (see PRE_FAB_VERIFICATION.md §7 for the BOM grouping command + range-expansion post-process)
```
Then zip `production/gerbers/`.

### [BLOCKER] 3. Upload to JLC and confirm matches
- Upload the new gerbers zip as the PCB file
- Upload `production/bom.csv`
- Upload `production/positions.csv` (CPL)
- In JLC's "Bill of Materials" page, confirm:
  - Every row says "Select by System"
  - Every row has non-zero qty and non-zero "JLCPCB" source
  - **J9** shows C489717, qty 5, source >0 (we verified stock 14,727 earlier)
  - **J1** shows C2977589 (female socket)
  - **Y1** shows C20625731 (Abracon ABM8-272-T3)
  - **F1** shows C207048 (Littelfuse 16V)
- Mark these as **Do Not Place** (they're THT / not assembled):
  - **J1** (if JLC tries to place it — some tiers do THT at extra cost; cheapest economy does not)
  - **SW1** (hook switch, no LCSC, you source separately)
- Confirm total price looks right (~$12-15/board parts, plus PCB + assembly labor)

### 4. Optional: physical clearance check for C2
The LCSC part (KNSCHA 220uF 25V electrolytic) is **D8 × L10.2mm** — 10.2mm tall. Our footprint is named `CP_Elec_8x6.5` suggesting 6.5mm tall. The pad pattern is identical between variants, but the component stands 3.7mm higher than the name implies.

- Confirm the phone enclosure has vertical clearance above C2's placed position (should be fine since the Pi sits above but offset)
- If tight, either swap to a shorter 220uF 25V D8 elec (likely D8×5-7mm) or rotate/move C2

## After boards arrive

### 5. Hand-solder through-hole parts
- **J1** — 2x20 female socket (C2977589). Solders from the top side; Pi plugs in above face-down.
- **SW1** — 6mm THT tactile switch. You source this from Amazon/Digikey/eBay; pad pattern is standard 6×6×7mm 4-leg.
- Optional: J9 if JLC somehow couldn't place it — hand-solder the JST B3B-ZR-SM4-TF (SMD, 3-pin, 1.5mm pitch).

### 6. Apply Pi-side software config
See `hardware/pcb/v2/SOFTWARE_CONFIG.md` for the full list. Quick version:
- Edit `/boot/firmware/config.txt` on the Pi's SD card:
  ```
  enable_uart=1
  dtoverlay=disable-bt
  ```
- Edit `/boot/firmware/cmdline.txt`: remove `console=serial0,115200`
- Install the codec device tree overlay (binds `tlv320aic3x` ASoC driver to our codec)

### 7. Bring-up verification
Run each in sequence; if any fails, stop and debug before moving on.
- [ ] Apply +12V, measure +5V (should be 5.0 ±0.1V at several test points)
- [ ] Measure +3V3 (at U5.2 or any IOVDD decoupling cap)
- [ ] Measure +1V8 (at U7.2 or U6.32 DVDD)
- [ ] Pi boots to login
- [ ] `i2cdetect -y 1` shows codec at 0x18
- [ ] `dmesg | grep tlv320` shows codec probe success
- [ ] OpenOCD connects to RP2040 over SWD: `openocd -f interface/raspberrypi-native.cfg -f target/rp2040.cfg -c "init; exit"`
- [ ] UART0 round-trip Pi↔RP2040 at 115200 baud
- [ ] Keypad: press each button, confirm RP2040 reads scan correctly
- [ ] Hook switch: lift and replace handset, confirm RP2040 sees transitions
- [ ] Indicator LED: RP2040 toggles GPIO16, LED responds
- [ ] Audio: `arecord` from codec → `aplay` back to codec (loopback test through handset mic and earpiece)
- [ ] Ringer: RP2040 drives IN1/IN2 square wave, bell hammer oscillates
- [ ] R2 ILIM validation: confirm I_TRIP ≈ 1.94A if you can measure fault current (not critical for bring-up)

### 8. Close out design-intent gaps
- [ ] If audio has ground-loop hum, consider adding `MIC_RETURN` as a separate net with Kelvin return (noted in `codec-module-spec.md` §3 as a future option)
- [ ] If ringer is marginal, verify the 33k ILIM value against measured bell coil characteristics and retune if needed
- [ ] If any THT part fit is tight, update mechanical notes

## Deferred (later rev)

Not blocking for first fab, but worth doing when you spin a rev-B:

- [ ] Silkscreen cleanup — 27 silk_over_copper + 15 silk_overlap violations. Move or shrink reference designator text so it doesn't land on pads/vias. Makes rework much easier.
- [ ] Upgrade C11 from 10uF 6.3V to 10uF 16V — current 1.26× derating on +5V is tight (MLCC loses ~40% capacitance at applied voltage).
- [ ] Run `Tools → Update Footprints from Library` on the 5 drifted footprints (C1, C2, C15, D1, SW1). Verified safe — pad geometry byte-identical to library copies.
- [ ] Consider adding 100kΩ pull-downs on RINGER_IN1/IN2 for defensive-design robustness during RP2040 reset.
- [ ] Fix `COMPONENTS.md` line 56 typo: says RC=1µs for RUN network, actual is 1ms.
- [ ] Fix `codec-module-spec.md` §5.1 incorrect PGA input impedance claim (100kΩ listed, datasheet says 20-80kΩ).
- [ ] Evaluate tying RP2040 USB_DM/USB_DP through 0Ω/ESD pads to GND, currently floating.

## When in doubt

- `hardware/pcb/v2/PRE_FAB_VERIFICATION.md` — full verification procedure and all the bug patterns we've caught
- `hardware/pcb/v2/COMPONENTS.md` — authoritative component reference (canonicalized during this session)
- `hardware/pcb/v2/SOFTWARE_CONFIG.md` — Pi-side config requirements
- `hardware/pcb/v2/NET_TOPOLOGY.md` — net routing intent and constraints
- `hardware/pcb/v2/codec-module-spec.md`, `ringer-module-spec.md` — module-level design specs
