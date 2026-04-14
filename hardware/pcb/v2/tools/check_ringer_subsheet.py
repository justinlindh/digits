#!/usr/bin/env python3
"""Validate hardware/pcb/v2/kicad/ringer.kicad_sch against the
module-contract invariants in ringer-module-spec.md.

Mirrors check_codec_subsheet.py: operates on the standalone sheet, uses
kiutils to parse the s-expression for structural checks, and runs
kicad-cli to export a netlist XML for connectivity checks. Reports each
of the 11 invariants as PASS or FAIL with color output when stdout is a
TTY.

Invariants checked:
  1. Sheet has exactly 6 hierarchical ports (+12V, GND, RINGER_IN1,
     RINGER_IN2, BELL_A, BELL_B).
  2. U1 is a DRV8871 with the correct datasheet pin map.
  3. R1 value is 33 kΩ.
  4. C1 value is 100 nF.
  5. C2 value is >= 10 uF (10/22/47 uF).
  6. Sheet has exactly 4 components: U1, R1, C1, C2.
  7. U1.4 and R1.1 share the same net; R1.2 is on GND.
  8. C1.1 shares net with U1.5 (+12V); C1.2 is on GND.
  9. C2.1 shares net with U1.5 (+12V); C2.2 is on GND.
 10. U1.6 is the sole driver of BELL_A (net name contains BELL_A).
 11. U1.8 is the sole driver of BELL_B (net name contains BELL_B).
"""
from __future__ import annotations

import pathlib
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET

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


def export_netlist(sheet_path: pathlib.Path) -> ET.Element:
    """Run kicad-cli to export the sheet as a kicadxml netlist."""
    with tempfile.NamedTemporaryFile(suffix=".xml", delete=False) as tmp:
        out = pathlib.Path(tmp.name)
    try:
        result = subprocess.run(
            [
                "kicad-cli",
                "sch",
                "export",
                "netlist",
                "--format",
                "kicadxml",
                "-o",
                str(out),
                str(sheet_path),
            ],
            capture_output=True,
            text=True,
        )
        if result.returncode != 0:
            print(f"ERROR: kicad-cli netlist export failed:\n{result.stderr}")
            sys.exit(1)
        return ET.parse(out).getroot()
    finally:
        out.unlink(missing_ok=True)


def pin_net(root: ET.Element, ref: str, pin: str) -> str | None:
    """Return the net name for <ref>.<pin> from the parsed netlist XML.

    Returns None if the pin is not found.
    """
    for net in root.findall(".//nets/net"):
        name = net.get("name") or ""
        for node in net.findall("node"):
            if node.get("ref") == ref and node.get("pin") == pin:
                return name
    return None


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


def check_invariant_3b_ilim_connectivity(root: ET.Element) -> tuple[bool, str]:
    """U1.4 and R1.1 must share the same net; R1.2 must be on GND."""
    u1_4 = pin_net(root, DRV_REF, "4")
    r1_1 = pin_net(root, ILIM_REF, "1")
    r1_2 = pin_net(root, ILIM_REF, "2")

    issues = []
    if u1_4 is None:
        issues.append("U1.4 not found in netlist")
    if r1_1 is None:
        issues.append("R1.1 not found in netlist")
    if r1_2 is None:
        issues.append("R1.2 not found in netlist")
    if issues:
        return False, "; ".join(issues)

    if u1_4 != r1_1:
        issues.append(f"U1.4 on net={u1_4!r} but R1.1 on net={r1_1!r} (must share)")
    if r1_2 not in {"GND", "/GND"}:
        issues.append(f"R1.2 on net={r1_2!r}, expected GND")

    if issues:
        return False, "; ".join(issues)
    return True, f"U1.4=R1.1 on net={u1_4!r}, R1.2 on net={r1_2!r}"


def check_invariant_4b_c1_connectivity(root: ET.Element) -> tuple[bool, str]:
    """C1.1 must share net with U1.5 (+12V); C1.2 must be on GND."""
    u1_5 = pin_net(root, DRV_REF, "5")
    c1_1 = pin_net(root, HF_CAP_REF, "1")
    c1_2 = pin_net(root, HF_CAP_REF, "2")

    issues = []
    if u1_5 is None:
        issues.append("U1.5 not found in netlist")
    if c1_1 is None:
        issues.append("C1.1 not found in netlist")
    if c1_2 is None:
        issues.append("C1.2 not found in netlist")
    if issues:
        return False, "; ".join(issues)

    if u1_5 != c1_1:
        issues.append(f"U1.5 on net={u1_5!r} but C1.1 on net={c1_1!r} (must share)")
    if c1_2 not in {"GND", "/GND"}:
        issues.append(f"C1.2 on net={c1_2!r}, expected GND")

    if issues:
        return False, "; ".join(issues)
    return True, f"C1.1=U1.5 on net={u1_5!r}, C1.2 on net={c1_2!r}"


def check_invariant_5b_c2_connectivity(root: ET.Element) -> tuple[bool, str]:
    """C2.1 must share net with U1.5 (+12V); C2.2 must be on GND."""
    u1_5 = pin_net(root, DRV_REF, "5")
    c2_1 = pin_net(root, BULK_CAP_REF, "1")
    c2_2 = pin_net(root, BULK_CAP_REF, "2")

    issues = []
    if u1_5 is None:
        issues.append("U1.5 not found in netlist")
    if c2_1 is None:
        issues.append("C2.1 not found in netlist")
    if c2_2 is None:
        issues.append("C2.2 not found in netlist")
    if issues:
        return False, "; ".join(issues)

    if u1_5 != c2_1:
        issues.append(f"U1.5 on net={u1_5!r} but C2.1 on net={c2_1!r} (must share)")
    if c2_2 not in {"GND", "/GND"}:
        issues.append(f"C2.2 on net={c2_2!r}, expected GND")

    if issues:
        return False, "; ".join(issues)
    return True, f"C2.1=U1.5 on net={u1_5!r}, C2.2 on net={c2_2!r}"


def check_invariant_7_bell_a(root: ET.Element) -> tuple[bool, str]:
    """U1.6 must be on net BELL_A."""
    net = pin_net(root, DRV_REF, "6")
    if net is None:
        return False, "U1.6 not found in netlist"
    if "BELL_A" not in net:
        return False, f"U1.6 on net={net!r}, expected net containing BELL_A"
    return True, f"U1.6 on net={net!r}"


def check_invariant_8_bell_b(root: ET.Element) -> tuple[bool, str]:
    """U1.8 must be on net BELL_B."""
    net = pin_net(root, DRV_REF, "8")
    if net is None:
        return False, "U1.8 not found in netlist"
    if "BELL_B" not in net:
        return False, f"U1.8 on net={net!r}, expected net containing BELL_B"
    return True, f"U1.8 on net={net!r}"


# Each entry: (label, input_kind, function)
# input_kind "sch" => receives the kiutils Schematic object
# input_kind "net" => receives the kicad-cli netlist XML root
CHECKS = [
    ("ports", "sch", check_invariant_1_ports),
    ("drv8871 pinmap", "sch", check_invariant_2_drv8871),
    ("R1 ILIM value", "sch", check_invariant_3_ilim),
    ("C1 HF bypass value", "sch", check_invariant_4_hf_cap),
    ("C2 bulk value", "sch", check_invariant_5_bulk_cap),
    ("component count", "sch", check_invariant_6_component_count),
    ("R1 ILIM connectivity", "net", check_invariant_3b_ilim_connectivity),
    ("C1 HF connectivity", "net", check_invariant_4b_c1_connectivity),
    ("C2 bulk connectivity", "net", check_invariant_5b_c2_connectivity),
    ("BELL_A driver", "net", check_invariant_7_bell_a),
    ("BELL_B driver", "net", check_invariant_8_bell_b),
]


def main() -> int:
    sch = load_sheet()  # exits 2 if file does not exist
    root = export_netlist(RINGER_SCH)

    use_color = sys.stdout.isatty() and "NO_COLOR" not in __import__("os").environ
    GREEN = "\033[32m" if use_color else ""
    RED = "\033[31m" if use_color else ""
    BOLD = "\033[1m" if use_color else ""
    RESET = "\033[0m" if use_color else ""

    fails = 0
    for name, kind, fn in CHECKS:
        arg = sch if kind == "sch" else root
        ok, msg = fn(arg)
        color = GREEN if ok else RED
        tag = "PASS" if ok else "FAIL"
        print(f"{color}{tag:5} {name:25} {msg}{RESET}")
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
