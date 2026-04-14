# Digits PCB v2

Carrier board that sits under a Raspberry Pi Zero 2 W inside a gutted vintage desk phone. Provides power (12 V → 5 V → 3.3 V), onboard audio codec for handset mic + earpiece, an RP2040 microcontroller that runs keypad scanning + hookswitch + bell ringer, and all the mechanical/electrical interfaces to the phone shell.

This directory is the **single source of truth for PCB v2**. Schematic, footprint placement, validation rules, and design notes all live here. If something about this board is not documented in this directory, it is not a design decision — it is an accident that needs to be fixed.

---

## Sources of truth — what trumps what

When two artefacts disagree, the higher entry in this table wins. Never argue with ERC or DRC; fix the schematic or the doc.

| Rank | Artefact | Authority |
|---|---|---|
| 1 | `kicad/digits-pcb.kicad_sch` (KiCad schematic) | Canonical electrical netlist. If a doc says a pin is wired to X and the schematic says Y, the schematic is correct and the doc is stale. |
| 2 | `kicad/digits-pcb.kicad_pcb` (KiCad board) | Canonical physical placement and routing. Authoritative for component positions, layer assignments, copper geometry. |
| 3 | `decoupling_targets.json` | Enforceable placement contract for RP2040 cluster decoupling caps. Each cap's allowed max-distance-to-pad is asserted by `tools/check_decoupling.py` against the `.kicad_pcb`. |
| 4 | `NET_TOPOLOGY.md` | Prose description of *why* every net exists and how every IC is wired. Human-readable companion to the schematic. Cite datasheet sections here, not in commit messages. |
| 5 | `COMPONENTS.md` | Per-component catalogue: value, package, LCSC part, purpose, and *which nets each component pin is on*. Read this when you want to know "what does C29 do?". |
| 6 | `CLUSTER_AUDIT_RUNBOOK.md` | **Process doc.** Step-by-step runbook for auditing and placing a cluster (IC + decoupling + pullups + crystal) against its datasheet and a first-party reference design. Every remaining cluster (U6 codec, U1 buck, U5 LDO, U2 motor driver) will be audited with this ritual. |
| 7 | `NEXT_STEPS.md` | Phased plan of remaining work from the current checkpoint (commit `0910411`) through fab. Pick up here after any break. |
| 8 | `tools/README.md` | How to run the validation scripts. Mandatory reading before touching the PCB. |

**Primary-source datasheets and reference designs** are linked from `NET_TOPOLOGY.md` → "References" section and `NEXT_STEPS.md` → "Reference document index". Keep those two lists in sync; if you find a new reference, add it to both.

---

## File map

```
hardware/pcb/v2/
├── README.md                   # this file — start here
├── NET_TOPOLOGY.md             # net-by-net wiring description with citations
├── COMPONENTS.md               # per-component catalogue (values, packages, pins, purpose)
├── CLUSTER_AUDIT_RUNBOOK.md    # process runbook for auditing each IC cluster
├── NEXT_STEPS.md               # phased plan from current checkpoint through fab
├── decoupling_targets.json     # machine-checkable per-pin placement contract
├── kicad/
│   ├── digits-pcb.kicad_pro    # KiCad project file
│   ├── digits-pcb.kicad_sch    # SCHEMATIC (authoritative electrical)
│   ├── digits-pcb.kicad_pcb    # BOARD (authoritative physical)
│   └── ...
├── gerber/                     # last-known-good fab output (stale until regenerated)
└── tools/
    ├── README.md               # how to run the checkers
    ├── check_decoupling.py     # validates decoupling_targets.json against .kicad_pcb
    ├── check_rp2040_bom.py     # validates §2.9 per-pin decoupling against netlist
    ├── inspect_cluster.py      # dumps ground-truth positions for the RP2040 cluster
    └── plan_rp2040_cluster.py  # computes target positions from Minimal-KiCAD reference offsets
```

---

## What to validate before any commit

Ordered gate. Each step must pass before the next starts. A commit touching `kicad/*` must have run all of these. Future phases (see `NEXT_STEPS.md`) will add `kicad-cli pcb drc`, `tools/check_all_placement.py`, and `tools/check_routing.py` to this list.

### 1. Canonical schematic ERC (mandatory)

```bash
kicad-cli sch erc --severity-error --exit-code-violations \
  hardware/pcb/v2/kicad/digits-pcb.kicad_sch -o /tmp/erc.rpt
```

Must report **0 errors**. The MCP `run_erc` tool is **NOT** a valid substitute — it silently skips dangling-label checks. Always use `kicad-cli`.

### 2. Per-pin decoupling placement contract (RP2040 cluster)

```bash
python3 hardware/pcb/v2/tools/check_decoupling.py
```

Must report **all constraints satisfied**. Enforces `decoupling_targets.json`. When adding or moving a cap in the RP2040 cluster, update `decoupling_targets.json` in the same commit.

### 3. RP2040 BOM invariant check

```bash
kicad-cli sch export netlist --format kicadxml -o /tmp/sch.xml \
  hardware/pcb/v2/kicad/digits-pcb.kicad_sch
python3 hardware/pcb/v2/tools/check_rp2040_bom.py
```

Must report **ALL PASS**. Verifies the RP2040 datasheet §2.9 per-pin decoupling net topology.

### 4. PCB analyzer (courtyard + DFM)

```bash
python3 /home/justin/src/kicad-happy/skills/kicad/scripts/analyze_pcb.py \
  hardware/pcb/v2/kicad/digits-pcb.kicad_pcb --compact
```

Review: `placement_analysis.courtyard_overlaps`, `dfm.violation_count`, `tombstoning_risk`. Small (< 0.5 mm²) courtyard touches between adjacent decoupling caps at the QFN-edge decoupling ring are expected and acceptable; body/pad clearance violations are not.

### 5. Symbol ↔ footprint pad audit

No dedicated script yet; run this ad-hoc Python when suspicious:

```python
# see NEXT_STEPS.md Phase A for the "make check" plan
```

Confirmed-clean as of commit `0910411`:
- `Y1` → `Device:Crystal_GND24` + `Crystal_SMD_3225-4Pin_3.2x2.5mm` (signals on diagonal pads 1/3, GND on 2/4)
- `R9` XOUT damping resistor present and wired as `U3.21 → XOUT_MCU → R9.1 / R9.2 → XOUT → Y1.3`
- `R6` (legacy QSPI_SS pullup) removed
- All U1/U2/U3/U6 `""`-numbered footprint pads are standard thermal-stencil reliefs, not missing nets
- All `J*.MP` unconnected pads are JST mechanical mounting tabs, intentionally floating

---

## Do-not-regress invariants

These are the things we have already caught once; regressing any of them is a production defect.

- **Never use `Device:Crystal` (2-pin) with a 4-pad crystal footprint.** The signals are diagonal on the physical part; the 2-pin symbol wires XOUT to a case-GND pad and the crystal never oscillates. Use `Device:Crystal_GND24` (pins 1/3 signal, 2/4 GND).
- **XOUT must pass through R9 (1 kΩ) before reaching Y1.3.** Raspberry Pi *Hardware design with RP2040* §2.3 mandates this for IOVDD = 3.3 V. Do not omit; do not substitute 0 Ω.
- **C5/C6 crystal load caps are 15 pF C0G 0402.** Previous rev had 22 pF which corresponds to no real-world crystal.
- **Y1 is specifically Abracon ABM8-272-T3**, not a generic 12 MHz crystal. RP2040 datasheet §2.16.1.1 says Pico was tuned for this exact part.
- **Six IOVDD caps (C12–C16 + C28), not five.** RP2040 has 6 IOVDD pins. Per-pin decoupling is mandated.
- **Both DVDD pins get 100 nF** (C29 on pin 23, C30 on pin 50). Plus the 1 µF bulk C10 on VREG_VOUT.
- **`decoupling_targets.json` is the contract.** When a cap moves, `check_decoupling.py` must still pass. If the checker and the board disagree, fix whichever is wrong — do not loosen the threshold silently.
- **Never use `mcp__kicad__run_erc` alone.** It skips the dangling-label rule. Always cross-check with `kicad-cli sch erc`.

---

## Where the current session left off

Commit `0910411` places the full RP2040 cluster against the Raspberry Pi Minimal-KiCAD reference geometry (3.05 mm uniform radial decoupling ring). All gates green (ERC, decoupling contract, BOM invariants, PCB analyzer). Schematic is fab-valid; **nothing is routed** and the other clusters (buck, LDO, codec, motor driver, Pi header, connectors) have not been audited against their datasheets. See `NEXT_STEPS.md` for the phased plan to take this board the rest of the way.
