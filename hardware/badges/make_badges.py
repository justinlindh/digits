"""Generate STL files for the keypad badges.

Source of truth for badge geometry. Re-run after editing constants:

    python3 make_badges.py

Requires build123d (pip install build123d). The Fraunces TTF for the
lockup variant is decompressed from the webapp's woff2 at runtime.
"""

from __future__ import annotations

import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

from build123d import (
    Align,
    Circle,
    Compound,
    Location,
    Part,
    Pos,
    Rectangle,
    RectangleRounded,
    Sketch,
    Text,
    export_stl,
    extrude,
)

HERE = Path(__file__).resolve().parent
REPO = HERE.parent.parent
WOFF2 = REPO / "server/internal/web/static/fonts/fraunces-variable.woff2"

BADGE_THICKNESS = 1.6
EMBOSS_THICKNESS = 0.4

BUTTON_SIZE = 4.0
BUTTON_RADIUS = 1.0
BUTTON_GAP = 1.0
KEYPAD_W = 3 * BUTTON_SIZE + 2 * BUTTON_GAP  # 14
KEYPAD_H = 4 * BUTTON_SIZE + 3 * BUTTON_GAP  # 19


def keypad_sketch(cx: float = 0.0, cy: float = 0.0) -> Sketch:
    """3x4 grid of rounded squares, the keypad's bounding-box center at (cx, cy)."""
    sketch = Sketch()
    for col in range(3):
        for row in range(4):
            x = cx - KEYPAD_W / 2 + BUTTON_SIZE / 2 + col * (BUTTON_SIZE + BUTTON_GAP)
            y = cy + KEYPAD_H / 2 - BUTTON_SIZE / 2 - row * (BUTTON_SIZE + BUTTON_GAP)
            sketch += Pos(x, y) * RectangleRounded(BUTTON_SIZE, BUTTON_SIZE, BUTTON_RADIUS)
    return sketch


def stack_emboss(base_sketch: Sketch, emboss_sketch: Sketch) -> Part:
    base = extrude(base_sketch, BADGE_THICKNESS)
    fg = extrude(emboss_sketch, EMBOSS_THICKNESS)
    fg_lifted = fg.moved(Location((0, 0, BADGE_THICKNESS)))
    return Compound(children=[base, fg_lifted])


def tile(width: float = 30.0, height: float = 30.0, corner: float = 4.0) -> Part:
    bg = RectangleRounded(width, height, corner)
    fg = keypad_sketch(0, 0)
    return stack_emboss(bg, fg)


def pill(width: float = 25.0, height: float = 40.0) -> Part:
    """Stadium / pill shape: rectangle with two semicircle caps."""
    radius = width / 2
    body_h = height - 2 * radius
    if body_h < 0:
        raise ValueError("pill height must be at least its width")
    bg = Sketch()
    if body_h > 0:
        bg += Rectangle(width, body_h)
    bg += Pos(0, body_h / 2) * Circle(radius)
    bg += Pos(0, -body_h / 2) * Circle(radius)
    fg = keypad_sketch(0, 0)
    return stack_emboss(bg, fg)


def round_badge(diameter: float = 32.0) -> Part:
    bg = Sketch() + Circle(diameter / 2)
    fg = keypad_sketch(0, 0)
    return stack_emboss(bg, fg)


def lockup(font_path: str, width: float = 30.0, height: float = 50.0,
           corner: float = 4.0, font_size: float = 7.5) -> Part:
    """Vertical badge: keypad above, 'Digits' wordmark below."""
    bg = RectangleRounded(width, height, corner)

    # Keypad sits in upper portion, centered around y = +12.
    keypad_cy = (height / 2) - 5 - KEYPAD_H / 2  # 5mm top padding then keypad
    fg_keypad = keypad_sketch(0, keypad_cy)

    # Wordmark sits in lower portion, centered around y = -14ish.
    wordmark_cy = -(height / 2) + 8  # baseline ~8mm above bottom
    text = Text(
        "Digits",
        font_size=font_size,
        font_path=font_path,
        align=(Align.CENTER, Align.CENTER),
    )
    fg_text = Pos(0, wordmark_cy) * text

    fg = fg_keypad + fg_text
    return stack_emboss(bg, fg)


def write_lockup_svg(ttf_path: str, out_path: Path,
                     bg_fill: str = "#1a140e", fg_fill: str = "#ede3d1") -> None:
    """Write the lockup SVG with the wordmark embedded as paths.

    Coordinates in mm; viewBox 0 0 30 50 to match lockup-30x50mm.stl.
    """
    from fontTools.pens.svgPathPen import SVGPathPen
    from fontTools.ttLib import TTFont

    font = TTFont(ttf_path)
    glyphs = font.getGlyphSet()
    cmap = font.getBestCmap()
    upem = font["head"].unitsPerEm

    font_size_mm = 7.5
    scale = font_size_mm / upem

    glyph_runs: list[tuple[str, float]] = []
    cursor = 0.0
    for ch in "Digits":
        glyph = glyphs[cmap[ord(ch)]]
        pen = SVGPathPen(glyphs)
        glyph.draw(pen)
        d = pen.getCommands()
        if d:
            glyph_runs.append((d, cursor))
        cursor += glyph.width * scale
    total_width = cursor

    text_x_left = 15.0 - total_width / 2
    text_baseline_y = 41.0
    glyph_paths = "".join(
        f'    <path d="{d}" transform="translate({text_x_left + offset:.3f} {text_baseline_y}) scale({scale:.6f} {-scale:.6f})"/>\n'
        for d, offset in glyph_runs
    )

    keypad_x_offset = (30 - KEYPAD_W) / 2
    keypad_y_top = 4.0
    keypad_rects = ""
    for row in range(4):
        for col in range(3):
            x = keypad_x_offset + col * (BUTTON_SIZE + BUTTON_GAP)
            y = keypad_y_top + row * (BUTTON_SIZE + BUTTON_GAP)
            keypad_rects += (
                f'    <rect x="{x}" y="{y}" width="{BUTTON_SIZE}" '
                f'height="{BUTTON_SIZE}" rx="{BUTTON_RADIUS}"/>\n'
            )

    svg = (
        '<svg xmlns="http://www.w3.org/2000/svg" width="30mm" height="50mm" '
        'viewBox="0 0 30 50">\n'
        f'  <g id="badge-bg" fill="{bg_fill}">\n'
        '    <rect width="30" height="50" rx="4"/>\n'
        '  </g>\n'
        f'  <g id="badge-fg" fill="{fg_fill}">\n'
        f'{keypad_rects}{glyph_paths}'
        '  </g>\n'
        '</svg>\n'
    )
    out_path.write_text(svg)


def main():
    if not WOFF2.exists():
        sys.exit(f"missing {WOFF2}")

    with tempfile.TemporaryDirectory() as tmp:
        tmp_woff = Path(tmp) / "fraunces.woff2"
        shutil.copy(WOFF2, tmp_woff)
        if shutil.which("woff2_decompress") is None:
            sys.exit("woff2_decompress not on PATH; install woff2 (pacman -S woff2)")
        subprocess.run(["woff2_decompress", str(tmp_woff)], check=True, cwd=tmp)
        ttf_path = str(tmp_woff.with_suffix(".ttf"))

        stl_targets = {
            "tile-30mm.stl": tile(),
            "pill-25x40mm.stl": pill(),
            "round-32mm.stl": round_badge(),
            "lockup-30x50mm.stl": lockup(ttf_path),
        }
        for name, part in stl_targets.items():
            out = HERE / name
            export_stl(part, str(out))
            print(f"wrote {name} ({out.stat().st_size:,} bytes)")

        write_lockup_svg(ttf_path, HERE / "lockup-30x50mm.svg")
        print("wrote lockup-30x50mm.svg")


if __name__ == "__main__":
    main()
