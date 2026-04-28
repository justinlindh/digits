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
BUTTON_RADIUS = 0.6  # crisp keys: tightened from 1.0 to reduce the pillow look
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


def _wordmark_lockup(text_str: str, font_path: str, font_size: float,
                     width: float, height: float, corner: float,
                     wordmark_cy: float) -> Part:
    bg = RectangleRounded(width, height, corner)
    keypad_cy = (height / 2) - 5 - KEYPAD_H / 2
    fg_keypad = keypad_sketch(0, keypad_cy)
    text = Text(
        text_str,
        font_size=font_size,
        font_path=font_path,
        align=(Align.CENTER, Align.CENTER),
    )
    fg_text = Pos(0, wordmark_cy) * text
    return stack_emboss(bg, fg_keypad + fg_text)


def lockup(font_path: str, width: float = 30.0, height: float = 50.0,
           corner: float = 4.0, font_size: float = 7.5) -> Part:
    """Vertical badge: keypad above, 'Digits' (Fraunces) wordmark below."""
    return _wordmark_lockup("Digits", font_path, font_size, width, height,
                            corner, wordmark_cy=-(height / 2) + 8)


def lockup_arcade(font_path: str, width: float = 30.0, height: float = 50.0,
                  corner: float = 4.0, font_size: float = 4.0) -> Part:
    """Vertical badge: keypad above, 'DIGITS' (Press Start 2P) wordmark below.

    Press Start 2P is a 16x16-pixel-grid font; cap height equals font_size,
    so 4mm renders glyphs at exactly 4mm tall. Keep the wordmark a touch
    higher than the Fraunces version: pixel glyphs have no descenders.
    """
    return _wordmark_lockup("DIGITS", font_path, font_size, width, height,
                            corner, wordmark_cy=-(height / 2) + 7.5)


def _glyph_paths_d(text_str: str, ttf_path: str, font_size_mm: float,
                   x_center: float, baseline_y: float) -> str:
    from fontTools.pens.svgPathPen import SVGPathPen
    from fontTools.ttLib import TTFont

    font = TTFont(ttf_path)
    glyphs = font.getGlyphSet()
    cmap = font.getBestCmap()
    upem = font["head"].unitsPerEm
    scale = font_size_mm / upem

    glyph_runs: list[tuple[str, float]] = []
    cursor = 0.0
    for ch in text_str:
        glyph = glyphs[cmap[ord(ch)]]
        pen = SVGPathPen(glyphs)
        glyph.draw(pen)
        d = pen.getCommands()
        if d:
            glyph_runs.append((d, cursor))
        cursor += glyph.width * scale

    text_x_left = x_center - cursor / 2
    return "".join(
        f'    <path d="{d}" transform="translate({text_x_left + offset:.3f} {baseline_y}) scale({scale:.6f} {-scale:.6f})"/>\n'
        for d, offset in glyph_runs
    )


def _keypad_rects_svg(viewBox_w: float, y_top: float = 4.0) -> str:
    keypad_x_offset = (viewBox_w - KEYPAD_W) / 2
    out = ""
    for row in range(4):
        for col in range(3):
            x = keypad_x_offset + col * (BUTTON_SIZE + BUTTON_GAP)
            y = y_top + row * (BUTTON_SIZE + BUTTON_GAP)
            out += (
                f'    <rect x="{x}" y="{y}" width="{BUTTON_SIZE}" '
                f'height="{BUTTON_SIZE}" rx="{BUTTON_RADIUS}"/>\n'
            )
    return out


def write_lockup_svg(ttf_path: str, out_path: Path, *,
                     text_str: str = "Digits",
                     font_size_mm: float = 7.5,
                     baseline_y: float = 41.0,
                     bg_fill: str = "#1a140e",
                     fg_fill: str = "#ede3d1") -> None:
    """Write a lockup SVG with the wordmark embedded as paths.

    Coordinates in mm; viewBox 0 0 30 50 to match lockup-*.stl.
    """
    glyph_paths = _glyph_paths_d(text_str, ttf_path, font_size_mm,
                                 x_center=15.0, baseline_y=baseline_y)
    keypad_rects = _keypad_rects_svg(viewBox_w=30.0)

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


PRESS_START_TTF = HERE / "fonts/PressStart2P-Regular.ttf"


def main():
    if not WOFF2.exists():
        sys.exit(f"missing {WOFF2}")
    if not PRESS_START_TTF.exists():
        sys.exit(f"missing {PRESS_START_TTF}")

    with tempfile.TemporaryDirectory() as tmp:
        tmp_woff = Path(tmp) / "fraunces.woff2"
        shutil.copy(WOFF2, tmp_woff)
        if shutil.which("woff2_decompress") is None:
            sys.exit("woff2_decompress not on PATH; install woff2 (pacman -S woff2)")
        subprocess.run(["woff2_decompress", str(tmp_woff)], check=True, cwd=tmp)
        fraunces_ttf = str(tmp_woff.with_suffix(".ttf"))
        press_start_ttf = str(PRESS_START_TTF)

        stl_targets = {
            "tile-30mm.stl": tile(),
            "pill-25x40mm.stl": pill(),
            "round-32mm.stl": round_badge(),
            "lockup-30x50mm.stl": lockup(fraunces_ttf),
            "lockup-arcade-30x50mm.stl": lockup_arcade(press_start_ttf),
        }
        for name, part in stl_targets.items():
            out = HERE / name
            export_stl(part, str(out))
            print(f"wrote {name} ({out.stat().st_size:,} bytes)")

        write_lockup_svg(fraunces_ttf, HERE / "lockup-30x50mm.svg")
        print("wrote lockup-30x50mm.svg")
        write_lockup_svg(
            press_start_ttf, HERE / "lockup-arcade-30x50mm.svg",
            text_str="DIGITS", font_size_mm=4.0, baseline_y=39.5,
            bg_fill="#1a140e", fg_fill="#ff7a59",
        )
        print("wrote lockup-arcade-30x50mm.svg")


if __name__ == "__main__":
    main()
