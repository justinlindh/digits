# Keypad badges

Small adhesive-mounted badges carrying the Digits keypad mark, sized for FDM printing on a Bambu X1C with the AMS for two-color filament swap.

## Files

- `tile-30mm.svg`: 30 mm rounded square. Most compact, prints fastest.
- `pill-25x40mm.svg`: 25 mm wide, 40 mm tall vertical pill. Has the most quiet space around the mark.
- `round-32mm.svg`: 32 mm circle. Most "logo medallion."
- `preview.html`: open in a browser to compare all three shapes across the palette options below.

Each SVG declares two filled groups: `#badge-bg` (the silhouette) and `#badge-fg` (the 12 keypad buttons). The colors written into the file are visual cues only. The actual print color is whatever filament you load.

## Printing on Bambu X1C

1. Bambu Studio: **File > Import > Import SVG**, select one of the three shape files.
2. When asked for height, set **base = 1.6 mm** (the silhouette) and **emboss = 0.4 mm** (the keypad buttons). Total badge thickness 2.0 mm.
3. The slicer will detect the two color regions in the SVG. Assign filament A to the silhouette, filament B to the buttons.
4. Print orientation: flat on the build plate, badge face up. No supports.
5. Recommended layer height: 0.16 mm (gives the keypad buttons 2-3 layers of color B; tactile but not catchy).

For a thinner badge (e.g. mounting on a phone where 2 mm is too thick), reduce the base to 1.0 mm. The 0.4 mm emboss stays the same.

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

All three shapes use the same keypad geometry: 12 rounded squares in a 3x4 grid, each button 4 mm with 1 mm gaps, total mark footprint 14 mm wide x 19 mm tall. The mark is centered on each silhouette. If you want to scale, scale the whole SVG uniformly. The keypad never falls outside the silhouette.
