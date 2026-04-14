#!/usr/bin/env python3
"""Verify 12MHz crystal placement against crystal_targets.json.

Thin wrapper around check_decoupling.py.

Usage:
    python3 hardware/pcb/v2/tools/check_crystal_placement.py
"""
from __future__ import annotations

import pathlib
import sys

import check_decoupling

REPO_ROOT = pathlib.Path(__file__).resolve().parents[4]
CRYSTAL_TARGETS = REPO_ROOT / "hardware/pcb/v2/crystal_targets.json"


def main() -> int:
    if "--targets" not in sys.argv:
        sys.argv.extend(["--targets", str(CRYSTAL_TARGETS)])
    return check_decoupling.main()


if __name__ == "__main__":
    raise SystemExit(main())
