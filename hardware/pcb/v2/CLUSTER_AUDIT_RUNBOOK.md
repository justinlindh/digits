# Runbook — Cluster audit and placement for an IC on PCB v2

This is the process that took the RP2040 cluster (U3 and its dependents) from a hallucinated BOM to a fab-valid placement matched against the first-party Raspberry Pi Minimal-KiCAD reference project. It is the same process to apply to the **TLV320AIC3104 codec (U6)**, the **LM2596 buck (U1)**, the **AMS1117 LDO (U5)**, and the **DRV8871 motor driver (U2)**. Each of those is a separate audit; do them one at a time.

The ritual has a strict order. Earlier steps catch defects that make later steps pointless. Do not skip steps, even if a step looks obviously fine, because every "obvious" step caught something real for the RP2040 cluster. Specific defects caught by each step are documented below the step so future runs know what to watch for.

---

## Before you start

1. **Close KiCad.** MCP tools overwrite files KiCad has open. See memory `feedback_close_kicad_before_agents.md`.
2. **Read this whole runbook once.** The steps reference tools and patterns that make more sense in sequence.
3. **Read the current state of the cluster** from `hardware/pcb/v2/NET_TOPOLOGY.md` and `COMPONENTS.md`. Assume both are partially stale; trust them as starting hypotheses, not ground truth. Ground truth comes from the schematic + the datasheet.
4. **Pick exactly one cluster.** Do not audit more than one IC at a time. Cross-cluster ambiguity is the single biggest source of errors. The cluster is the IC + its decoupling, pullups, series resistors, and any discrete parts whose sole purpose is to support that IC.

---

## Step 1 — Gather primary sources

Collect every primary-source document that governs the cluster:

| source | where to look |
|---|---|
| Manufacturer datasheet | TI / Abracon / Winbond / Raspberry Pi website. Download the PDF to `/tmp/` so a subagent can parse it. |
| Manufacturer hardware design guide | E.g. RP-008279 "Hardware design with RP2040", TI SLAU EVM user guides. These usually contain the minimal-design schematic + layout guidance absent from the raw datasheet. |
| Reference project (first-party) | E.g. `https://datasheets.raspberrypi.com/rp2040/Minimal-KiCAD.zip`, TI EVM KiCad files, vendor eval boards. First-party geometry trumps everything else. Cache to `/tmp/`. |
| Reference project (third-party) | E.g. the Raspberry Pi Press book's ch05/ch10/ch11 KiCad files at `/home/justin/src/raspberry-pi-pico-with-kicad/eg/`. Useful as a sanity check but author interpretation, not authoritative. |
| LCSC / Digi-Key part datasheets | For any discrete on the cluster: the specific resistor / cap / inductor / diode's electrical + package drawing. |

**If the datasheet is a PDF that WebFetch cannot parse fully, dispatch a general-purpose subagent** with `Read` + PDF support to extract the sections you need. Past-session example prompt is at the bottom of this file under "Subagent prompt templates".

Output: a short bullet list of source URLs/paths, one per document, committed to a `/tmp/<cluster>-sources.md` scratch note if you like. The final versions of the important ones get pinned in the `NEXT_STEPS.md` reference index.

**Defects this step caught for RP2040:**
- "22 pF crystal load caps" from an unknown source (not in any datasheet or reference design) — corrected to 15 pF after reading RP2040 DS §2.16.
- Missing XOUT damping resistor R9 — only documented in RP-008279 §2.3, not the raw datasheet.

---

## Step 2 — Schematic-level audit against the datasheet

Compare the schematic (nets, component values, footprints) against the datasheet's mandatory requirements. Use a subagent for an unbiased second opinion.

Procedure:

1. Export the canonical netlist:
   ```bash
   kicad-cli sch export netlist --format kicadxml \
     -o /tmp/sch.xml hardware/pcb/v2/kicad/digits-pcb.kicad_sch
   ```
2. Write a small Python checker that loads `/tmp/sch.xml` and asserts every datasheet-mandated invariant. For the RP2040, this is `tools/check_rp2040_bom.py`. For a new cluster, copy that file and adapt the invariant list. Invariants include:
   - Per-pin decoupling: for each listed power pin, at least N caps of the right value are on the right rail.
   - Bulk caps: the datasheet's min bulk capacitance is present on each rail.
   - Pullups / pulldowns at the right values on the right pins.
   - Crystal / clock / reset circuitry matches the datasheet's typical-application topology.
   - Connectors on the cluster have the right pin-to-net mapping (watch for mirrored/rotated connectors).
3. Dispatch a **general-purpose subagent** with the cluster's schematic file path and the datasheet URL and ask it to audit independently. Past-session prompt at the bottom. The subagent's main value is catching things you did not think to check.
4. Run canonical ERC after every schematic fix:
   ```bash
   kicad-cli sch erc --severity-error --exit-code-violations \
     hardware/pcb/v2/kicad/digits-pcb.kicad_sch -o /tmp/erc.rpt
   ```
   **Do not trust `mcp__kicad__run_erc` alone** — it silently skips the dangling-label check. See `feedback_kicad_erc_canonical.md`.

**Defects this step caught for RP2040:**
- Missing per-pin IOVDD, DVDD, VREG_VIN, USB_VDD, ADC_AVDD decoupling (6 + 2 + 1 + 1 + 1 = 11 caps missing or mis-specced).
- C10 bulk was 10 µF 0603 — should be 1 µF 0402 per Pico reference.
- R6 QSPI_SS pullup existed on schematic with an incorrect "enables BOOTSEL" rationale; Pico and all three RPi Press references omit it. Removed.
- Flash (U4) VCC had no decap. Added C34 per Winbond DS + ref designs.
- XOUT had no damping resistor. Added R9 per RP-008279 §2.3.

---

## Step 3 — Symbol ↔ footprint pin/pad audit

For every component in the cluster, verify the schematic symbol pin count equals the PCB footprint pad count, or that the mismatch is a documented cosmetic (thermal stencil, mechanical mount).

Procedure:

1. Run the symbol/footprint pad audit (paste this inline; we have not yet extracted it into a dedicated script — worth doing for the next cluster):
   ```python
   import xml.etree.ElementTree as ET, pathlib, collections
   import sys
   sys.path.insert(0, 'hardware/pcb/v2/tools')
   from check_decoupling import load_pcb

   tree = ET.parse('/tmp/sch.xml').getroot()
   sym_pins = collections.defaultdict(set)
   for net in tree.findall(".//nets/net"):
       for node in net.findall("node"):
           sym_pins[node.get("ref")].add(node.get("pin"))
   fps = load_pcb(pathlib.Path('hardware/pcb/v2/kicad/digits-pcb.kicad_pcb'))
   for ref in sorted(set(list(sym_pins.keys()) + list(fps.keys()))):
       sp = sym_pins.get(ref, set())
       pp = set(fps.get(ref, {}).get("pads", {}).keys())
       missing = pp - sp; extra = sp - pp
       if missing or extra:
           print(f"{ref}: missing {sorted(missing)}  extra {sorted(extra)}")
   ```
2. Classify each mismatch:
   - **Real defect** — symbol has fewer pins than the footprint has *electrical* pads (e.g., a 2-pin crystal symbol on a 4-pad crystal footprint with two signal pads and two GND case pads).
   - **Cosmetic stencil** — extra `""`-numbered thermal stencil pads under an EP that is already wired via its numbered pad. No action.
   - **Mechanical mount** — extra `MP` pads on JST connectors. No action.
3. For every real defect, swap to the correct symbol (or update the footprint). After any symbol swap, re-run Step 2's ERC gate.

**Defects this step caught for RP2040:**
- `Y1` used `Device:Crystal` (2 pin) against `Crystal_SMD_3225-4Pin_3.2x2.5mm` (4 pad). The ABM8 has diagonal signal pads (1/3) and case-GND pads (2/4). KiCad wired XOUT into pad 2 (GND), leaving pad 3 (Xo) floating — the crystal could not have oscillated. Fixed by swapping to `Device:Crystal_GND24`.

---

## Step 4 — Build the machine-checkable placement contract

Every cluster's layout constraints go into a JSON file and a checker script. When the constraints and the board disagree, the checker tells you so in CI (or before commit), so placement defects cannot hide.

Procedure:

1. Copy `decoupling_targets.json` to `<cluster>_targets.json` (or add entries to the existing file if the new cluster shares the contract).
2. For each constrained component, write an entry with:
   - `cap` or `part` — the reference designator
   - `target` — the target pad in `REF.padnum` form, e.g. `U6.17` for the codec's AVDD pin
   - `max_distance_mm` — from the datasheet, or from reference measurements (see Step 5). Start conservative (datasheet "close") and tighten once you have reference data.
   - `rationale` — a short citation to the datasheet section or reference design
3. Copy `check_decoupling.py` to `check_<cluster>_placement.py` or extend it. The script should read the targets JSON and the `.kicad_pcb` and compute centroid-to-pad distance per entry, exit non-zero on failure.
4. Run the checker. At this point, most entries will fail — the components are still placed by hand in non-reference positions. That is expected. The failures are your punch list for Step 6.

**Defects this step caught for RP2040:**
- Pre-existing IOVDD caps C12–C16 were all 2.58–2.78 mm from their target pins — *just* over the 2.5 mm datasheet threshold. C16 was 12.7 mm from its target, wildly wrong.
- The checker also became the post-F8-sync punch list: after F8 drops new caps at origin (50+ mm away), the checker lists every cap that still needs placing.

---

## Step 5 — Extract authoritative reference geometry

Do not hand-guess placement offsets. Pull a first-party reference design and measure it.

Procedure:

1. Download the reference `.kicad_pcb` to `/tmp/`. For RP2040 this was `https://datasheets.raspberrypi.com/rp2040/Minimal-KiCAD.zip`. For the codec it will be TI's `TLV320AIC3104EVM` reference PCB or similar.
2. Dispatch a **general-purpose subagent** to parse the reference PCB and produce a per-pin offset table (cap ref, value, absolute position, position relative to the IC center in the IC's un-rotated frame, cap rotation relative to IC rotation, distance to the nearest power pad, which QFN/QFP edge the pad is on). Template at the bottom of this file.
3. Read the report and extract:
   - The uniform radial offset (for RP2040: 3.05 mm)
   - The rotation convention (for RP2040: left=180°, right=0°, top=90°, bottom=270°; pin 1 of each cap facing the IC)
   - Whether the reference uses single-row or staggered rows along each edge
   - Crystal / flash / special-case placements
4. Update the max_distance_mm values in the targets JSON to match the reference's observed distances (+ small margin for placement grid). For RP2040, the reference is at 3.05 mm uniform; we tightened from a datasheet-conservative 2.5 mm to a reference-matched 3.2 mm.

**Defects this step caught for RP2040:**
- The ch05 RPi Press reference uses shared caps for some power pins; the first-party Minimal-KiCAD reference is stricter 1:1. Knowing this made the "pin 48/49 can share a cap" compromise from RP-008279 §2.3 a *conscious* choice rather than a hidden assumption.

---

## Step 6 — Compute target positions and present a preview table

Before moving anything, compute every target position algorithmically and present it to the user for approval. Never move first and show later.

Procedure:

1. Copy `plan_rp2040_cluster.py` to `plan_<cluster>.py`. The script should:
   - Read the current `.kicad_pcb` via `inspect_cluster.py`-style parsing to get the IC center and pad positions
   - Apply the reference's uniform radial offset to each pad to produce a target `(x, y, rotation)` tuple
   - Assign caps to pins deterministically (strict 1:1 unless the datasheet says otherwise)
   - Compute a body-overlap audit between all targets (use `body_halfsize(rot) = (0.5, 0.25)` for rot 0/180 and `(0.25, 0.5)` for rot 90/270 for 0402)
   - Check target positions against locked components (MH*, SW1) and board edge
2. Run the planner. Review the output. If anything looks off (a cap lands inside an adjacent IC, two caps collide by more than a small courtyard margin, the reference offset is unrepresentable because of board geometry), iterate the plan before showing it.
3. Present the preview table to the user. Include: current position, target position, distance to target pad, rotation, and any flagged collisions. Wait for explicit approval.

**Defects this step caught for RP2040:**
- A first pass had all top-edge caps at 1.6 mm offset (my hand-guess) which did not match the reference's 3.05 mm. Fixing this was cheap at Step 6 and expensive at Step 7.
- C34 (flash VCC) target landed inside U5 (LDO) body — placement preview flagged it before any MCP move.
- R3/R4 USB series resistors landed inside J4 footprint — flagged and relocated.

---

## Step 7 — Execute moves in small batches, re-check after every batch

Move 3–6 components per batch. After each batch, re-run the placement checker and the overlap analysis. If a batch regresses any previously-passing check, stop, investigate, and fix before the next batch.

Procedure:

1. `mcp__kicad__open_project` on the `.kicad_pro` to freshen MCP state. If the user has edited the PCB externally (e.g. after F8 sync), MCP needs a fresh open or its next write will clobber the user's changes — see `feedback_mcp_pcb_stale_state.md`.
2. Call `mcp__kicad__move_component` with `position` and `rotation` for each component in the batch. MCP `move_component` accepts `unit: "mm"` — use it explicitly.
3. `mcp__kicad__save_project` after each batch.
4. Re-run the placement checker and `analyze_pcb.py`. Capture the overlap delta.
5. If any overlap is > 0.5 mm² body penetration, or any threshold regression, stop and adjust.
6. Sub-0.5 mm² courtyard touches between adjacent ring caps at QFN-edge pin pitch are acceptable — the Minimal-KiCAD reference has the same pattern.

**Defects this step caught for RP2040:**
- First-pass R9 position collided with Y1 body (0.59 mm² overlap). Moved R9 to a cleared corner.
- Bottom cap row at 0.4 mm pin pitch required a second staggered row, then reduced to a single row with offset-from-pin trick (cap body at `x_pin ± 0.4`).

---

## Step 8 — Broad overlap sweep and real-defect vs cosmetic triage

After all moves are done, run the whole-board overlap analysis and categorise each hit.

Procedure:

```bash
python3 /home/justin/src/kicad-happy/skills/kicad/scripts/analyze_pcb.py \
  hardware/pcb/v2/kicad/digits-pcb.kicad_pcb --compact \
  | python3 -c "import sys, json; d=json.loads(sys.stdin.read()); [print(o) for o in d['placement_analysis']['courtyard_overlaps']]"
```

For each overlap, ask:
1. Is the component pair in this cluster, or pre-existing from another cluster? (Out-of-cluster is deferred to that cluster's audit.)
2. Is the overlap a body/pad clearance violation, or a courtyard margin touch?
3. If it is a courtyard margin touch between two decoupling caps at the datasheet-required radial offset, is it below ~0.4 mm² and present in the reference design? If yes, accept.
4. Otherwise, nudge or rotate to clear.

**Defects this step caught for RP2040:**
- Nine 0.22 mm² cap-to-cap courtyard touches at the QFN-edge ring — accepted as reference-matched.
- C5/C6 vs Y1 at 0.38 mm² each — accepted, body clearance fine.
- R9 vs bottom cap row — moved R9 out of the squeeze zone.

---

## Step 9 — Commit and update docs

Every cluster audit produces multiple commits, not one. Split them so each is reviewable:

1. Schematic fixes (value / footprint / symbol / new components). One commit per logical fix, with datasheet citations in the commit message.
2. Placement contract update (`<cluster>_targets.json` + checker script additions or updates).
3. Placement moves (one commit that moves the whole cluster at once — reviewing move-by-move is noise).
4. Doc updates (`NET_TOPOLOGY.md`, `COMPONENTS.md`, add a "do not regress" entry to `README.md` § Do-not-regress invariants for anything nontrivial you caught).

After each commit:
- `kicad-cli sch erc` — 0 errors
- `check_decoupling.py` / `check_<cluster>_placement.py` — all PASS
- `kicad-cli pcb drc` (when Phase A of NEXT_STEPS.md lands)
- `analyze_pcb.py` — no new body/pad violations vs baseline

**Defects this step caught for RP2040:**
- A dangling-label ERC error from MCP `delete_schematic_component` leaving labels behind after R6 removal. Only `kicad-cli sch erc` caught this; MCP `run_erc` reported clean.

---

## Subagent prompt templates

### Template: unbiased cluster audit

```
You are auditing a custom <CHIP> cluster on a carrier board against reference designs and primary datasheets. Your job is to be unbiased and rigorous — do NOT assume the schematic is correct; do NOT assume it is wrong. Check everything relevant to the <CHIP> cluster (the chip + its decoupling + any pullups/pulldowns + series resistors + crystal or reference oscillator if applicable) and report findings.

SCOPE: <CHIP> cluster only. Ignore every other cluster.

FILES TO READ:
- Target schematic: /home/justin/src/digits/hardware/pcb/v2/kicad/digits-pcb.kicad_sch
- Reference <N>: <path or URL>
- Project net topology doc: /home/justin/src/digits/hardware/pcb/v2/NET_TOPOLOGY.md

TOOLING:
- /home/justin/src/kicad-happy/skills/kicad/scripts/analyze_schematic.py <path>
- kicad-cli sch export netlist --format kicadxml -o /tmp/out.xml <path>

PRIMARY DATASHEETS: <list URLs, cite sections>

WHAT TO CHECK: <enumerate the specific datasheet-mandated invariants>

OUTPUT: structured report, 400-800 words. Findings numbered by severity. Each finding must have: severity (blocker / must-fix / should-fix / nit), claim, evidence (file path + ref + net), citation (datasheet section). Explicit "no issue" list for categories you checked and found clean. Things you could not verify from files alone.
```

### Template: reference PCB geometry extraction

```
Download and measure the <REFERENCE PROJECT>. Goal: per-power-pin placement offset table.

URL: <url to zip or .kicad_pcb>

STEPS:
1. Download to /tmp/<name>.zip and extract.
2. Locate the .kicad_pcb file.
3. Parse it (use the compat parser at /home/justin/src/digits/hardware/pcb/v2/tools/check_decoupling.py's load_pcb + add a fallback for fp_text reference).
4. Identify the <CHIP> footprint. Get its center x/y/rotation.
5. For each pin in <PIN LIST>, find the nearest cap and compute: cap ref, value, absolute position, rotation relative to chip rotation, position relative to chip center in the chip's unrotated frame, distance from cap center to pad center, offset from pad center to cap center (dx, dy), which edge of the chip the pad lives on.
6. Also locate and report: crystal / reference oscillator, flash, any critical discretes.

OUTPUT: plain text table per the RP2040 session's /tmp/extract_ch05_v2.py output format, under 800 words.
```

---

## Checklist — did you do every step?

Copy this into your working notes for each cluster audit:

- [ ] Step 1: primary sources gathered, cached locally
- [ ] Step 2: schematic-level audit, subagent dispatched, ERC clean via `kicad-cli`
- [ ] Step 3: symbol↔footprint pad audit, every mismatch classified and fixed or accepted
- [ ] Step 4: placement contract JSON + checker script
- [ ] Step 5: reference geometry extracted, offsets + rotation conventions documented
- [ ] Step 6: target positions computed, preview table shown, user approved
- [ ] Step 7: moves executed in batches, checker re-run after each batch
- [ ] Step 8: overlap sweep, real vs cosmetic triage
- [ ] Step 9: commits split logically, docs updated, do-not-regress invariants added to `README.md`

If any checkbox is not ticked, the cluster is not audited.
