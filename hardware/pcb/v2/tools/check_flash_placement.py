#!/usr/bin/env python3
"""Verify W25Q16 QSPI flash placement against flash_targets.json.

Thin wrapper around check_decoupling.py.

Usage:
    python3 hardware/pcb/v2/tools/check_flash_placement.py
"""
from __future__ import annotations

import pathlib
import sys

import check_decoupling

REPO_ROOT = pathlib.Path(__file__).resolve().parents[4]
FLASH_TARGETS = REPO_ROOT / "hardware/pcb/v2/flash_targets.json"


def main() -> int:
    if "--targets" not in sys.argv:
        sys.argv.extend(["--targets", str(FLASH_TARGETS)])
    return check_decoupling.main()


if __name__ == "__main__":
    raise SystemExit(main())
