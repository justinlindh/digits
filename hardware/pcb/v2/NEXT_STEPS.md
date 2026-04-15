# PCB v2 — Next Steps

Status checkpoint: 2026-04-14 after commit `52be12b`.

## Where we are

Schematic is clean and ERC-validated (0 errors, 82 warnings — all cosmetic/style).
Two compartmentalized hierarchical sheets are in place:

- **`codec.kicad_sch`** — TLV320AIC3104 + XC6206P182MR LDO + 20 passives, 13-port interface, 19/19 invariants.
- **`ringer.kicad_sch`** — DRV8871 + 33kΩ ILIM + 100nF HF + 47µF bulk, 6-port interface, 11/11 invariants. Drives an off-board 12V↔120V step-up transformer (used in reverse) whose secondary feeds the bell coils.

Seven cluster placement validators all PASS (52/52 constraints):

| Cluster | Targets | Constraints |
|---|---|---|
| Codec (U6/U7) | `codec_targets.json` | 20 ✅ |
| RP2040 (U3) | `decoupling_targets.json` | 16 ✅ |
| Buck (U1) | `buck_targets.json` | 6 ✅ |
| Flash (U4) | `flash_targets.json` | 2 ✅ |
| Crystal (Y1) | `crystal_targets.json` | 3 ✅ |
| Ringer (U2) | `ringer_targets.json` | 3 ✅ |
| LDO (U5) | `ldo_targets.json` | 2 ✅ |

Comprehensive IC supply pin audit (25 pins across U1–U7): every pin has a decoupling cap. RP2040, codec, and flash bypass caps use a single centroid-to-pad limit of **4.68 mm**, derived from the worst-case decap distance across 11 RP2040 decaps on the KiCAD_Board_3_Minimal_Full_RP2040 reference board. Pass/fail only, no tiers. Buck, crystal, ringer, and LDO clusters keep their own rules because they are governed by different physical constraints (switching-loop area, oscillator SI, DRV8871 §10.1, LDO stability).

32 critical traces are routed and `(locked yes)` on F.Cu — close-in decoupling for RP2040, codec, buck, ringer, LDO, crystal, and flash. Two GND copper pours on F.Cu and B.Cu cover the full board outline.

All connectors except `J1` (Pi 40-pin) are SMD JST ZH SM4 family — board is eligible for JLCPCB economy assembly.

LCSC Part #s assigned to 38 of 42 components. Missing: `J1` and `SW1` (TH, hand-assembled), `J9` and `R9` (need JLCPCB DB lookup, deferred).

---

## What's left

### 🔴 Phase F — DRC blockers (DO THIS BEFORE ROUTING)

`hardware/pcb/v2/tools/check_real_drc.py` (new this session) reports **79 real DRC violations** across 7 categories. These must be fixed before any meaningful routing — otherwise the routes will land on top of existing shorts. None of the placement validators caught these because the validators only check center-to-pad distance, not body collisions.

Categories and what to do:

#### 1. Crystal cluster shorts (8 shorting + many courtyard overlaps) — WORST OFFENDER

The Y1/C5/C6/C14/C29/C35/R5/R9 cluster around `(28-34, 46-50)` is jammed too tight. Components are physically overlapping and pads are touching across nets. The 1.5mm shift of Y1 toward U3 earlier in the session squeezed everything together.

**Fix**: spread out the crystal cluster. Y1 + 6 caps + 1 resistor need ~6×4 mm of clear space minimum. Move Y1 back 1-2mm south (further from U3.20) to give the surrounding caps room. Then re-run `check_real_drc.py` and `check_decoupling.py`/`check_crystal_placement.py` until both pass.

Specific shorts to clear (look in pcbnew at the listed coords):
- C14.GND ↔ Y1.XOUT at (30.75, 46.81)
- C35.GND ↔ C6.XOUT at (32.35, 46.81)
- R5.+3V3 ↔ C6.GND at (33.55, 46.84)
- R9.XOUT_MCU ↔ Y1.GND at (29.05, 47.35)
- Y1.XOUT ↔ C29.GND at (31.25, 47.35)

#### 2. Buck cluster shorts (3 shorting + multiple courtyard overlaps)

- **C2.GND ↔ L1.SW_NODE** at (52.92, 39.09): C2 is too close to L1 above. Move C2 north by ~1mm.
- **C4.+12V ↔ D1.SW_NODE** at (56.48, 54.33): C4 right next to D1.
- **C4.GND ↔ D1.SW_NODE** at (56.48, 52.43): same issue.

C4 was placed at (56.48, 53.38) for the U1 pin 1 fanout. D1 was at (59.23, 53.38). They're 2.75mm center-to-center but D1 is 4mm wide and C4 is 1mm wide, so they overlap. **Move C4 ~1mm south** (away from D1) or **rotate C4 90° vertically** to give D1 horizontal clearance.

#### 3. CODEC_RESET track touching U6.30 (1 shorting)

`Track [CODEC_RESET] on F.Cu, length 1.4770 mm | Pad 30 [unconnected-(U6-RIGHT_LOM-Pad30)] of U6` at (22.16, 22.97).

This is a routing bug from this session's locked starter traces. The CODEC_RESET trace from R11 to U6.31 (RESET) is brushing the adjacent U6.30 (RIGHT_LOM, unused). Route is too wide for the 0.5mm QFN pin pitch at the angle it takes.

**Fix**: delete the existing CODEC_RESET trace, move R11 slightly so it can approach U6.31 without crossing pin 30, or hand-route a 0.15mm trace at a less aggressive angle.

#### 4. C56 ↔ L1 courtyard overlap

C56 at (49.5, 51.0) is too close to L1 at (52.92, 44.03). The +5V output cap collides with the inductor. **Move C56 east** to ~(48, 53) where it's clear of L1's body but still within ~5mm of U1.4.

#### 5. U1 copper_edge_clearance (5 pads)

5 pads of U1 are flagged as too close to the board outline `Rectangle on Edge.Cuts`. Worth investigating in pcbnew — could be the LM2596's thermal tab crossing one of the board edges, or a stale Edge.Cuts segment from earlier work.

#### 6. tracks_crossing (1)

`Track [/codec/MICBIAS] on F.Cu, length 1.2394 mm` crosses `Track [/codec/MIC2L_INT] on F.Cu, length 2.1549 mm` at (24.66, 27.85). Two F.Cu tracks on different nets crossing — needs one to move to B.Cu via or detour around.

#### 7. track_dangling (1)

`Track [XIN] on F.Cu, length 1.9800 mm` at (29.05, 50.55). XIN trace stub with one unconnected end, near the crystal cluster. Probably a fragment from a deleted route. **Delete it.**

#### 8. solder_mask_bridge (13)

These mostly correlate with the shorting_items above (when pads are too close, the mask between them can't be drawn). Fixing shorts in #1-#3 will resolve most of these.

#### 9. clearance (26)

26 cases of copper too close to other copper. Most are in the codec / RP2040 cluster boundary area where the fanout traces are tight against U6/U3 pad edges. Some are in the crystal cluster from #1. Fix #1-#3 first; then re-run check_real_drc.py and triage what's left.

**Verification command after each fix**:
```
NO_COLOR=1 python3 hardware/pcb/v2/tools/check_real_drc.py
```

Goal: 0 real DRC issues before starting Phase G routing.

### Phase G — Inter-cluster manual routing (next)

Route the critical analog/clock/power nets that span clusters or are too signal-sensitive for the auto-router. Then hand the rest to freerouter.

**Manual-route punch list, ordered by criticality:**

#### 1. Codec internal (3 nets, all inside the codec sheet but cross U7 ↔ U6 spatially)

- [ ] `/codec/+1V8`: U7.2 → C37.1 → U6.32 (DVDD feed). Short, direct, F.Cu only. The DVDD bulk path; should be the smallest possible loop.
- [ ] `/codec/MIC_P_INT`: C46 → U6.10 (mic positive AC coupling, internal codec node).
- [ ] `/codec/MIC_N_INT`: C47 → U6.11 (mic negative AC coupling).
- [ ] `/codec/MICBIAS`: U6.15 → C48 → R10 → MIC_FROM_SW boundary (sheet port). C48 + R10 are inside the codec sheet, MIC_FROM_SW exits the sheet.

#### 2. Buck switching loop (must be tight!)

- [ ] `SW_NODE`: U1.2 → L1.1 (switch node to inductor). Short, fat, F.Cu, no via, in the smallest possible loop with D1.
- [ ] `SW_NODE`: U1.2 → D1.1 (switch node to catch diode anode). Same hot-loop concern.
- [ ] `/VIN_RAW`: J3.1 → F1.1 (barrel-jack input → fuse). Through the fuse to the +12V rail.

#### 3. Audio analog signals (sensitive to noise)

- [ ] `MIC_FROM_SW`: J9.2 (mic kill switch) → C46 (codec sheet port → MIC1LP coupling cap inside sheet). Keep short and away from clock nets.
- [ ] `MIC_HOT`: J8 → J9 (handset to mic kill switch). Short trace.
- [ ] `MIC_GND`: J8 → J9. Mic return reference; pair with MIC_HOT.
- [ ] `EAR_P`: U6.HPLOUT → J8 (earpiece BTL+). Short, F.Cu, away from mic path.
- [ ] `EAR_N`: U6.HPLCOM → J8 (earpiece BTL−). Short, F.Cu, parallel to EAR_P.

#### 4. RP2040 oscillator

- [ ] `XIN`: Y1.1 → U3.20. Short, F.Cu, no via, away from clocks.
- [ ] `XOUT`: Y1.3 → C6 (load cap is between Y1 and U3 buffer R9).
- [ ] `XOUT_MCU`: R9 → U3.21. Buffered crystal output to MCU.

#### 5. I²S clock and data (J1 ↔ U6, 7 nets — speed critical)

- [ ] `CODEC_BCLK`: J1.12 → U6.2.
- [ ] `CODEC_WCLK`: J1.35 → U6.3.
- [ ] `CODEC_DIN`: J1.38 → U6.4 (Pi → codec data).
- [ ] `CODEC_DOUT`: J1.40 → U6.5 (codec → Pi data).
- [ ] `CODEC_MCLK`: J1.7 (GPCLK0) → U6.1.
- [ ] `CODEC_SDA`: J1.3 → U6.9 (I²C data).
- [ ] `CODEC_SCL`: J1.5 → U6.8 (I²C clock).

Better to route by hand and lock than rely on freerouter for these.

#### 6. QSPI bus (U3 ↔ U4, 6 nets, 62 MHz default)

- [ ] `QSPI_SCLK`: U3.52 → U4.6.
- [ ] `QSPI_SS`: U3.56 → U4.1.
- [ ] `QSPI_SD0`: U3.53 → U4.5.
- [ ] `QSPI_SD1`: U3.55 → U4.2.
- [ ] `QSPI_SD2`: U3.54 → U4.3.
- [ ] `QSPI_SD3`: U3.51 → U4.7.

QSPI distance budget: U4 is currently 8.6 mm from U3 at the edge of acceptable. Route on F.Cu, no vias, length-matched within ±2 mm.

#### 7. Bell ringer

- [ ] `BELL_A`: U2.6 → J7.1 (off-board transformer primary side A).
- [ ] `BELL_B`: U2.8 → J7.2 (off-board transformer primary side B).

J7 is JST ZH SM4, 1 A per contact. Trace width ≥0.5 mm to handle the ~150-400 mA primary current. Short.

#### 8. RP2040 control / I/O

- [ ] `RINGER_IN1`: U3.30 (GPIO19) → U2.3.
- [ ] `RINGER_IN2`: U3.18 (GPIO15) → U2.2.
- [ ] `HOOK_SW`: U3.31 → SW1 hook contact.
- [ ] `RUN`: R5.2 → U3.26 (RP2040 reset pullup).
- [ ] `LED_OUT`: U3.27 → R1.1 (LED current limit input).
- [ ] `LED_A`: R1.2 → J6 (LED anode connector).
- [ ] `SWD_SWCLK`: J1.18 → U3.24.
- [ ] `SWD_SWDIO`: J1.22 → U3.25.
- [ ] `UART_TX_PI`: J1.8 → U3.41.
- [ ] `UART_RX_PI`: J1.10 → U3.40.
- [ ] `KP_ROW0..3`, `KP_COL0..2`: J4 keypad to U3 (7 nets).

#### 9. CODEC_RESET

- [ ] `CODEC_RESET`: R11 → U6.31 (10kΩ pullup to RESET via codec sheet port).

#### 10. Power rails — let freerouter or pour handle

`+12V`, `+5V`, `+3V3`, `DVDD_1V1`, `GND` have many segments. The GND pour already covers the GND ratsnest. The `+12V`/`+5V`/`+3V3` rails should be wide traces freerouter can place after the critical nets are locked, or small +3V3 sub-pours stitched with vias.

### Phase H — Freerouter pass

After the manual-route checklist above is complete and all those traces are locked:

1. Export DSN: `kicad-cli pcb export dsn hardware/pcb/v2/kicad/digits-pcb.kicad_pcb -o /tmp/digits.dsn`
2. Run freerouter against `/tmp/digits.dsn`.
3. Import SES: `kicad-cli pcb import-ses --input /tmp/digits.ses --board hardware/pcb/v2/kicad/digits-pcb.kicad_pcb`
4. Refill zones: `kicad-cli pcb refill-zones hardware/pcb/v2/kicad/digits-pcb.kicad_pcb`

### Phase I — Final verification

- [ ] `kicad-cli pcb drc` exits 0 with no unconnected items.
- [ ] All 7 cluster placement checkers still PASS.
- [ ] Both subsheet checkers (codec, ringer) still PASS.
- [ ] ERC still 0 errors.
- [ ] BOM review: every component has a verified LCSC Part # (fill in J9, R9).
- [ ] JLCPCB live stock check on every LCSC Part # in the BOM.
- [ ] Position file sanity check.
- [ ] Export gerbers + drill files.
- [ ] Visual review of gerbers in a viewer.

### Phase J — Software touch-ups

These don't block fab but should be fixed before flashing the real board:

- [ ] `pi/image/rootfs-overlay/boot/firmware/overlays/digits-codec.dts`: verify `compatible = "ti,tlv320aic3104"`, supply refs, GPCLK0 pinmux for GPIO4 at 12.288 MHz.
- [ ] `pi/digitsd/internal/audio/alsa.go`: verify mixer control names match the AIC3x driver.

---

## Open questions

- **R9 1 kΩ 0402 LCSC Part #** — need to look up.
- **J9 JST_ZH_B3B-ZR-SM4-TF (3-pin) LCSC Part #** — need to look up.
- **QSPI length matching** — currently at the edge of acceptable distance (8.6 mm). Test under stress before committing to fab.

---

## Reference document index

- `codec-module-spec.md` — codec sheet design contract.
- `ringer-module-spec.md` — ringer sheet design contract.
- `codec_targets.json`, `decoupling_targets.json`, `buck_targets.json`, `flash_targets.json`, `crystal_targets.json`, `ringer_targets.json`, `ldo_targets.json` — placement constraint files.
- `tools/check_*_placement.py` — wrapper scripts.
- `tools/check_decoupling.py` — shared validator (the engine).
- `tools/check_codec_subsheet.py`, `tools/check_codec_symbol.py`, `tools/check_ringer_subsheet.py` — module contract validators.
