#!/usr/bin/env python3
"""Run kicad-cli pcb drc and report only "real" violations.

The default DRC report includes:
- silk_over_copper / silk_overlap / silk_edge_clearance: cosmetic, fixed by
  silk-screen edits or ignored.
- unconnected_items: tracked separately by routing progress, not an error
  while the board is mid-build.
- starved_thermal: cosmetic, refill-time concern.
- lib_footprint_mismatch: cosmetic if intentional.

Real fab blockers:
- shorting_items: copper of different nets touching. Will short the board.
- clearance: copper too close to other copper. Will fail at fab or short.
- copper_edge_clearance: copper too close to board edge. Will fail at fab.
- tracks_crossing: two tracks on the same layer cross. Real wiring bug.
- track_dangling: track has an unconnected end. Real wiring bug.
- courtyards_overlap: footprint courtyards overlap. Components physically
  collide; will fail at assembly.
- solder_mask_bridge: soldermask too narrow between adjacent pads. Risk
  of solder bridging at assembly.

Exit 0 if no real issues, 1 otherwise. Does not refill zones (use kicad-cli
pcb refill-zones first if needed).
"""
from __future__ import annotations

import json
import pathlib
import subprocess
import sys

REPO_ROOT = pathlib.Path(__file__).resolve().parents[4]
PCB = REPO_ROOT / "hardware/pcb/v2/kicad/digits-pcb.kicad_pcb"

REAL_TYPES = {
    "shorting_items",
    "clearance",
    "copper_edge_clearance",
    "tracks_crossing",
    "track_dangling",
    "courtyards_overlap",
    "solder_mask_bridge",
}


def main() -> int:
    out = pathlib.Path("/tmp/drc-real.json")
    r = subprocess.run(
        ["kicad-cli", "pcb", "drc", str(PCB), "-o", str(out), "--format", "json"],
        capture_output=True,
        text=True,
    )
    if not out.exists():
        print(f"ERROR: kicad-cli pcb drc did not produce {out}", file=sys.stderr)
        print(r.stdout, file=sys.stderr)
        print(r.stderr, file=sys.stderr)
        return 2
    d = json.loads(out.read_text())
    violations = d.get("violations", [])

    use_color = sys.stdout.isatty() and "NO_COLOR" not in __import__("os").environ
    GREEN = "\033[32m" if use_color else ""
    RED = "\033[31m" if use_color else ""
    BOLD = "\033[1m" if use_color else ""
    DIM = "\033[2m" if use_color else ""
    RESET = "\033[0m" if use_color else ""

    real = [v for v in violations if v.get("type") in REAL_TYPES]
    cosmetic = [v for v in violations if v.get("type") not in REAL_TYPES]

    by_type: dict[str, list] = {}
    for v in real:
        by_type.setdefault(v.get("type", "?"), []).append(v)

    for t in sorted(by_type.keys()):
        items = by_type[t]
        print(f"{BOLD}{RED}=== {t} ({len(items)}) ==={RESET}")
        for v in items:
            its = v.get("items", [])
            if not its:
                continue
            pos = its[0].get("pos", {})
            descs = [i.get("description", "") for i in its]
            print(f"  ({pos.get('x',0):6.2f}, {pos.get('y',0):6.2f}) {' | '.join(descs)}")
        print()

    print(f"{DIM}cosmetic / deferred (silk, thermal, unconnected, lib_mismatch): {len(cosmetic)}{RESET}")
    print()
    if real:
        print(f"{BOLD}{RED}FAIL: {len(real)} real DRC issues across {len(by_type)} categories.{RESET}")
        return 1
    print(f"{BOLD}{GREEN}OK: no real DRC issues.{RESET}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
