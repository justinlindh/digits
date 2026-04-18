# PCB V2 Errata and Design Notes

Issues discovered during pre-fab review (2026-04-18). Record of design-time fixes and outstanding audits.

---

## Fixed Before Fab

### 1. SW_NODE undersized (Default class 0.2mm, should be 0.75mm)

Discovered during pre-fab audit on 2026-04-18. `SW_NODE` (LM2596 switching node: U1 pin 2 → D1 cathode → L1 pin 1) was labeled in the schematic but was not in the `Power` netclass patterns. It fell into the `Default` class (0.2mm) and was routed at that width throughout — 6 segments totaling ~25mm, all on F.Cu.

**Analysis:** At 2A output (user load ceiling), switching-node RMS current equals ~2A. IPC-2221 gives +94°C rise at 0.2mm/1oz Cu, well outside IPC's 10-20°C design target. LM2596 datasheet (SNVS124G) §9.4.1 also requires the SW trace to be "wide and short" — current route has an unnecessary 7.5mm dog-leg.

**Fix applied (2026-04-18):**
- Added new `Switching` netclass at 0.75mm / via 0.6mm in `digits-pcb.kicad_pro`.
- Added pattern `SW_NODE → Switching`.
- User to re-route `SW_NODE` trace in KiCad to use the new 0.75mm width and shorten the U1→L1 path.

### 2. /VIN_RAW pattern mismatch (Power class pattern didn't apply)

`VIN_RAW` net exists in a hierarchical sub-sheet path as `/VIN_RAW`. The `"VIN_RAW"` pattern in the project file didn't match the leading `/`, so the net fell into `Default` class (0.2mm). At 1.5A fuse-clamped current, 0.2mm gives ~+49°C rise.

**Fix applied (2026-04-18):**
- Changed pattern `"VIN_RAW"` → `"/VIN_RAW"` in `digits-pcb.kicad_pro`.
- User to re-route `/VIN_RAW` trace in KiCad to pick up the now-matching 0.4mm Power class.

### 3. BELL_A / BELL_B in Default class

Audit flagged these as potentially undersized. **Verified fine** per `ringer-module-spec.md` (150-400mA peak). At 0.35A on 0.2mm, IPC gives ~+1.7°C rise. No action needed.

---

## Outstanding Audits

### Power Integrity Check (TODO)

A formal power integrity review was not part of the design methodology for v2. The SW_NODE / VIN_RAW issues above were found by ad-hoc audit, not systematic checking. Before the next fab cycle, do a full power integrity pass:

- [ ] Build a per-net current budget table (nominal, peak, fault) cross-referenced against component datasheets and observed use cases.
- [ ] Verify every net carrying >0.5A is in an appropriate netclass with sufficient trace width per IPC-2221 at the target temperature rise (aim for 10-20°C max).
- [ ] Check netclass_patterns against actual net names in the PCB (watch for hierarchical path prefixes like `/VIN_RAW`).
- [ ] Verify decoupling network per component datasheet — capacitor placement, values, and return path.
- [ ] Examine GND return paths for high-current nets — especially the D1 anode → GND and U1 pin 3 → GND paths (same switching current as SW_NODE on the opposite half-cycle).
- [ ] Run DRC with appropriate min_track_width set to Default class width; confirm no stranded traces.
- [ ] Consider adding a KiCad custom DRC rule to flag any net where the actual trace width is less than the net's class track_width (catches netclass-assignment regressions).

### Trace re-route tasks (before fab resubmission)

- [ ] Re-route `SW_NODE` to use the new `Switching` netclass (0.75mm). Shortest direct path U1 pin 2 → D1 cathode → L1 pin 1, single-layer F.Cu, no vias on this net, no copper pour.
- [ ] Re-route `/VIN_RAW` to use the now-matching `Power` class (0.4mm).
- [ ] Re-run full DRC.
- [ ] Re-export Gerbers and re-submit to JLCPCB.

---

## Verified Correct

Power rails (+5V, +12V, +3V3) are in the Power netclass and routed at 0.4mm. IPC-2221 thermal rise calculations at expected peak currents:

- +5V at 1.5A peak (Pi + codec): +14°C rise — within spec.
- +12V at 1.5A typical: +14°C rise. At 2.5A fault: +43°C rise, fuse-clamped by F1 at 1.5A continuous.
- +3V3 at 0.5A peak: +2°C rise — comfortable margin.

No action required on these rails.
