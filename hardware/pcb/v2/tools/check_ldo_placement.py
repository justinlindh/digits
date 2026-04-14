#!/usr/bin/env python3
"""Verify AMS1117-3.3 LDO (U5) cluster placement against ldo_targets.json.

Thin wrapper around check_decoupling.py.
"""
from __future__ import annotations

import pathlib
import sys

import check_decoupling

REPO_ROOT = pathlib.Path(__file__).resolve().parents[4]
LDO_TARGETS = REPO_ROOT / "hardware/pcb/v2/ldo_targets.json"


def main() -> int:
    if "--targets" not in sys.argv:
        sys.argv.extend(["--targets", str(LDO_TARGETS)])
    return check_decoupling.main()


if __name__ == "__main__":
    raise SystemExit(main())
