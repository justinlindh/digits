# PCB v2 — Next Steps Plan

Status checkpoint: 2026-04-13 after commit `0910411`.

The RP2040 cluster (U3, U4, Y1, R3, R4, R5, R9, C5, C6, C10, C12–C16, C28–C35) is placed against the Raspberry Pi Minimal-KiCAD reference geometry (3.05 mm uniform radial decoupling ring). Schematic is clean. Nothing is routed. This document tracks the remaining validation and execution work needed to get the board to fab-ready.

Everything below is phased so future sessions can pick up at any phase without re-deriving context.

---

## Phase A — Canonical DRC gate

Goal: plug a real KiCad DRC check into the loop so we catch clearance / annular-ring / courtyard / track-width / zone violations the third-party `analyze_pcb.py` does not.

**A1. Wire up `kicad-cli pcb drc`.**
- Command form: `kicad-cli pcb drc hardware/pcb/v2/kicad/digits-pcb.kicad_pcb --exit-code-violations -o /tmp/drc.rpt`
- Exits non-zero on any violation. Report format is readable text.
- Add as a mandatory gate before any commit that touches `digits-pcb.kicad_pcb`.
- Expected baseline today: several unrouted-net violations (everything in the cluster is still an airwire). We will tighten this after Phase D.

**A2. Tune design rules for the RP2040 cluster.**
- Current `.kicad_pro` design rules unknown — confirm they match JLCPCB 1 oz / 2-layer minimums (0.127 mm track / 0.127 mm clearance / 0.2 mm via drill / 0.4 mm via diameter). If not, update to JLC capabilities.
- Add a custom rule constraining **high-speed nets** (QSPI_SD0-3, QSPI_SCLK, QSPI_SS, USB_DM, USB_DP, XIN, XOUT, XOUT_MCU) to keep lengths reasonable. Max length constraint first, matched-length constraint later if we need it.
- Add a rule pinning QSPI bus track width to at least 0.15 mm for impedance + crosstalk.

**A3. Add a `Makefile` or shell helper** at `hardware/pcb/v2/tools/check_all.sh` that runs the full validation gauntlet in order: canonical ERC, `check_decoupling.py`, `kicad-cli pcb drc`, and `analyze_pcb.py`. Exit non-zero on any failure. Future sessions run one command before committing.

---

## Phase B — Non-cluster placement audit

Goal: every non-RP2040 component is currently placed by hand from the pre-rewrite era. Audit each cluster against datasheet / reference practice the same way we did for U3.

**Process: follow `CLUSTER_AUDIT_RUNBOOK.md` for each sub-phase below.** The runbook was written after the RP2040 cluster work and captures the nine-step ritual (gather primary sources → schematic audit → symbol/footprint pad audit → placement contract → reference geometry → target compute + preview → batched moves → overlap triage → commit + docs). Each cluster gets its own targets JSON and checker script; they are additive, not replacements.

**B1. Power cluster (U1 LM2596S, D1, L1, C1, C2, C4, C11, F1, J3).**
- Reference: TI LM2596 datasheet §8.2 "Typical Application" and §10 "Layout Guidelines"; Digi-Key and TI reference designs.
- Check: input cap C1 close to U1.1 VIN; catch diode D1 reverse path loop area; L1 switching node kept short; output cap C2 close to L1.2 and in the same GND-return loop as D1 and U1.3 GND; GND return stitching.
- Current U1 at (151.13, 142.24) — needs measurement vs. input/output cap placement.
- Deliverable: `hardware/pcb/v2/tools/check_buck_layout.py` that measures distances between U1 and each of C1/C2/D1/L1 and asserts max-loop-area is under ~8 mm².

**B2. LDO cluster (U5 AMS1117-3.3, C9, C11 input, C9 output).**
- Reference: AMS1117 datasheet §typical application.
- Check: input cap (shared with buck output) within 2 mm of U5.3 VI; output bulk within 2 mm of U5.2 VO; GND return kept tight.
- Current U5 at (55, 20.46). Likely fine but unverified.

**B3. Codec cluster (U6 TLV320AIC3104, C17–C27, R7, R8).**
- Reference: TI TLV320AIC3104 datasheet §layout + TI EVM (SLAU218A) layout notes.
- Check: one 100 nF + one 10 µF within 2 mm of each of AVDD, DRVDD (×2), IOVDD, DVDD pins. MICBIAS decap C26 close to pin 7. HPLOUT / HPLCOM output caps (we are capless BTL so this is different from a typical Codec Zero — verify the BTL topology once more). I²S clock traces from J1 to U6 should be short and matched.
- This was the subject of a past "TLV320 pin mapping crisis" memory note; a fresh audit is overdue.

**B4. Motor driver cluster (U2 DRV8871, R2 ILIM, J7, C4 VM decap).**
- Reference: TI DRV8871 datasheet §8.2 "Typical Application" + §10 "Layout Guidelines".
- Check: VM supply loop area (C4 → U2.5 VM → U2.1 GND → C4); ILIM resistor R2 close to U2.4; OUT1/OUT2 traces to J7 wide enough for 1 A peak; thermal pad via stitching under U2.
- Bell ringer is inductive load — kickback path needs attention.

**B5. Pi header + SWD + UART (J1, R1 LED, R5 RUN pullup).**
- Check: Pi header at correct pin numbering, SWD pair short, UART cross-wired correctly (done per NET_TOPOLOGY but unverified on PCB).
- R5 RUN pullup placement already good from the RP2040 cluster work.

**B6. Connector placement (J4, J6, J8, J9, J10, J7, J3, SW1).**
- Enclosure-driven: mounting hole positions and SW1 are locked. Connectors should be accessible from enclosure cutouts. This is a mechanical question — defer to enclosure CAD or a visual check against the phone shell.
- Not likely to move, but worth a sanity check.

**Deliverable for Phase B:** a second placement-constraints JSON (`hardware/pcb/v2/placement_constraints.json`) plus one Python checker (`tools/check_all_placement.py`) that covers all clusters, analogous to `check_decoupling.py` for the RP2040 cluster. Same pass/fail contract, same enforcement gate.

---

## Phase C — Thermal pad via stitching

Goal: every thermally-enhanced IC needs vias under its EP to carry heat to the ground plane. Fab will accept the board without this but the ICs will overheat in production.

**C1. U3 RP2040 QFN-56-1EP.** EP at ~3.2 × 3.2 mm. Needs a 3×3 or 4×4 grid of 0.3 mm vias stitched to the bottom GND plane.

**C2. U2 DRV8871 HTSOP-8-1EP.** EP at ~2.29 × 3 mm. Needs a 2×3 grid. Critical — DRV8871 dissipates up to 1 W during bell ring.

**C3. U6 TLV320AIC3104 VQFN-32-1EP.** EP at ~3.45 × 3.45 mm. Needs a 3×3 grid.

**C4. U1 LM2596S-5 TO-263-5.** Thermal tab pad (pin 3) is the heatsink. Needs 2 rows of vias under the tab.

**Deliverable:** one commit adding via arrays under each of these pads. Verify via `analyze_pcb.py thermal_pad_vias` report. No schematic change.

---

## Phase D — Routing

Goal: land actual copper. Freerouting auto-routes via DSN export/import cycle; we need guardrails so it does not make obviously wrong choices.

**D1. Pre-route preparation.**
- Assign track width classes in `.kicad_pro`:
  - `Power`: 0.4 mm (for +3V3, +5V, +12V, GND; trunk widths)
  - `Default`: 0.2 mm (for most signals)
  - `HighSpeed_QSPI`: 0.15 mm, length-matched within 0.5 mm (QSPI_SD0-3, SCLK)
  - `USB`: 0.15 mm differential pair (USB_DM, USB_DP) — even though no USB connector, we want clean traces
  - `Crystal`: 0.2 mm, length-matched, on inner layer if possible (XIN, XOUT, XOUT_MCU)
- Fill copper pours: F.Cu and B.Cu both tied to GND, cleared by 0.25 mm around signal traces and 0.5 mm around power rails.

**D2. Critical-first manual routing.** Lock in the hard-to-autoroute nets by hand before handing off to freerouting:
- **XIN, XOUT_MCU, XOUT** — crystal loop. Traces must be short, symmetric, over an uninterrupted GND plane on the opposite layer. Route these manually, then lock the tracks.
- **QSPI_SD0-3, QSPI_SCLK, QSPI_SS** — 133 MHz boot bus. Route manually over a continuous GND plane; keep traces between U3 and U4 within 5 mm total length variance. Lock the tracks.
- **DVDD_1V1** — low-voltage internal rail from U3.45 VREG_VOUT to C10, C29, C30, U3.23, U3.50. Short, wide, star topology with C10 at the center.
- **+3V3 trunk** — from U5.2 to the decoupling ring. Route as a polygon pour on F.Cu with at least 0.4 mm trunks to each cap.

**D3. Export to freerouting.**
- `kicad-cli pcb export dsn -o /tmp/digits.dsn hardware/pcb/v2/kicad/digits-pcb.kicad_pcb`
- Run freerouting: `java -jar freerouting.jar -mp /tmp/digits.dsn -de 30 -mt 4` (mp = process input; de = number of passes; mt = threads).
- Import result: `kicad-cli pcb import ses /tmp/digits.ses hardware/pcb/v2/kicad/digits-pcb.kicad_pcb` (or equivalent — check kicad-cli syntax).
- Expect: decoupling caps, SWD, UART, keypad matrix, codec control / I²S, ringer H-bridge all auto-routed.

**D4. Post-route validation gate.** Mandatory checks after any routing pass:
- `kicad-cli pcb drc --exit-code-violations` — must be zero.
- `check_decoupling.py` — still 16/16 (freerouting must not move components; verify it did not).
- **NEW: `tools/check_routing.py`** — see Phase E below. This is the freerouting-sanity check.

---

## Phase E — Routing sanity check (NEW, unique to this project)

Goal: freerouting does not understand design intent. A cap labelled `+3V3` near U3 pin 49 must be electrically tied to U3 pin 49 by copper; freerouting might route it via a 20 mm trace that goes around the chip. Catch that.

**E1. Net-by-net topology assertions.**
- For every entry in `decoupling_targets.json`, compute the actual routed trace length from the cap's near pad to the target pad. Fail if the trace exceeds a threshold (nominal ~5 mm, per-cap override allowed).
- Parse the `.kicad_pcb` track records. Build a net → list of (layer, start, end) segments. For a given cap+target, search for the shortest connected path through the track graph. Length of that path is the metric.
- Allow vias in the path — a small via count penalty is fine (each via adds ~0.3 mm to the effective length), but caps for a power pin should ideally have zero vias between the cap and the pad.

**E2. Critical-net length audit.**
- QSPI bus (6 nets): total length per net + pairwise difference. Assert max difference < 0.5 mm (matched).
- USB pair (2 nets): length difference < 0.2 mm.
- Crystal loop (XIN, XOUT_MCU, XOUT): each net < 10 mm total; XIN and XOUT_MCU should be roughly equal.
- DVDD_1V1 from U3.45 to C10 bulk: < 2 mm.

**E3. "Correct cap on correct pin" assertion.**
- For each decoupling cap, check that the actual route tree from the cap's pad-to-GND pin ends at (a) a GND pour or via, AND that the cap's pad-to-power pin ends at the correct target pad on the correct IC, directly, without passing through any other chip. Freerouting sometimes stitches through other footprint pads as convenient, creating subtle wrong-cap-feeding-wrong-pin bugs.

**E4. Crosstalk proximity heuristic.**
- Run `analyze_pcb.py --proximity` to get the crosstalk report.
- Fail if any QSPI net runs parallel to another QSPI net or to USB_DM/DP for more than 3 mm.
- Fail if XIN/XOUT/XOUT_MCU run parallel to any QSPI net at all.

**Deliverable:** `hardware/pcb/v2/tools/check_routing.py` taking `.kicad_pcb` and `decoupling_targets.json` + a new `routing_constraints.json` with the per-net length budgets. Pass/fail contract like `check_decoupling.py`.

---

## Phase F — Final verification gauntlet

Run in this order, all must pass, before raising the PR:

1. `kicad-cli sch erc --severity-error --exit-code-violations` → 0
2. `hardware/pcb/v2/tools/check_decoupling.py` → 16/16 PASS
3. `hardware/pcb/v2/tools/check_all_placement.py` (Phase B deliverable) → all PASS
4. `kicad-cli pcb drc --exit-code-violations` → 0
5. `hardware/pcb/v2/tools/check_routing.py` (Phase E deliverable) → all PASS
6. `analyze_pcb.py` dfm_tier → `standard` or better, 0 violations
7. `kicad-cli pcb export gerbers` → clean
8. Visual review in pcbnew 3D viewer (human).
9. `check_all.sh` (Phase A deliverable) runs all of the above as one step and exits 0.

---

## Phase G — Fab prep

After Phase F passes:

1. `kicad-cli pcb export gerbers -o /tmp/gerbers/ ...` — JLC standard layers
2. `kicad-cli pcb export pos -o /tmp/pos.csv` — pick-and-place
3. `kicad-cli sch export bom -o /tmp/bom.csv` with LCSC part numbers
4. Run JLC DFM via `kicad-happy jlcpcb` skill (upload gerbers)
5. Quote, order, wait 2 weeks, solder, bring-up.

---

## Open questions / deferred items

- **Locked-position flag on MH1/MH2/MH3.** The audit showed `locked=False` in the PCB file, but these positions are fixed by the phone enclosure. Either set `(locked yes)` on the footprints or add them to `placement_constraints.json` with a zero-movement constraint.
- **SW1 on B.Cu.** Confirmed bottom-side. Verify the enclosure cutout is on the right side.
- **JST connector MP (mechanical) pads** — currently unconnected. Standard practice varies. Decide once: leave floating, or tie to GND for EMI shielding. If tie: add a schematic net-tie to each J*.MP.
- **USB connector decision.** Currently R3/R4 terminate at explicit NC markers. If we ever want USB for programming/data, we need to add a USB-C footprint (or similar) and route D+/D- to it. Defer until bring-up proves SWD is sufficient.
- **R6 QSPI_SS pullup** — removed. If a future revision switches to a flash variant that actually needs it (e.g., larger W25Q variants with different reset behavior), revisit.
- **The crystal case pads (Y1.2, Y1.4)** should stitch to the bottom GND plane near Y1 to reduce noise pickup. Add a stitching via per pad.

---

## Reference document index

Pinned for future sessions to avoid re-fetching:

| doc | purpose | local path or URL |
|---|---|---|
| RP2040 datasheet | power supply §2.9, crystal §2.16 | https://datasheets.raspberrypi.com/rp2040/rp2040-datasheet.pdf |
| Hardware design with RP2040 (RP-008279) | minimal design example, layout prose | /tmp/rp2040-hw-design.pdf (local cache) |
| Raspberry Pi Minimal-KiCAD ref | authoritative placement geometry | /tmp/minimal-kicad/ (local cache) |
| RPi Press "Design an RP2040 board with KiCad" ch05/10/11 | reference RP2040 designs | /home/justin/src/raspberry-pi-pico-with-kicad/eg/ |
| W25Q16JV datasheet | flash VCC decap, CS behavior | https://www.winbond.com/resource-files/w25q16jv%20spi%20revg%2003222018%20plus.pdf |
| Abracon ABM8 | crystal load caps, pinout | https://abracon.com/Resonators/ABM8.pdf |
| TLV320AIC3104 datasheet (SLAS510C) | codec | https://www.ti.com/lit/ds/symlink/tlv320aic3104.pdf |
| DRV8871 datasheet | ringer driver | https://www.ti.com/lit/ds/symlink/drv8871.pdf |
| LM2596 datasheet | buck | https://www.ti.com/lit/ds/symlink/lm2596.pdf |
| AMS1117-3.3 datasheet | LDO | https://www.advanced-monolithic.com/pdf/ds1117.pdf (verify exact vendor URL during Phase B2 cluster audit; prior link was wrong) |
