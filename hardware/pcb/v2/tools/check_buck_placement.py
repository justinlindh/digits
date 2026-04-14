#!/usr/bin/env python3
"""Verify LM2596 buck converter placement against buck_targets.json.

Thin wrapper around check_decoupling.py. Run this whenever you touch the
U1/L1/D1/C1/C2/C4 cluster.

Usage:
    python3 hardware/pcb/v2/tools/check_buck_placement.py
    python3 hardware/pcb/v2/tools/check_buck_placement.py --quiet
"""
from __future__ import annotations

import pathlib
import sys

import check_decoupling

REPO_ROOT = pathlib.Path(__file__).resolve().parents[4]
BUCK_TARGETS = REPO_ROOT / "hardware/pcb/v2/buck_targets.json"


def main() -> int:
    if "--targets" not in sys.argv:
        sys.argv.extend(["--targets", str(BUCK_TARGETS)])
    return check_decoupling.main()


if __name__ == "__main__":
    raise SystemExit(main())
