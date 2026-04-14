#!/usr/bin/env python3
"""Compute target positions for the TLV320AIC3104 codec cluster.

Reads U6 (codec) and U7 (XC6206 LDO) positions from the current .kicad_pcb
and produces target (x, y, rot) for each of the 20 cluster components. The
layout is hand-tuned (not algorithmic) because the 0.5 mm QFN pin pitch
plus 0402 cap bodies force specific choices that don't emerge from a pure
radial-offset algorithm. Pattern borrowed from plan_rp2040_cluster.py.

Layout:
- Left edge (pins 1-8):    C40 at pin 7 (IOVDD), alone
- Bottom edge (pins 9-16): C46, C47, C48 in the "inner ring" at priority
                           positions; C49-C52 + R10 in a looser outer row
- Right edge (pins 17-24): C41, C42 at the two DRVDD pins
- Top edge (pins 25-32):   C43 at AVDD, C53/R11 at RESET, C38/C39 at DVDD
- LDO area:                C36 beside U7 pin 3 (VIN), C37 beside U7 pin 2 (VOUT)
- Bulk caps:               C44, C45 above U6 in empty space

Run with --json /tmp/codec_targets_plan.json to emit a machine-readable
target list for an apply step.
"""
from __future__ import annotations

import argparse
import json
import math
import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
from check_decoupling import load_pcb  # noqa: E402

REPO_ROOT = pathlib.Path(__file__).resolve().parents[4]
PCB = REPO_ROOT / "hardware/pcb/v2/kicad/digits-pcb.kicad_pcb"

# Radial offsets in mm (center-to-center from QFN pad to cap body).
INNER = 1.50  # priority: critical decoupling and mic signal caps
OUTER = 2.60  # less critical: unused input termination, series resistors


def body_halfsize(rot: float) -> tuple[float, float]:
    """0402 body is 1.0 x 0.5 mm. Return half-widths after rotation."""
    if abs(rot) % 180 < 0.5:
        return (0.5, 0.25)
    return (0.25, 0.5)


def build_plan(u6, u7):
    """Return a list of (ref, target_pin, x, y, rot, rationale) tuples.

    Positions are in absolute PCB coordinates (mm).
    """
    def p(ic, n): return ic["pads"][str(n)]

    plan = []

    # ---------------- LEFT EDGE (pins 1-8) ----------------
    # Only IOVDD (pin 7) gets a cap on this edge. Radial direction: -x.
    pad = p(u6, 7)
    plan.append(("C40", "U6.7",
                 pad[0] - INNER, pad[1], 0,
                 "IOVDD (pin 7) close-in decoupling, 100nF"))

    # ---------------- BOTTOM EDGE (pins 9-16) ----------------
    # Priority caps in an inner row at INNER offset below the edge (+y).
    # Pin pad y ~= 94.710 for all of them; inner row at y = 94.710 + INNER.
    pad10 = p(u6, 10); pad11 = p(u6, 11); pad15 = p(u6, 15)

    inner_y = pad10[1] + INNER
    plan.append(("C46", "U6.10",
                 pad10[0], inner_y, 90,
                 "MIC1LP (pin 10) AC coupling to MIC_FROM_SW, 0.47uF"))
    plan.append(("C47", "U6.11",
                 pad11[0], inner_y, 90,
                 "MIC1LM (pin 11) AC coupling to AGND, 0.47uF"))
    plan.append(("C48", "U6.15",
                 pad15[0], inner_y, 90,
                 "MICBIAS (pin 15) bypass to AGND, 100nF"))

    # Unused analog inputs + MICBIAS series resistor in a looser outer row
    # at y = pad_y + OUTER, with 1.0 mm tangential pitch so bodies don't
    # collide. Five components across pins 12-16 span (2.5 mm) would be
    # too tight at 0.5 mm pitch, so we spread them to 1.0 mm pitch
    # starting from the leftmost pin on the row.
    outer_y = pad10[1] + OUTER
    # Start at x = pad12.x - 0.5 (slightly left of pin 12) so the row is
    # centered-ish across pins 12-16 at 1.0 mm pitch.
    pad12_x = p(u6, 12)[0]   # 82.500 in our case
    row_start_x = pad12_x - 0.5  # 82.000
    row_pitch = 1.0
    outer_row = [
        ("C49", "U6.12", "MIC1RP (pin 12) unused input termination, 100nF"),
        ("C50", "U6.13", "MIC1RM (pin 13) unused input termination, 100nF"),
        ("C51", "U6.14", "MIC2L  (pin 14) unused input termination, 100nF"),
        ("C52", "U6.16", "MIC2R  (pin 16) unused input termination, 100nF"),
        ("R10", "U6.15", "MICBIAS series resistor 2.2k -> MIC_FROM_SW"),
    ]
    for i, (ref, target, note) in enumerate(outer_row):
        x = row_start_x + i * row_pitch
        plan.append((ref, target, x, outer_y, 90, note))

    # ---------------- RIGHT EDGE (pins 17-24) ----------------
    # Two DRVDD pins (18 and 24) get one cap each. Radial direction: +x.
    pad18 = p(u6, 18); pad24 = p(u6, 24)
    plan.append(("C41", "U6.18",
                 pad18[0] + INNER, pad18[1], 0,
                 "DRVDD (pin 18) close-in decoupling, 100nF"))
    plan.append(("C42", "U6.24",
                 pad24[0] + INNER, pad24[1], 0,
                 "DRVDD (pin 24) close-in decoupling, 100nF"))

    # ---------------- TOP EDGE (pins 25-32) ----------------
    # AVDD at pin 25, RESET + pullup at pin 31, DVDD (100n + 1u) at pin 32.
    # Top edge radial direction: -y (upward). Pin pad y ~= 89.835.
    pad25 = p(u6, 25); pad31 = p(u6, 31); pad32 = p(u6, 32)
    top_inner_y = pad25[1] - INNER
    top_outer_y = pad25[1] - OUTER

    plan.append(("C43", "U6.25",
                 pad25[0], top_inner_y, 90,
                 "AVDD (pin 25) close-in decoupling, 100nF"))

    # Pin 31 (RESET) gets C53 (ESD 1nF) in inner row, R11 (10k pullup)
    # in outer row directly above it.
    plan.append(("C53", "U6.31",
                 pad31[0], top_inner_y, 90,
                 "RESET (pin 31) ESD cap, 1nF"))
    plan.append(("R11", "U6.31",
                 pad31[0], top_outer_y, 90,
                 "RESET (pin 31) 10k pullup to +3V3"))

    # Pin 32 (DVDD) gets C38 (100nF) inner and C39 (1uF) outer.
    plan.append(("C38", "U6.32",
                 pad32[0], top_inner_y, 90,
                 "DVDD (pin 32) close-in 100nF"))
    plan.append(("C39", "U6.32",
                 pad32[0], top_outer_y, 90,
                 "DVDD (pin 32) close-in bulk 1uF"))

    # ---------------- LDO (U7) ----------------
    # U7 pins: 1 GND (upper-left), 2 VOUT (lower-left), 3 VIN (right).
    # C36 (input, 100nF) to the right of pin 3.
    # C37 (output, 10uF) below pin 2.
    u7_pad3 = p(u7, 3)
    u7_pad2 = p(u7, 2)
    plan.append(("C36", "U7.3",
                 u7_pad3[0] + 1.5, u7_pad3[1], 0,
                 "LDO VIN (100nF) input decoupling"))
    plan.append(("C37", "U7.2",
                 u7_pad2[0], u7_pad2[1] + 1.5, 0,
                 "LDO VOUT (10uF) bulk + DVDD feed"))

    # ---------------- +3V3 BULK CAPS ----------------
    # Place above U6 in empty space (y < top_outer_y - 1.0 mm).
    # Stacked vertically to one side so they don't collide with the top row.
    u6_cx, u6_cy = u6["x"], u6["y"]
    # Place C44 and C45 to the top-left and top-right of U6, clear of the
    # top outer row (top_outer_y) by ~1.5 mm.
    bulk_y = top_outer_y - 2.0
    plan.append(("C44", "U6(center)",
                 u6_cx - 2.5, bulk_y, 0,
                 "+3V3 bulk 1uF near U6 top area"))
    plan.append(("C45", "U6(center)",
                 u6_cx + 2.5, bulk_y, 0,
                 "+3V3 bulk 10uF near U6 top area"))

    return plan


def distance_to_target(u6, u7, target_pin, tx, ty):
    """Compute distance from target position (tx, ty) to the pad at target_pin."""
    if target_pin.startswith("U6."):
        pad = u6["pads"].get(target_pin.split(".", 1)[1])
    elif target_pin.startswith("U7."):
        pad = u7["pads"].get(target_pin.split(".", 1)[1])
    else:
        return None
    if pad is None:
        return None
    return math.hypot(tx - pad[0], ty - pad[1])


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--json", type=pathlib.Path, default=None,
                    help="Write machine-readable target positions to this path")
    args = ap.parse_args()

    fps = load_pcb(PCB)
    u6 = fps.get("U6"); u7 = fps.get("U7")
    if u6 is None:
        print("ERROR: U6 not on PCB", file=sys.stderr); return 2
    if u7 is None:
        print("ERROR: U7 not on PCB", file=sys.stderr); return 2

    print(f"U6 center: ({u6['x']:.3f}, {u6['y']:.3f})  rot {u6['rot']:g}")
    print(f"U7 center: ({u7['x']:.3f}, {u7['y']:.3f})  rot {u7['rot']:g}")
    print(f"Inner offset: {INNER} mm    Outer offset: {OUTER} mm")
    print()

    plan = build_plan(u6, u7)

    print(f"{'ref':5} {'target':10} {'x':>8} {'y':>8} {'rot':>4} "
          f"{'dist':>9}  rationale")
    print("-" * 100)
    for ref, target, x, y, rot, note in plan:
        d = distance_to_target(u6, u7, target, x, y)
        d_s = f"{d:6.2f}mm" if d is not None else "      -"
        print(f"{ref:5} {target:10} {x:8.3f} {y:8.3f} {rot:4.0f} "
              f"{d_s:>9}  {note}")

    # Collision audit
    print()
    print("Body-overlap audit:")
    pts = [(ref, x, y, rot) for ref, _, x, y, rot, _ in plan]
    coll = 0
    for i in range(len(pts)):
        for j in range(i + 1, len(pts)):
            ri, xi, yi, roti = pts[i]
            rj, xj, yj, rotj = pts[j]
            hxi, hyi = body_halfsize(roti)
            hxj, hyj = body_halfsize(rotj)
            dx = abs(xi - xj) - (hxi + hxj)
            dy = abs(yi - yj) - (hyi + hyj)
            if dx < 0 and dy < 0:
                pen = max(-dx, -dy)
                print(f"  COLLISION: {ri} vs {rj}  body overlap {pen:.2f} mm")
                coll += 1
    if coll == 0:
        print("  no body overlaps ✓")

    # Clearance check against locked mechanical parts
    print()
    print("Clearance from locked mechanical parts (MH1/MH2/MH3, SW1):")
    tight = False
    for locked_ref in ["MH1", "MH2", "MH3", "SW1"]:
        locked = fps.get(locked_ref)
        if locked is None:
            continue
        lx, ly = locked["x"], locked["y"]
        for ref, _, x, y, _, _ in plan:
            d = math.hypot(x - lx, y - ly)
            if d < 3.0:
                print(f"  {ref} -> {locked_ref}: {d:.2f} mm (tight!)")
                tight = True
    if not tight:
        print("  all targets >=3 mm from locked parts ✓")

    # Board extent check
    print()
    print("Board-extent check:")
    xs = [x for _, _, x, _, _, _ in plan]
    ys = [y for _, _, _, y, _, _ in plan]
    print(f"  target bbox: x ∈ [{min(xs):.2f}, {max(xs):.2f}]  "
          f"y ∈ [{min(ys):.2f}, {max(ys):.2f}]")

    if args.json:
        out = {
            "cluster": "codec",
            "u6_ref": "U6",
            "u7_ref": "U7",
            "inner_offset_mm": INNER,
            "outer_offset_mm": OUTER,
            "collisions": coll,
            "targets": [
                {
                    "ref": ref,
                    "target_pin": target,
                    "x": x,
                    "y": y,
                    "rot": rot,
                    "distance_mm": distance_to_target(u6, u7, target, x, y),
                    "rationale": note,
                }
                for ref, target, x, y, rot, note in plan
            ],
        }
        args.json.write_text(json.dumps(out, indent=2))
        print(f"\nTargets JSON: {args.json}")

    return 0 if coll == 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
