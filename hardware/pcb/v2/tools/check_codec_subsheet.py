#!/usr/bin/env python3
"""Verify codec.kicad_sch satisfies the codec module contract.

Source of truth: hardware/pcb/v2/codec-module-spec.md §9 (verification invariants).
Datasheet pin table: SLAS510G §7 Table 7-1.

Runs kicad-cli to export a netlist from codec.kicad_sch, parses the XML, and
checks 11 invariants against the spec. Exit 0 = all pass, 1 = one or more fail.

Run before any hardware commit that touches codec.kicad_sch.
"""
from __future__ import annotations

import re
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET
from pathlib import Path
from typing import Iterable

REPO_ROOT = Path(__file__).resolve().parents[4]
SHEET_FILE = REPO_ROOT / "hardware/pcb/v2/kicad/codec.kicad_sch"

# TLV320AIC3104 IC reference inside the standalone subsheet.
# The parent schematic will re-annotate during Phase 3; this value is correct
# only for the standalone sheet file.
CODEC_REF = "U1"
LDO_REF = "U2"

# Expected sheet ports (exactly 13). MIC_RETURN is intentionally NOT on this
# list - see spec §3 and the reconciliation commit.
EXPECTED_PORTS = {
    "+3V3",
    "GND",
    "CODEC_BCLK",
    "CODEC_WCLK",
    "CODEC_DIN",
    "CODEC_DOUT",
    "CODEC_MCLK",
    "CODEC_SDA",
    "CODEC_SCL",
    "CODEC_RESET",
    "MIC_FROM_SW",
    "EAR_P",
    "EAR_N",
}

# Codec pin -> datasheet function (from SLAS510G §7 Table 7-1).
# Only the pins the invariants reference are listed.
POWER_PINS = {
    7: "IOVDD",
    18: "DRVDD",
    24: "DRVDD",
    25: "AVDD",
    32: "DVDD",
}
UNUSED_INPUT_PINS = [12, 13, 14, 16]  # MIC1RP, MIC1RM, MIC2L, MIC2R
MIC1LM_PIN = 11
MICBIAS_PIN = 15
HPLOUT_PIN = 19
HPLCOM_PIN = 20
EP_PIN = 33
DVDD_PIN = 32


class Check:
    def __init__(self, label: str):
        self.label = label
        self.ok: bool | None = None
        self.detail: str = ""

    def passed(self, detail: str = "") -> None:
        self.ok = True
        self.detail = detail

    def failed(self, detail: str = "") -> None:
        self.ok = False
        self.detail = detail

    def print(self) -> None:
        mark = "PASS" if self.ok else "FAIL"
        suffix = f": {self.detail}" if self.detail else ""
        print(f"  [{mark}] {self.label}{suffix}")


def run_check(checks: list[Check], label: str) -> Check:
    c = Check(label)
    checks.append(c)
    return c


def export_netlist(sch: Path) -> ET.Element:
    """Run kicad-cli to export the sheet as a kicadxml netlist."""
    with tempfile.NamedTemporaryFile(suffix=".xml", delete=False) as tmp:
        out = Path(tmp.name)
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
                str(sch),
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


def build_pin_net_map(root: ET.Element, ref: str) -> dict[int, str]:
    """Return {pin_number: net_name} for the given reference."""
    out: dict[int, str] = {}
    for net in root.findall(".//nets/net"):
        name = net.get("name") or ""
        for node in net.findall("node"):
            if node.get("ref") == ref:
                pin = node.get("pin")
                if pin is not None:
                    try:
                        out[int(pin)] = name
                    except ValueError:
                        pass
    return out


def build_net_node_map(root: ET.Element) -> dict[str, list[tuple[str, str]]]:
    """Return {net_name: [(ref, pin), ...]}."""
    out: dict[str, list[tuple[str, str]]] = {}
    for net in root.findall(".//nets/net"):
        name = net.get("name") or ""
        nodes: list[tuple[str, str]] = []
        for node in net.findall("node"):
            ref = node.get("ref") or ""
            pin = node.get("pin") or ""
            nodes.append((ref, pin))
        out[name] = nodes
    return out


def caps_on_net(nodes: Iterable[tuple[str, str]]) -> list[str]:
    """Return the cap refs on a net (reference designators starting with C)."""
    return sorted({ref for ref, _ in nodes if ref.startswith("C") and not ref.startswith("#")})


def u1_pins_on_net(nodes: Iterable[tuple[str, str]]) -> list[str]:
    """Return codec pins (as strings) present on a net."""
    return sorted([pin for ref, pin in nodes if ref == CODEC_REF])


def read_sheet_text() -> str:
    return SHEET_FILE.read_text()


def main() -> int:
    if not SHEET_FILE.exists():
        print(f"ERROR: {SHEET_FILE} not found")
        return 1

    sheet_text = read_sheet_text()
    root = export_netlist(SHEET_FILE)
    pin_net = build_pin_net_map(root, CODEC_REF)
    net_nodes = build_net_node_map(root)

    print("=" * 60)
    print("codec.kicad_sch module contract audit")
    print("=" * 60)

    if len(pin_net) != 33:
        print(
            f"ERROR: expected 33 pins on {CODEC_REF}, got {len(pin_net)}. "
            f"Sheet may not be parseable or U1 is not the codec."
        )
        return 1

    checks: list[Check] = []

    # 1. Exactly 13 sheet ports (hierarchical labels).
    hlabel_names = set(re.findall(r'\(hierarchical_label\s+"([^"]*)"', sheet_text))
    c = run_check(checks, "1. exactly 13 sheet ports")
    if hlabel_names == EXPECTED_PORTS:
        c.passed(f"{len(hlabel_names)} ports match spec")
    else:
        missing = EXPECTED_PORTS - hlabel_names
        extra = hlabel_names - EXPECTED_PORTS
        parts = []
        if missing:
            parts.append(f"missing={sorted(missing)}")
        if extra:
            parts.append(f"extra={sorted(extra)}")
        c.failed(f"found {len(hlabel_names)} ports; " + "; ".join(parts))

    # 2. +1V8 / DVDD rail does NOT cross the sheet boundary.
    c = run_check(checks, "2. +1V8/DVDD rail stays internal")
    forbidden_ports = {"+1V8", "DVDD", "VDD_1V8", "1V8"}
    leaked = hlabel_names & forbidden_ports
    if leaked:
        c.failed(f"internal rail(s) exposed as sheet ports: {sorted(leaked)}")
    else:
        c.passed()

    # 3. Every power pin has >=1 close-in decoupling cap on its net.
    for pin, pin_name in sorted(POWER_PINS.items()):
        c = run_check(checks, f"3.{pin:<2} power pin {pin} ({pin_name}) has >=1 cap")
        net = pin_net.get(pin)
        if net is None:
            c.failed("pin not in netlist")
            continue
        caps = caps_on_net(net_nodes.get(net, []))
        if caps:
            c.passed(f"net={net} caps={caps}")
        else:
            c.failed(f"net={net} caps=[]")

    # 4. DVDD driven by LDO U2 pin 2 (VO), not +3V3 directly.
    c = run_check(checks, "4. DVDD driven by LDO U2.2 (not +3V3 direct)")
    dvdd_net = pin_net.get(DVDD_PIN)
    if dvdd_net is None:
        c.failed("DVDD pin not in netlist")
    else:
        nodes = net_nodes.get(dvdd_net, [])
        has_ldo_out = (LDO_REF, "2") in nodes
        is_3v3 = dvdd_net.endswith("+3V3") or dvdd_net == "/+3V3"
        if has_ldo_out and not is_3v3:
            c.passed(f"net={dvdd_net} has {LDO_REF}.2")
        elif is_3v3:
            c.failed(f"DVDD is tied directly to +3V3 ({dvdd_net})")
        else:
            c.failed(f"net={dvdd_net} missing {LDO_REF}.2; nodes={nodes}")

    # 5. MICBIAS (pin 15) has a bypass cap.
    c = run_check(checks, "5. MICBIAS (pin 15) has >=1 bypass cap")
    mb_net = pin_net.get(MICBIAS_PIN)
    if mb_net is None:
        c.failed("MICBIAS pin not in netlist")
    else:
        caps = caps_on_net(net_nodes.get(mb_net, []))
        if caps:
            c.passed(f"net={mb_net} caps={caps}")
        else:
            c.failed(f"net={mb_net} caps=[]")

    # 6. Each unused analog input on its own dedicated net with exactly one cap.
    for pin in UNUSED_INPUT_PINS:
        c = run_check(checks, f"6.{pin:<2} unused input pin {pin} dedicated + single cap")
        net = pin_net.get(pin)
        if net is None:
            c.failed("pin not in netlist")
            continue
        nodes = net_nodes.get(net, [])
        u1_pins = u1_pins_on_net(nodes)
        caps = caps_on_net(nodes)
        if len(u1_pins) == 1 and len(caps) == 1:
            c.passed(f"net={net} cap={caps[0]}")
        else:
            c.failed(
                f"net={net} u1_pins={u1_pins} caps={caps} "
                f"(expected 1 U1 pin and 1 cap)"
            )

    # 7. MIC1LM (pin 11) is AC-coupled to AGND, NOT on the GND net.
    c = run_check(checks, "7. MIC1LM (pin 11) AC-coupled, not on GND")
    net = pin_net.get(MIC1LM_PIN)
    if net is None:
        c.failed("pin 11 not in netlist")
    elif net in {"GND", "/GND"}:
        c.failed(f"pin 11 is hard-grounded on {net}")
    else:
        caps = caps_on_net(net_nodes.get(net, []))
        if caps:
            c.passed(f"net={net} caps={caps}")
        else:
            c.failed(f"net={net} has no cap to AGND")

    # 8a. HPLOUT (pin 19) has no series caps to the EAR_P port.
    c = run_check(checks, "8a. HPLOUT (pin 19) -> EAR_P, no series caps")
    net = pin_net.get(HPLOUT_PIN)
    if net is None:
        c.failed("pin 19 not in netlist")
    else:
        caps = caps_on_net(net_nodes.get(net, []))
        if caps:
            c.failed(f"net={net} has unexpected caps={caps}")
        elif "EAR_P" not in net:
            c.failed(f"net={net} does not name EAR_P")
        else:
            c.passed(f"net={net}")

    # 8b. HPLCOM (pin 20) has no series caps to the EAR_N port.
    c = run_check(checks, "8b. HPLCOM (pin 20) -> EAR_N, no series caps")
    net = pin_net.get(HPLCOM_PIN)
    if net is None:
        c.failed("pin 20 not in netlist")
    else:
        caps = caps_on_net(net_nodes.get(net, []))
        if caps:
            c.failed(f"net={net} has unexpected caps={caps}")
        elif "EAR_N" not in net:
            c.failed(f"net={net} does not name EAR_N")
        else:
            c.passed(f"net={net}")

    # 9. Exactly 6 no_connect flags (HPROUT, HPRCOM, LEFT_LO[PM], RIGHT_LO[PM]).
    c = run_check(checks, "9. exactly 6 no_connect flags")
    nc_count = sheet_text.count("(no_connect ")
    if nc_count == 6:
        c.passed()
    else:
        c.failed(f"found {nc_count}, expected 6")

    # 10. Exposed pad (pin 33) on GND.
    c = run_check(checks, "10. EP (pin 33) on GND")
    ep_net = pin_net.get(EP_PIN)
    if ep_net in {"GND", "/GND"}:
        c.passed(f"net={ep_net}")
    else:
        c.failed(f"pin 33 on net={ep_net}, expected GND")

    # 11. check_codec_symbol.py still passes (symbol library has not regressed).
    c = run_check(checks, "11. check_codec_symbol.py still passes")
    r = subprocess.run(
        [sys.executable, str(Path(__file__).parent / "check_codec_symbol.py")],
        capture_output=True,
        text=True,
    )
    if r.returncode == 0:
        c.passed()
    else:
        c.failed(f"exit {r.returncode}")

    for check in checks:
        check.print()

    print()
    total = len(checks)
    fails = sum(1 for c in checks if not c.ok)
    passed = total - fails
    print(f"Result: {passed}/{total} invariants satisfied")
    if fails:
        print(f"FAIL: {fails} invariant(s) violated")
        return 1
    print(f"PASS: all {total} invariants satisfied")
    return 0


if __name__ == "__main__":
    sys.exit(main())
