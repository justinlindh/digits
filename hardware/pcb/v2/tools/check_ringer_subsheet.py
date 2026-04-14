#!/usr/bin/env python3
"""Validate hardware/pcb/v2/kicad/ringer.kicad_sch against the
module-contract invariants in ringer-module-spec.md.

Mirrors check_codec_subsheet.py: operates on the standalone sheet, uses
kiutils to parse the s-expression, and reports each invariant as PASS
or FAIL with color output when stdout is a TTY.
"""
from __future__ import annotations

import pathlib
import sys

from kiutils.schematic import Schematic

REPO_ROOT = pathlib.Path(__file__).resolve().parents[4]
RINGER_SCH = REPO_ROOT / "hardware/pcb/v2/kicad/ringer.kicad_sch"

EXPECTED_PORTS = {
    "+12V", "GND", "RINGER_IN1", "RINGER_IN2", "BELL_A", "BELL_B"
}

DRV_REF = "U1"
ILIM_REF = "R1"
HF_CAP_REF = "C1"
BULK_CAP_REF = "C2"


def load_sheet():
    if not RINGER_SCH.exists():
        print(f"ERROR: {RINGER_SCH} does not exist", file=sys.stderr)
        sys.exit(2)
    return Schematic().from_file(str(RINGER_SCH))


def find_symbol(sch, ref: str):
    for s in sch.schematicSymbols:
        for prop in s.properties:
            if prop.key == "Reference" and prop.value == ref:
                return s
    return None


def check_invariant_1_ports(sch) -> tuple[bool, str]:
    """Sheet has exactly 6 hierarchical ports with the expected names."""
    labels = {h.text for h in sch.hierarchicalLabels}
    if labels == EXPECTED_PORTS:
        return True, f"6 ports match: {sorted(labels)}"
    missing = EXPECTED_PORTS - labels
    extra = labels - EXPECTED_PORTS
    msg = []
    if missing:
        msg.append(f"missing {sorted(missing)}")
    if extra:
        msg.append(f"extra {sorted(extra)}")
    return False, "; ".join(msg)


def check_invariant_2_drv8871(sch) -> tuple[bool, str]:
    """U1 must be a DRV8871-shaped symbol with the datasheet pin map."""
    u1 = find_symbol(sch, DRV_REF)
    if u1 is None:
        return False, f"{DRV_REF} not found"
    value = next((p.value for p in u1.properties if p.key == "Value"), None)
    if value is None or "DRV8871" not in value.upper():
        return False, f"U1 value is {value!r}, expected DRV8871*"
    return True, f"{DRV_REF} value={value}"


def check_invariant_3_ilim(sch) -> tuple[bool, str]:
    """R1 exists, value is 33k."""
    r1 = find_symbol(sch, ILIM_REF)
    if r1 is None:
        return False, f"{ILIM_REF} not found"
    value = next((p.value for p in r1.properties if p.key == "Value"), None)
    vnorm = (value or "").replace(" ", "").lower()
    if not vnorm.startswith("33k"):
        return False, f"R1 value is {value!r}, expected 33k*"
    return True, f"{ILIM_REF} value={value}"


def check_invariant_4_hf_cap(sch) -> tuple[bool, str]:
    """C1 exists, value 100nF."""
    c1 = find_symbol(sch, HF_CAP_REF)
    if c1 is None:
        return False, f"{HF_CAP_REF} not found"
    value = next((p.value for p in c1.properties if p.key == "Value"), None)
    vnorm = (value or "").replace(" ", "").lower()
    if vnorm not in ("100nf", "0.1uf", "0.1u", "100n"):
        return False, f"C1 value is {value!r}, expected 100nF"
    return True, f"{HF_CAP_REF} value={value}"


def check_invariant_5_bulk_cap(sch) -> tuple[bool, str]:
    """C2 exists, value >= 10uF (10/22/47uF)."""
    c2 = find_symbol(sch, BULK_CAP_REF)
    if c2 is None:
        return False, f"{BULK_CAP_REF} not found"
    value = next((p.value for p in c2.properties if p.key == "Value"), None)
    if value is None:
        return False, "C2 has no Value"
    vnorm = value.replace(" ", "").lower()
    ok = any(vnorm.startswith(v) for v in ("10uf", "22uf", "47uf"))
    if not ok:
        return False, f"C2 value is {value!r}, expected >=10uF (10/22/47uF)"
    return True, f"{BULK_CAP_REF} value={value}"


def check_invariant_6_component_count(sch) -> tuple[bool, str]:
    """Exactly 4 components: U1, R1, C1, C2."""
    refs = set()
    for s in sch.schematicSymbols:
        for prop in s.properties:
            if prop.key == "Reference":
                refs.add(prop.value)
    expected = {DRV_REF, ILIM_REF, HF_CAP_REF, BULK_CAP_REF}
    extras = refs - expected
    missing = expected - refs
    if not extras and not missing:
        return True, f"4 components exactly: {sorted(refs)}"
    msg = []
    if missing:
        msg.append(f"missing {sorted(missing)}")
    if extras:
        msg.append(f"extra {sorted(extras)}")
    return False, "; ".join(msg)


CHECKS = [
    ("ports", check_invariant_1_ports),
    ("drv8871 pinmap", check_invariant_2_drv8871),
    ("R1 ILIM", check_invariant_3_ilim),
    ("C1 HF bypass", check_invariant_4_hf_cap),
    ("C2 bulk", check_invariant_5_bulk_cap),
    ("component count", check_invariant_6_component_count),
]


def main() -> int:
    sch = load_sheet()
    use_color = sys.stdout.isatty() and "NO_COLOR" not in __import__("os").environ
    GREEN = "\033[32m" if use_color else ""
    RED = "\033[31m" if use_color else ""
    BOLD = "\033[1m" if use_color else ""
    RESET = "\033[0m" if use_color else ""

    fails = 0
    for name, fn in CHECKS:
        ok, msg = fn(sch)
        color = GREEN if ok else RED
        tag = "PASS" if ok else "FAIL"
        print(f"{color}{tag:5} {name:20} {msg}{RESET}")
        if not ok:
            fails += 1

    print()
    if fails:
        print(f"{BOLD}{RED}FAIL: {fails} of {len(CHECKS)} ringer invariants violated.{RESET}")
        return 1
    print(f"{BOLD}{GREEN}OK: all {len(CHECKS)} ringer invariants satisfied.{RESET}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
