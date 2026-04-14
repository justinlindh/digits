# PCB v2 tools

Small Python checkers that enforce constraints KiCad's native DRC cannot express.

## `check_decoupling.py` — per-pin decoupling placement check

**Purpose.** The RP2040 datasheet §2.9 requires a 100 nF cap close to each of the chip's six IOVDD pins, both DVDD pins, VREG_VIN, USB_VDD, and ADC_AVDD. KiCad's schematic symbol collapses all IOVDD pins into a single node, so once you've added the caps in the schematic the "one per physical pin" requirement becomes purely a PCB-layout concern. KiCad DRC has no rule for "component must be within N mm of a pad", so this script enforces it externally.

**Source of truth.** `hardware/pcb/v2/decoupling_targets.json` — one entry per constrained cap, each naming its target pad (`U3.49`, `U4.8`, etc.), its value, and its maximum centroid-to-pad distance in mm. Add or remove entries there when the decoupling topology changes; do not edit the checker.

**Run it:**

```
python3 hardware/pcb/v2/tools/check_decoupling.py
```

Defaults resolve from the repo root — no args needed. Use `--pcb` and `--targets` to override, `--quiet` to only list failures.

Exit code: `0` pass, `1` at least one constraint violated, `2` missing file.

**When to run:**
- After any placement change in the RP2040 cluster (U3, U4, Y1, C5, C6, C10, C12–C16, C28–C34).
- After every F8 sync from schematic to PCB — the sync will not move caps for you, so freshly-added caps land at the PCB origin and MUST be placed before the check passes.
- As the final gate before committing a layout change to `digits-pcb.kicad_pcb`.
- Before raising a PR that touches PCB v2.

**When a check fails,** fix the placement in pcbnew (drag cap closer to its target pad) and rerun. Do not widen `max_distance_mm` unless you have a new datasheet citation justifying it.

## Mandatory for future sessions

Any Claude Code (or human) session that places or moves components in the RP2040 cluster on PCB v2 MUST:

1. Read `decoupling_targets.json` before moving anything — it is the contract.
2. Run `check_decoupling.py` after placement changes and before committing.
3. Update `decoupling_targets.json` in the SAME commit if the decoupling topology changes (new cap, removed cap, reassigned target pin).

This file exists because the datasheet constraint is easy to forget during routing. Don't forget it.
