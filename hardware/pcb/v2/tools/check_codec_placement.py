#!/usr/bin/env python3
"""Verify codec cluster placements against codec_targets.json.

Thin wrapper around check_decoupling.py that passes the codec targets
file by default. Run this as often as you like while manually placing
codec cluster components in pcbnew - it re-reads the .kicad_pcb each
time and prints a per-component PASS/FAIL table showing current distance
vs. the allowed maximum.

Usage:
    python3 hardware/pcb/v2/tools/check_codec_placement.py
    python3 hardware/pcb/v2/tools/check_codec_placement.py --quiet  # only FAILs
"""
from __future__ import annotations

import pathlib
import sys

import check_decoupling

REPO_ROOT = pathlib.Path(__file__).resolve().parents[4]
CODEC_TARGETS = REPO_ROOT / "hardware/pcb/v2/codec_targets.json"


def main() -> int:
    # Inject codec_targets.json as the default target file, but allow the
    # user to still override via --targets on the command line.
    if "--targets" not in sys.argv:
        sys.argv.extend(["--targets", str(CODEC_TARGETS)])
    return check_decoupling.main()


if __name__ == "__main__":
    raise SystemExit(main())
