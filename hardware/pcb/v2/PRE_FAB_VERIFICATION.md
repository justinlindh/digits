# Pre-Fab Verification Guide

Comprehensive audit procedure and knowledge base for the Digits PCB v2. Captures every verification step that was performed during bring-up, including the reasoning behind each one, tooling choices, known gotchas, and cluster/trace concerns. Read this before touching the board; update it whenever a new audit class is added.

---

## 1. The canonical "is it ready?" check

Run these in order; each must pass before moving on.

### 1a. DRC (electrical design rule check)

**Tool:** `kicad-cli pcb drc --refill-zones` — MUST include `--refill-zones`. Without it, DRC reads stale zone fills and misses zone-island violations, same-layer clearance errors that depend on pour boundaries, and starved-thermal warnings.

**The MCP `run_drc` tool is unreliable** (doesn't refill zones, misses dangling tracks). Use `kicad-cli` as the authoritative source.

**Must-pass categories:**
| Category | Must be |
|---|---|
| unconnected | 0 |
| dangling tracks/vias | 0 |
| clearance violations | 0 |
| shorting items | 0 |
| solder mask bridge | 0 |

**Cosmetic (OK to defer):**
- `silk_over_copper`, `silk_overlap` — reference designators over pads/vias. Won't affect fab but makes rework hard to read. End-of-project cleanup.
- `lib_footprint_mismatch` — footprint in PCB differs from library copy. Almost always cosmetic (version stamps, UUIDs, property order). Audit pad geometry via script before concluding; if pads match byte-for-byte, ignore.

**Iterative dangling cleanup:** deleting a dangling track can expose another. Re-run DRC in a loop until stable.

### 1b. ERC (electrical rule check for schematic)

**Tool:** `kicad-cli sch erc`. MCP's `run_erc` misses dangling labels — cross-check with kicad-cli.

**Real issues to flag:**
- Net with only one endpoint (floating input, dangling output)
- Power pin with no power source
- Analog input DC-shorted to GND (MIC1LM pattern we caught)
- Global/local label name collision that actually represents two different nets

**Cosmetic (OK):**
- `endpoint_off_grid` — wires not landing on the 50mil grid. Aesthetic, not electrical.
- `same_local_global_label` — same name on global and local. Usually intentional (hierarchical scope).
- `lib_symbol_mismatch` — symbol embedded copy differs from library. Same as footprint mismatch, usually cosmetic.

### 1c. Schematic-PCB parity

**Tool:** `kicad-cli pcb drc --schematic-parity` catches netlist mismatches between the two files.

Also: `kicad-cli sch export netlist` and compare against pad-net assignments in the PCB for any ref.

---

## 2. BOM verification

### Per-part checks

For each component with an LCSC part number, verify on `https://www.lcsc.com/product-detail/{C-number}.html` or `https://jlcpcb.com/partdetail/{C-number}`:

1. **Value match** — BOM says 10uF, LCSC says 10uF. Caught the C109455=10uF-0603-assigned-to-1uF-0402 error this way.
2. **Package match** — 0402 vs 0603 vs 0805. Caught the C49678=0805-assigned-to-0402 error.
3. **Voltage rating** — ≥2× rail voltage for bypass, ≥1× for signal coupling. Caught C72515=16V-on-12V-rail (1.33× derating) as insufficient.
4. **Dielectric** — C0G for timing/crystal, X7R/X5R for bypass/bulk, avoid Y5V. Caught C1555=22pF mislabeled as 15pF.
5. **Stock at JLCPCB assembly** — not just retail LCSC stock. The two inventories are separate. Use `https://jlcpcb.com/partdetail/{C-number}`.
6. **Part-number suffix variance** — `-TF` vs `-TFT` (JST) are the same physical part with different packaging. Either works; stock may differ.

### JLCPCB upload format gotchas

- **Range notation breaks CPL matching.** `"C12-C16"` in the BOM doesn't match `"C12","C13","C14","C15","C16"` in the CPL. Always expand ranges to comma-separated.
- **Ungrouped BOM triggers duplicate warnings AND zero-qty rows.** When 3 connectors share one LCSC, JLC's tool only places the first one unless they're on the same row.
- **Correct format:** group by LCSC, concatenate designators comma-separated, no ranges. The build script for `production/bom.csv` does this:

```python
kicad-cli sch export bom --group-by "LCSC Part #" ...
# then post-process: expand any "C12-C16" to "C12,C13,C14,C15,C16"
```

### Known OOS patterns
- `C25744` (Uni-Royal 10k 0402) has flickered in and out of LCSC stock repeatedly. Prefer **C60490** (Yageo RC0402FR-0710KL) as the stable alternate.
- `C265115` (JST B3B-ZR-SM4-TFT) has 0 JLC assembly stock. Use **C489717** (same part, different packaging) instead.

---

## 3. Per-IC datasheet compliance

For every active device (U1…U7), the authoritative reference is the device datasheet's "Typical Application" figure. Compare schematic vs figure topologically — every cap, resistor, inductor in the reference must have a match in the schematic.

### U1 LM2596S-5 (buck converter)
- Datasheet: TI SNVS124
- Reference circuit: §9.2, Fig 9-13
- Must have: C1 input bulk (470uF 25V), L1 (33uH 3A+), D1 (SS54 Schottky 5A/40V), C2 output bulk (220uF 25V), R and Cff optional for speed (not populated)
- Layout: keep the switching loop (U1 SW → L1 → D1 cathode → U1 GND) as tight as possible, single layer.

### U2 DRV8871DDAR (H-bridge motor driver)
- Datasheet: TI SLVSCY9B
- Reference circuit: §9.2, Fig 11
- Must have: C54 (100nF ≤3mm from U2.5), C55 (47uF ≥25V, ≤6mm from U2.5), R2 (ILIM at U2.4 to GND)
- **I_TRIP = V_ILIM / R_ILIM = 64 / R_kΩ.** For R2=33k → I_TRIP = 1.94A typ (tolerance range 1.77-2.11A).
- Layout: thermal pad to GND pour; short VM to C55 loop; IN1/IN2 can be longer (3.3V CMOS).

### U3 RP2040
- Datasheet: RPi RP2040 + "Hardware design with RP2040"
- Decoupling per §2.9:
  | Pin | Function | Local cap | Our ref |
  |---|---|---|---|
  | 1, 10, 22, 33, 42, 49 | IOVDD | 100nF each | C12, C13, C14, C15, C16, C28 |
  | 23, 50 | DVDD | 100nF each | C29, C30 |
  | 44 | VREG_VIN | 1uF | C31 |
  | 48 | USB_VDD | 100nF | C32 |
  | 43 | ADC_AVDD | 100nF | C33 |
  | 45 | VREG_VOUT | 1uF | C10 |
  | 26 | RUN | 100nF POR cap | C35 |
- Crystal §2.16: Y1 = Abracon ABM8-272-T3 (CL=10pF), load caps 15pF on XIN side + 15pF on XOUT side of **R9** (1k damping), NOT between RP2040 and R9.
- Per-pin cap placement: ≤3mm from the supply pin, tracked by `tools/check_decoupling.py`.
- TESTEN (pin 19) **must** tie to GND.

### U4 W25Q16JV flash
- Datasheet: Winbond W25Q16JV
- Must have: C34 (100nF) at U4.8 VCC, ≤1mm per datasheet.
- Pin 1 CS needs no pullup (RP2040 bootrom drives it).
- Layout: group all 6 QSPI data lines (SD0-3, SCLK, CS) close to both ICs; ideally < 20mm total length, matched ±1mm.

### U5 AMS1117-3.3 (LDO)
- Datasheet: AMS AMS1117
- Must have: C11 (10uF) input, C9 (10uF) output. Datasheet mandates ≥10uF ceramic output for stability.
- Input cap placement: ≤4mm from U5.3 Vin.
- Output cap placement: ≤12mm from U5.2 Vout (tracked by `tools/check_ldo_placement.py`).

### U6 TLV320AIC3104IRHBR (codec)
- Datasheet: TI SLAS510G
- Reference circuit: Fig 11-1
- Per-pin decoupling (close-in, ≤1mm):
  | Pin | Rail | Cap |
  |---|---|---|
  | 7 (IOVDD) | +3V3 | C40 100nF |
  | 18, 24 (DRVDD) | +3V3 | C41, C42 100nF |
  | 25 (AVDD) | +3V3 | C43 100nF |
  | 32 (DVDD) | +1V8 | C38 100nF + C39 1uF |
  | 15 (MICBIAS) | output | C48 100nF |
  | 31 (RESET) | logic | C53 1nF to GND (ESD) |
- Bulk +3V3 near EP: C44 1uF + C45 10uF.
- Mic coupling: R10 (2.2k from MICBIAS) + C46 (0.47uF to MIC1LP) + C47 (0.47uF on MIC1LM to GND as AC reference).
- Unused mic inputs MIC1RP/RM/MIC2L/R (pins 12-14, 16): each with its own 100nF to GND (C49-C52). NOT bussed onto a shared cap.
- Earpiece: capless BTL via HPLOUT (pin 19) + HPLCOM (pin 20), NO coupling caps.
- I2C address 0x18 fixed, no strap.
- PLL can source from BCLK (firmware Page 0 Reg 102 D5:D4 = BCLK; D7:D6 = BCLK for CLKDIV).
- **Critical gotcha caught this session:** mic cold wire must tie to GND. A separate `MIC_GND` net with no GND connection leaves the electret unbiased.

### U7 XC6206P182MR (codec DVDD LDO)
- Datasheet: Torex XC6206
- Must have: C36 (100nF) input bypass, C37 (10uF) output bulk.
- 1.8V fixed output, 250mA capacity.
- Internal codec DVDD LDO must remain OFF in firmware (see `SOFTWARE_CONFIG.md` §7).

### Y1 crystal
- **Load matching is critical.** `C_load = 2 × (CL - C_stray)`, where C_stray ≈ 2.5pF typical. For CL=10pF → 15pF caps. For CL=20pF → 35pF caps.
- Using the datasheet-exact part (Abracon ABM8-272-T3, C20625731) is preferred over YXC substitutes — RP2040 is specifically tuned for ABM8 parameters per §2.16.1.1.

---

## 4. Pi Zero 2 W interface

- **Power:** feed +5V at J1.2/J1.4. Pi Zero 2 W onboard regulator is OK receiving 5V from GPIO with USB disconnected.
- **Pin 1 and Pin 17 (3V3) are NC** — don't back-feed Pi's onboard 3V3 regulator.
- **I2C1** (GPIO2/3) has internal 1.8k pullups on the Pi; no external pullups needed on carrier. Codec at 0x18.
- **I2S** (GPIO18-21): PCM_CLK/FS/DIN/DOUT. PCM_DIN is Pi input (codec out), PCM_DOUT is Pi output (codec in). Easy to get direction wrong — verify.
- **UART0** (GPIO14/15) conflicts with Pi's default serial console — see `SOFTWARE_CONFIG.md` §1.
- **SWD** on GPIO24/25 uses OpenOCD `raspberrypi-native` config. No hardware pullups needed.
- **CODEC_RESET** from Pi GPIO22, held high by R11 (10k pullup to +3V3), C53 (1nF) for ESD.

### Face-down mounting
- Pi mounts face-down above the carrier. J1 body on F.Cu. Pi pin 1 must align with J1 pad 1 (southwest corner in our layout).
- **J1 must be a FEMALE socket** (C2977589). Two male headers can't mate.

---

## 5. Placement / clustering / trace length

### Hard placement constraints (tracked by `tools/check_*_placement.py`)

| Constraint | Target ≤ | Tool |
|---|---|---|
| RP2040 per-pin decouplers to their supply pin | 3mm | `check_decoupling.py` |
| Codec per-pin decouplers to their supply pin | 1mm (close-in) | `check_codec_placement.py` |
| U5 AMS1117 input cap (C11) to U5.3 Vin | 4mm | `check_ldo_placement.py` |
| U5 AMS1117 output cap (C9) to U5.2 Vout | 12mm | `check_ldo_placement.py` |
| U4 flash VCC cap (C34) to U4.8 | 1mm | `check_flash_placement.py` |
| Crystal load caps (C5/C6) to respective crystal pin | 5mm | `check_crystal_placement.py` |
| Ringer VM HF cap (C54) to U2.5 | 3mm | `check_ringer_placement.py` |
| Ringer VM bulk (C55) to U2.5 | 6mm | `check_ringer_placement.py` |
| Buck loop components | tight — see `check_buck_placement.py` | `check_buck_placement.py` |

### Pad-only checker limitation
`check_decoupling.py` and friends measure **pad-to-pad distance**, NOT including component body/courtyard. A PASS means pads are close; it does NOT prove physical components don't collide. Always cross-check with `check_real_drc.py` or a visual DRC pass.

### Cluster groups (see kicad_pcb group definitions)

| Cluster | Components | Placement discipline |
|---|---|---|
| Buck | U1, L1, D1, C1, C2 | Tight SW loop; D1 cathode ↔ L1 ↔ U1.SW; top-layer loop only |
| LDO | U5, C9, C11 | Input cap on U5.3 side, output cap on U5.2 side |
| RP2040 | U3, C12-C16, C28-C33, C35 | 6 IOVDD caps distributed around U3, 1 per pin |
| Crystal | Y1, C5, C6, R9 | Isolated island, no digital traces crossing, GND guard preferred |
| Flash | U4, C34 | U4.8 cap touching the pin |
| Codec | U6, C36-C52, R10, R11 | Tightest cluster; per-datasheet arrangement |
| Ringer | U2, C54, C55, R2 | VM bulk near VM pin; ILIM short to GND |

### Trace length concerns

| Net class | Concern | Guideline |
|---|---|---|
| QSPI (SD0-3, SCLK, CS) | Signal integrity at 133 MHz | Total ≤ 30mm; matched ±1mm between data lines |
| I2S (BCLK, WCLK, DIN, DOUT, MCLK) | Clock jitter, crosstalk | Keep short; route over continuous ground reference; avoid parallel with switching power |
| Crystal (XIN, XOUT) | EMI sensitivity, stray capacitance | Shortest possible; no vias; away from digital clocks; under F.Cu ground guard |
| Buck switching (SW node, D1) | EMI radiation | Minimize loop area; keep on one layer |
| Mic signal (MIC_HOT, MIC_FROM_SW) | Analog sensitivity | Short; AC-coupled; separate from digital switching; over GND pour |
| Earpiece BTL (EAR_P, EAR_N) | Differential matching | Route parallel, equal length, F.Cu only |
| I2C (CODEC_SDA, CODEC_SCL) | Not critical at 100 kHz | Standard routing fine |
| UART0 (Pi-RP2040 serial) | Not critical at 115200 | Standard routing fine |
| SWD (SWDIO, SWCLK) | Not critical at typical bit-bang speeds | Standard routing fine |
| +12V, +5V, +3V3 power rails | Current capacity | Width ≥0.4mm for ≥500mA; zones preferred |
| GND | Return path continuity | B.Cu full pour; minimize breaks |

### Clearance deviations from default (JLC economy)
- Default Power net class clearance: 0.13mm (5mil, JLC economy-safe)
- Minimum trace width: 0.2mm (Default class) and 0.4mm (Power class)
- Via size: 0.5 outer / 0.3 drill

---

## 6. Critical bugs this session caught

Future-agent: these are the patterns to suspect first when reviewing.

1. **Floating analog net labeled as distinct** — `MIC_GND` was a global label attached only to J8.4/C3.2/J9.3 with no GND connection anywhere. The prose spec said "ties to GND inside the codec sheet" but the schematic didn't. Check every analog return / reference / ground-like net for an explicit GND tie.

2. **Gender mismatch in THT connectors** — C50980 (male pin header) assigned to J1 where the Pi has a male header. Check every mating connector — the C-number's "receptacle" vs "pin header" vs "plug" matters.

3. **Crystal load cap sized for wrong CL** — BOM value text said "Abracon ABM8-272-T3" (CL=10pF) but the assigned LCSC was a 20pF CL substitute; load caps stayed sized for 10pF. Always cross-check the LCSC part's actual CL against the load cap sizing equation.

4. **PTC fuse voltage rating below rail voltage** — TECHFUSE 1210 at 6V Vmax on a 12V rail. Some 1210 PTCs are 6V-rated; always check Vmax ≥ rail voltage (ideally 1.5×).

5. **Same LCSC on different parts** — wrong C-number mechanical (C109455 is 10uF 0603, but was assigned to 1uF 0402 components). The BOM tool's auto-match works on value+footprint; if the user hand-assigned a number, verify by fetching the LCSC page.

6. **Stale LCSC from cached BOM** — the old `production/bom.csv` was stale by several LCSC updates. Always regenerate from the schematic before upload.

7. **KiCad `Flip` mirrors pads** — for a THT component where we wanted to change the body side, Flip mirrors X coordinates and breaks routing. Correct approach: direct file edit of `(layer "B.Cu")` → `(layer "F.Cu")` on the footprint plus sub-element layers, NO pad mirror.

8. **Hierarchical sheet refs** — in a sub-sheet, the local "C1" reference maps to final "C36" via the `(instances ...)` block. Writing LCSC properties on the sub-sheet symbol affects all its instances. Fine for our one-placement case, but be aware if sheets get instantiated multiply.

9. **DRC without `--refill-zones` hides zone-island issues** — the authoritative DRC needs refill enabled. kicad-cli's `run_drc` via MCP does not refill.

10. **JLC BOM range notation mismatch** — JLC expects every designator in the CPL to appear in the BOM. Range notation `C12-C16` in the BOM is not valid; expand to comma-separated.

---

## 7. Tooling quick reference

### kicad-cli commands
```
kicad-cli pcb drc --refill-zones --output drc.json --format json --severity-all board.kicad_pcb
kicad-cli sch erc --output erc.json --format json --severity-all schematic.kicad_sch
kicad-cli sch export bom --output bom.csv --group-by "LCSC Part #" --fields "Reference,Value,Footprint,LCSC Part #" schematic.kicad_sch
kicad-cli sch export netlist --output netlist.net schematic.kicad_sch
kicad-cli pcb export dsn board.kicad_pcb  # for Freerouter
kicad-cli pcb import ses --output board.kicad_pcb routed.ses  # import routed back
```

### Python helpers in `hardware/pcb/v2/tools/`
- `check_decoupling.py` — RP2040 per-pin decoupling proximity
- `check_codec_placement.py` — codec per-pin + bulk
- `check_ringer_placement.py` — ringer cluster spacing
- `check_buck_placement.py` — buck loop
- `check_ldo_placement.py` — LDO in/out caps
- `check_flash_placement.py` — flash VCC cap
- `check_crystal_placement.py` — crystal load caps
- `check_real_drc.py` — combined DRC wrapper with zone refill

### LCSC verification URLs
- `https://www.lcsc.com/product-detail/{C-number}.html`
- `https://jlcpcb.com/partdetail/{C-number}` (use this for JLC assembly stock)

### WebFetch / WebSearch patterns that work
- Direct product URLs work: `https://www.lcsc.com/product-detail/C12345.html`
- LCSC search URLs do NOT work (client-side rendered; returns empty)
- Use WebSearch for discovery, then WebFetch specific C-numbers to confirm

---

## 8. What NOT to trust

- **MCP `run_drc`** — skips zone refill, misses several classes of issue
- **MCP `get_schematic_pin_locations`** — Y coordinate is inverted in some versions
- **MCP `swig` backend** — stalls mid-session unpredictably; when it hangs, switch to direct file edit or restart the MCP server
- **KiCad's `Update Footprints from Library`** without a geometry audit first — may silently move pads and break routing (though in our case we audited and all 5 mismatched footprints had identical pad geometry)
- **LCSC retail stock** as a proxy for JLC assembly stock — they are separate inventories
- **BOM output with range notation** — doesn't match CPL individual designators, JLC rejects the upload
- **"Flip" in pcbnew for changing THT body side** — mirrors pads, breaks routing
- **`production/bom.csv` without regenerating** — caches stale LCSC assignments across schematic changes

---

## 9. Sign-off checklist before ordering

- [ ] `kicad-cli pcb drc --refill-zones` returns 0 unconnected, 0 dangling, 0 clearance, 0 shorting
- [ ] `kicad-cli sch erc` returns 0 net errors (off-grid and same-local-global warnings OK)
- [ ] Every LCSC number in `production/bom.csv` verified against the LCSC product page for value+package+voltage+stock
- [ ] Every IC has complete datasheet-compliant decoupling (run placement checkers)
- [ ] All mechanical constraints met (mounting holes, SW1, hook switch positions)
- [ ] Pi pinout matches Pi Zero 2 W standard pinout
- [ ] Connector genders correct (female socket to mate with Pi's male header, etc.)
- [ ] Crystal load caps sized for the actual part's CL
- [ ] PTC fuse voltage rating ≥ rail voltage
- [ ] All power caps have ≥2× voltage derating (noted exceptions: C9, C11 borderline at 1.9× and 1.26× respectively)
- [ ] `production/bom.csv` is freshly regenerated, grouped by LCSC, range-expanded
- [ ] Gerbers, CPL, BOM uploaded to JLC; JLC's matcher shows every row as "Select by System" with non-zero qty and non-zero stock
- [ ] THT parts (J1, SW1) marked "Do Not Place" in JLC UI
- [ ] SOFTWARE_CONFIG.md items tracked for bring-up
