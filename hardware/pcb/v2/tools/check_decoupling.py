#!/usr/bin/env python3
"""Validate that decoupling caps are placed within range of their target IC pads.

KiCad has no native DRC rule for "component must be within N mm of a pad", so
this script enforces the constraint externally. Run it against the .kicad_pcb
after any placement change in the RP2040 cluster. It exits non-zero if any
decoupling cap is missing, misplaced, or drifts beyond its allowed radius.

Source of truth: hardware/pcb/v2/decoupling_targets.json
Usage:
    python3 hardware/pcb/v2/tools/check_decoupling.py \\
        [--pcb hardware/pcb/v2/kicad/digits-pcb.kicad_pcb] \\
        [--targets hardware/pcb/v2/decoupling_targets.json]
"""
from __future__ import annotations

import argparse
import json
import math
import pathlib
import sys

REPO_ROOT = pathlib.Path(__file__).resolve().parents[4]
DEFAULT_PCB = REPO_ROOT / "hardware/pcb/v2/kicad/digits-pcb.kicad_pcb"
DEFAULT_TARGETS = REPO_ROOT / "hardware/pcb/v2/decoupling_targets.json"


# ---------- minimal s-expression parser ----------

def tokenize(text: str):
    i, n = 0, len(text)
    while i < n:
        c = text[i]
        if c.isspace():
            i += 1
            continue
        if c == "(":
            yield "("; i += 1
        elif c == ")":
            yield ")"; i += 1
        elif c == '"':
            j = i + 1
            out = []
            while j < n:
                cj = text[j]
                if cj == "\\" and j + 1 < n:
                    out.append(text[j + 1]); j += 2
                elif cj == '"':
                    j += 1
                    break
                else:
                    out.append(cj); j += 1
            yield ('"', "".join(out))
            i = j
        else:
            j = i
            while j < n and not text[j].isspace() and text[j] not in "()":
                j += 1
            yield ("a", text[i:j])
            i = j


def parse(text: str):
    tokens = list(tokenize(text))
    idx = [0]

    def read():
        tok = tokens[idx[0]]
        idx[0] += 1
        if tok == "(":
            out = []
            while tokens[idx[0]] != ")":
                out.append(read())
            idx[0] += 1
            return out
        if tok == ")":
            raise ValueError("unexpected )")
        if isinstance(tok, tuple):
            kind, val = tok
            return val
        return tok

    return read()


def is_list(node):
    return isinstance(node, list)


def head(node):
    if is_list(node) and node:
        return node[0]
    return None


def find_all(node, name):
    if not is_list(node):
        return
    for child in node:
        if is_list(child) and head(child) == name:
            yield child


def find_first(node, name):
    for c in find_all(node, name):
        return c
    return None


def get_at(node):
    """Return (x, y, rot) from a (at x y [rot]) form, rot defaults to 0."""
    at = find_first(node, "at")
    if at is None:
        return None
    x = float(at[1])
    y = float(at[2])
    rot = float(at[3]) if len(at) > 3 else 0.0
    return x, y, rot


def footprint_reference(fp):
    """Extract reference designator from a footprint node."""
    for prop in find_all(fp, "property"):
        if len(prop) >= 3 and prop[1] == "Reference":
            return prop[2]
    return None


def rotate(px, py, deg):
    r = math.radians(deg)
    c, s = math.cos(r), math.sin(r)
    return (px * c - py * s, px * s + py * c)


# ---------- PCB data extraction ----------

def load_pcb(path: pathlib.Path):
    text = path.read_text()
    tree = parse(text)
    footprints = {}  # ref -> {"x","y","rot","pads":{num:(abs_x,abs_y)}}
    for fp in find_all(tree, "footprint"):
        at = get_at(fp)
        if at is None:
            continue
        fx, fy, frot = at
        ref = footprint_reference(fp)
        if ref is None:
            continue
        pads = {}
        for pad in find_all(fp, "pad"):
            if len(pad) < 2:
                continue
            num = pad[1]
            pat = get_at(pad)
            if pat is None:
                continue
            px, py, _prot = pat
            # pad offset is relative to footprint origin, rotated by footprint rotation
            rx, ry = rotate(px, py, frot)
            pads[str(num)] = (fx + rx, fy + ry)
        footprints[ref] = {"x": fx, "y": fy, "rot": frot, "pads": pads}
    return footprints


# ---------- check ----------

def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--pcb", type=pathlib.Path, default=DEFAULT_PCB)
    ap.add_argument("--targets", type=pathlib.Path, default=DEFAULT_TARGETS)
    ap.add_argument("--quiet", action="store_true", help="Only print failures")
    args = ap.parse_args()

    if not args.pcb.exists():
        print(f"ERROR: PCB not found: {args.pcb}", file=sys.stderr)
        return 2
    if not args.targets.exists():
        print(f"ERROR: targets not found: {args.targets}", file=sys.stderr)
        return 2

    targets_doc = json.loads(args.targets.read_text())
    default_max = float(targets_doc.get("default_max_distance_mm", 2.5))
    constraints = targets_doc["constraints"]

    fps = load_pcb(args.pcb)

    fails = 0
    rows = []

    for c in constraints:
        cap_ref = c["cap"]
        target = c["target"]
        max_d = float(c.get("max_distance_mm", default_max))
        ic_ref, pin = target.split(".")

        cap = fps.get(cap_ref)
        ic = fps.get(ic_ref)

        if cap is None:
            rows.append(("FAIL", cap_ref, target, None, max_d, f"cap {cap_ref} not on PCB"))
            fails += 1
            continue
        if ic is None:
            rows.append(("FAIL", cap_ref, target, None, max_d, f"IC {ic_ref} not on PCB"))
            fails += 1
            continue
        pad = ic["pads"].get(pin)
        if pad is None:
            rows.append(("FAIL", cap_ref, target, None, max_d, f"pad {target} not in footprint"))
            fails += 1
            continue

        dx = cap["x"] - pad[0]
        dy = cap["y"] - pad[1]
        dist = math.hypot(dx, dy)
        status = "PASS" if dist <= max_d else "FAIL"
        rows.append((status, cap_ref, target, dist, max_d, c.get("rationale", "")))
        if status == "FAIL":
            fails += 1

    if not args.quiet:
        print(f"{'res':5} {'cap':6} {'target':10} {'dist':>9} {'max':>6}  rationale")
        print("-" * 80)
    for status, cap_ref, target, dist, max_d, note in rows:
        if args.quiet and status == "PASS":
            continue
        dist_s = f"{dist:6.2f}mm" if dist is not None else "    n/a"
        print(f"{status:5} {cap_ref:6} {target:10} {dist_s:>9} {max_d:5.2f}  {note}")

    print()
    if fails:
        print(f"FAIL: {fails} of {len(constraints)} decoupling constraints violated.")
        return 1
    print(f"OK: all {len(constraints)} decoupling constraints satisfied.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
