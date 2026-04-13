#!/usr/bin/env bash
# deploy.sh — Build and flash Digits firmware in one step
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

"$SCRIPT_DIR/build.sh" && picotool load -f "$SCRIPT_DIR/../firmware/build/local/digits.uf2"
