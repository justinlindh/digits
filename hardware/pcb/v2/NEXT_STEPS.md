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

Comprehensive IC supply pin audit (25 pins across U1–U7): every pin has a decoupling cap within 6 mm.

32 critical traces are routed and `(locked yes)` on F.Cu — close-in decoupling for RP2040, codec, buck, ringer, LDO, crystal, and flash. Two GND copper pours on F.Cu and B.Cu cover the full board outline.

All connectors except `J1` (Pi 40-pin) are SMD JST ZH SM4 family — board is eligible for JLCPCB economy assembly.

LCSC Part #s assigned to 38 of 42 components. Missing: `J1` and `SW1` (TH, hand-assembled), `J9` and `R9` (need JLCPCB DB lookup, deferred).

---

## What's left

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
