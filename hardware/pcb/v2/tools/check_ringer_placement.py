#!/usr/bin/env python3
"""Verify DRV8871 cluster placement against ringer_targets.json.

Thin wrapper around check_decoupling.py.
"""
from __future__ import annotations

import pathlib
import sys

import check_decoupling

REPO_ROOT = pathlib.Path(__file__).resolve().parents[4]
RINGER_TARGETS = REPO_ROOT / "hardware/pcb/v2/ringer_targets.json"


def main() -> int:
    if "--targets" not in sys.argv:
        sys.argv.extend(["--targets", str(RINGER_TARGETS)])
    return check_decoupling.main()


if __name__ == "__main__":
    raise SystemExit(main())
