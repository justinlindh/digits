# PCB Pre-Fab Checklist

Run through this before submitting Gerbers to JLCPCB (or any fab). Written
after the 2026-04-18 incident where v2 was submitted with a 2A switching node
on a 0.2mm trace because the schematic label existed but the project netclass
pattern was missing. KiCad's built-in DRC did not catch it.

## Per-net current budget

Build a table of every power-carrying net with nominal, peak, and fault
current. Cross-reference datasheets and use cases.

- [ ] Every net carrying >0.5A peak is assigned to an appropriate netclass
  (not Default)
- [ ] IPC-2221 thermal rise at peak current is within design target
  (typically 10-20C rise over ambient at 1oz external copper)
- [ ] Switching nodes (e.g., LM2596 pin 2, similar buck regulators) are
  routed wide (0.5-0.75mm) AND short, per the regulator datasheet's layout
  section
- [ ] Switching nodes have NO vias (they're EMI loops if routed across layers)

## Netclass pattern integrity

- [ ] Every hierarchical-path net (prefixed with `/sheet/name`) matches its
  pattern exactly. `VIN_RAW` pattern won't match `/VIN_RAW` net — use the
  full path or a wildcard like `*VIN_RAW*`
- [ ] Every unlabeled switching-regulator output has an explicit net label
  (e.g., `VSW`, `SW_NODE`). KiCad auto-names like `Net-(D1-K)` silently
  miss Power patterns
- [ ] All netclass_patterns in .kicad_pro actually match nets in the
  .kicad_pcb (run `kicad-cli sch export netlist` and grep for each pattern)

## Connectivity checks

- [ ] `kicad-cli pcb drc` returns 0 unconnected items
- [ ] `kicad-cli pcb drc` returns 0 track_dangling warnings (or, if any
  remain, each is confirmed intentional and not evidence of a split net)
- [ ] **Important: KiCad's "unconnected pads" check misses split-net cases
  where every pad has *some* routed trace but the net is broken into
  disjoint subgraphs.** Manually verify high-importance rails (typically
  +5V, +3V3, any battery/USB input) by clicking a pad in KiCad and confirming
  the full net highlights, not just a subgraph

## Thermal pad / EP stitching

- [ ] Every power IC's thermal pad has stitching vias per datasheet
  (LM2596: multiple GND vias under the TO-263 tab; DRV8871/TLV320/RP2040:
  4+ GND vias inside the EP bounding box)
- [ ] GND return paths for high-current switching nets are on a ground
  pour OR wide traces (same current as the positive-side switching trace)

## Decoupling

- [ ] Each IC's decoupling caps are placed within the datasheet-specified
  distance of their power pin (typically <5mm for 100nF close-in, <10mm
  for bulk)
- [ ] Correct cap values per datasheet (not "close enough")

## Protection

- [ ] Reverse-polarity protection on any wall-adapter input
- [ ] Fuses or polyfuses on external power inputs
- [ ] TVS diodes on inputs exposed to hot-insertion (barrel jacks, USB, etc.)
- [ ] Inductor saturation current exceeds switching regulator peak switch
  current

## Consider custom DRC rules

KiCad's .kicad_dru file can enforce rules beyond the built-in checks. At
minimum, add rules that fail DRC if any Power-class-candidate net falls
through to Default class. See `hardware/pcb/v2/kicad/digits-pcb.kicad_dru`
for an example.

## Final fab-ready verification

- [ ] `kicad-cli pcb drc --severity-error` returns 0 errors
- [ ] Silkscreen version stamp matches the PCB revision
- [ ] BOM is reconciled between schematic and PCB (no LCSC part number
  drift)
- [ ] Gerbers re-exported after any last-minute PCB edits (easy to forget)
