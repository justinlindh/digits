# Keypad badges

Small adhesive-mounted badges carrying the Digits keypad mark, sized for FDM printing on a Bambu X1C with the AMS for two-color filament swap.

## Files

Each shape ships as both an SVG (geometry source, useful for slicer SVG-import or as a flat preview) and an STL (pre-built 3D mesh, drop straight into Bambu Studio).

- `tile-30mm.svg` / `.stl`: 30 mm rounded square. Most compact, prints fastest.
- `pill-25x40mm.svg` / `.stl`: 25 mm wide, 40 mm tall vertical pill. Quietest framing.
- `round-32mm.svg` / `.stl`: 32 mm circle. Most "logo medallion."
- `lockup-30x50mm.svg` / `.stl`: 30 mm wide, 50 mm tall rounded rectangle. Keypad above + "Digits" wordmark in Fraunces below. Use this when you want the full lockup as a name plate.
- `make_badges.py`: source of truth for badge geometry. Re-run after editing constants to regenerate STLs and the lockup SVG. Requires `build123d` (pip), `woff2_decompress` (pacman -S woff2), and the Fraunces TTF, which is decompressed from `server/internal/web/static/fonts/fraunces-variable.woff2` at runtime.
- `*-top.png`: top-down render of each STL for visual reference.
- `preview.html`: open in a browser to compare the three icon-only shapes across the palette options below.

Each SVG declares two filled groups: `#badge-bg` (the silhouette) and `#badge-fg` (everything raised). The fill colors written into the file are visual cues only. The actual print color is whatever filament you load.

## Printing on Bambu X1C

The fastest path is the STL. Two parts are baked in (the silhouette and the raised features), stacked in Z, no slicer assembly needed.

1. Bambu Studio: **File > Import > Import STL**, select one of the four shape files.
2. The mesh arrives at correct mm dimensions. Lay flat on the build plate, badge face up, no supports.
3. Two-color via filament swap at z = 1.6 mm: in the slicer's "Filament" panel, add a swap at that height. Filament A prints layers 0 to 1.6 mm (the silhouette). Filament B prints 1.6 to 2.0 mm (the raised keypad and, on the lockup, the wordmark).
4. Recommended layer height: 0.16 mm (gives the raised features 2-3 layers of color B; tactile but not catchy).

For a thinner badge, edit `BADGE_THICKNESS` in `make_badges.py` (default 1.6 mm) and re-run. The 0.4 mm emboss stays the same.

If you prefer SVG import, the same files in their `.svg` form work in Bambu Studio too: import, set the same heights manually (`base = 1.6 mm`, `emboss = 0.4 mm`), and the slicer will split the two colored groups for you.

## Adhesive

3M VHB tape (the thin variant, around 0.4 mm) on the smooth bottom face works well on the painted metal of vintage phone housings. Cyanoacrylate also works but is permanent. Avoid hot glue: the PLA softens around it.

## Palettes

The SVG defaults ship a different palette per shape so you can see the range without opening the slicer. Swap in any of these by editing the two `fill="..."` attributes in the SVG, or just load whichever filaments you have and ignore the file colors.

| Name        | Background | Foreground | Vibe                                   |
|-------------|------------|------------|----------------------------------------|
| Parchment   | `#fff7e6`  | `#c1410c`  | Matches the digits.family favicon.     |
| Walnut      | `#1a140e`  | `#ede3d1`  | Matches the webapp intercom dark theme.|
| Bone        | `#f5f1ea`  | `#2a1f18`  | Matches the webapp intercom light theme.|
| Sage        | `#626f47`  | `#fff7e6`  | Calm, woodsy.                          |
| Brass       | `#1a140e`  | `#dda867`  | Luxe vintage.                          |
| Coral       | `#ff7a59`  | `#fff7e6`  | Warm pop.                              |
| Charcoal    | `#222222`  | `#e0b964`  | Industrial signage.                    |
| Plum        | `#5a3a4f`  | `#f4e4b8`  | Unexpected.                            |
| Cobalt      | `#1c3a6e`  | `#f7eed8`  | Oceanic.                               |
| Terracotta  | `#b85a3c`  | `#e8d4a4`  | Mediterranean tile.                    |

## Geometry

All four shapes use the same keypad geometry: 12 rounded squares in a 3x4 grid, each button 4 mm with 1 mm gaps, total mark footprint 14 mm wide x 19 mm tall. On the icon-only shapes the mark is centered on the silhouette. On the lockup, the keypad sits in the upper half and the "Digits" wordmark (Fraunces 500, 7.5 mm cap-to-baseline) sits in the lower half.

If you want to scale, scale the SVG uniformly or pass new dimensions to the matching function in `make_badges.py` and rerun.
