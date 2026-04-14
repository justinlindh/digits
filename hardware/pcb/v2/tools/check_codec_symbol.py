#!/usr/bin/env python3
"""Verify TLV320AIC3104IRHBR symbol pin mapping against SLAS510G §7 Table 7-1.

Run before any hardware commit that touches digits-pcb.kicad_sym.
Exit 0 = all pins match datasheet. Exit 1 = one or more mismatches.
"""
import re
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[4]
SYM_FILE = REPO_ROOT / "hardware/pcb/v2/kicad/digits-pcb.kicad_sym"
SYMBOL_NAME = "TLV320AIC3104IRHBR"

# Pin number -> canonical datasheet name from SLAS510G §7 Table 7-1
DATASHEET_PINS = {
    1:  "MCLK",
    2:  "BCLK",
    3:  "WCLK",
    4:  "DIN",
    5:  "DOUT",
    6:  "DVSS",
    7:  "IOVDD",
    8:  "SCL",
    9:  "SDA",
    10: "MIC1LP",
    11: "MIC1LM",
    12: "MIC1RP",
    13: "MIC1RM",
    14: "MIC2L",
    15: "MICBIAS",
    16: "MIC2R",
    17: "AVSS1",
    18: "DRVDD",
    19: "HPLOUT",
    20: "HPLCOM",
    21: "DRVSS",
    22: "HPRCOM",
    23: "HPROUT",
    24: "DRVDD",
    25: "AVDD",
    26: "AVSS2",
    27: "LEFT_LOP",
    28: "LEFT_LOM",
    29: "RIGHT_LOP",
    30: "RIGHT_LOM",
    31: "RESET",
    32: "DVDD",
    33: "DRVSS",  # EP thermal pad
}


def strip_overbar(name: str) -> str:
    """Strip KiCad overbar notation ~{...} -> inner text."""
    m = re.match(r'^~\{(.*)\}$', name)
    return m.group(1) if m else name


def base_name(name: str) -> str:
    """Normalise a pin name for comparison.

    Rules:
    - Strip overbar notation.
    - Strip trailing _N disambiguation suffix (e.g. DRVDD_1 -> DRVDD).
    - Take only the part before the first '/' (alternate function separator).
    """
    name = strip_overbar(name)
    name = name.split('/')[0]
    name = re.sub(r'_\d+$', '', name)
    return name


def name_matches(actual: str, expected: str) -> bool:
    """Return True if actual pin name is an acceptable match for expected."""
    # Pin 33 thermal pad: accept loose naming
    if expected == "DRVSS":
        return base_name(actual) in {"DRVSS", "GND", "EP", "AGND"}
    return base_name(actual) == base_name(expected)


def extract_symbol_block(data: str, sym_name: str) -> str:
    """Extract the full s-expression block for sym_name using a depth counter.

    Handles quoted strings containing parentheses correctly.
    """
    pattern = re.compile(r'\(symbol\s+"' + re.escape(sym_name) + r'"')
    m = pattern.search(data)
    if not m:
        raise ValueError(f"Symbol {sym_name!r} not found in library")
    start = m.start()
    depth = 0
    i = start
    in_string = False
    while i < len(data):
        c = data[i]
        if c == '"' and (i == 0 or data[i - 1] != '\\'):
            in_string = not in_string
        if not in_string:
            if c == '(':
                depth += 1
            elif c == ')':
                depth -= 1
                if depth == 0:
                    break
        i += 1
    return data[start:i + 1]


def extract_pins(block: str) -> dict[int, str]:
    """Return {pin_number: pin_name} from a symbol s-expression block."""
    pins: dict[int, str] = {}
    # Each pin: (pin type style (at ...) (length N) ... (name "X" ...) (number "N" ...))
    pattern = re.compile(
        r'\(pin\s+\w+\s+\w+\s+'          # (pin type style
        r'\(at[^)]*\)\s+'                  # (at ...)
        r'\(length\s+[\d.]+\)'             # (length N)
        r'(?:[^(]|\([^(]*\))*?'           # optional mid-content (e.g. hide)
        r'\(name\s+"([^"]*)"[^)]*\)'      # (name "X" ...)
        r'(?:[^(]|\([^(]*\))*?'           # middle content
        r'\(number\s+"(\d+)"[^)]*\)',     # (number "N" ...)
        re.DOTALL,
    )
    for m in pattern.finditer(block):
        name, number = m.group(1), int(m.group(2))
        pins[number] = name
    return pins


def main() -> int:
    data = SYM_FILE.read_text()

    try:
        block = extract_symbol_block(data, SYMBOL_NAME)
    except ValueError as e:
        print(f"ERROR: {e}")
        return 1

    pins = extract_pins(block)

    print("=" * 60)
    print(f"TLV320AIC3104IRHBR symbol pin audit - SLAS510G Table 7-1")
    print("=" * 60)

    if len(pins) == 0:
        print("ERROR: no pins extracted - regex may need updating")
        return 1

    fails = 0
    for pin_num in sorted(DATASHEET_PINS):
        expected = DATASHEET_PINS[pin_num]
        actual = pins.get(pin_num, "<missing>")
        ok = name_matches(actual, expected)
        mark = "PASS" if ok else "FAIL"
        if not ok:
            fails += 1
        print(f"  [{mark}] pin {pin_num:2d}: expected {expected:<12} got {actual}")

    print()
    total = len(DATASHEET_PINS)
    passed = total - fails
    print(f"Result: {passed}/{total} pins match datasheet SLAS510G Table 7-1")
    if fails:
        print(f"FAIL: {fails} pin(s) do not match")
        return 1
    print(f"PASS: {passed}/{total} pins match datasheet SLAS510G Table 7-1")
    return 0


if __name__ == "__main__":
    sys.exit(main())
