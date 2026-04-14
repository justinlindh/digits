#!/usr/bin/env python3
"""Step 1 ground-truth inspection of the RP2040 cluster on digits-pcb v2."""
import sys
sys.path.insert(0, '/home/justin/src/digits/hardware/pcb/v2/tools')
from check_decoupling import load_pcb, parse, find_all, get_at, footprint_reference
import pathlib, math, json

PCB = pathlib.Path('/home/justin/src/digits/hardware/pcb/v2/kicad/digits-pcb.kicad_pcb')
fps = load_pcb(PCB)

# extra: get footprint names/values (not just refs)
text = PCB.read_text()
tree = parse(text)
meta = {}
for fp in find_all(tree, "footprint"):
    ref = footprint_reference(fp)
    if ref is None:
        continue
    # footprint library identifier is the first string after (footprint ...)
    lib = fp[1] if len(fp) > 1 and isinstance(fp[1], str) else ""
    val = ""
    for prop in find_all(fp, "property"):
        if len(prop) >= 3 and prop[1] == "Value":
            val = prop[2]
    # layer
    layer = "F.Cu"
    l = None
    for child in fp:
        if isinstance(child, list) and child and child[0] == "layer":
            layer = child[1]
            break
    # locked?
    locked = any(isinstance(c, list) and c and c[0] == "locked" for c in fp)
    meta[ref] = {"lib": lib, "value": val, "layer": layer, "locked": locked}

cluster = ["U3", "U4", "Y1", "R3", "R4", "R5",
           "C5", "C6", "C10",
           "C12", "C13", "C14", "C15", "C16",
           "C28", "C29", "C30", "C31", "C32", "C33", "C34", "C35",
           "MH1", "MH2", "MH3", "SW1"]

print("=" * 78)
print("RP2040 CLUSTER GROUND TRUTH")
print("=" * 78)
print(f"{'ref':5} {'x':>8} {'y':>8} {'rot':>6} {'layer':6} {'lock':5} {'value':12} lib")
for ref in cluster:
    fp = fps.get(ref)
    m = meta.get(ref, {})
    if fp is None:
        print(f"{ref:5} NOT ON PCB")
        continue
    print(f"{ref:5} {fp['x']:8.3f} {fp['y']:8.3f} {fp['rot']:6.1f} "
          f"{m.get('layer','?'):6} {'LOCK' if m.get('locked') else '':5} "
          f"{m.get('value',''):12} {m.get('lib','')}")

# Power pins of U3 (absolute)
print()
print("U3 power/clock/reset pins (absolute):")
u3 = fps.get("U3")
if u3:
    pin_list = [1, 10, 19, 20, 21, 22, 23, 26, 33, 42, 43, 44, 45, 48, 49, 50, 57]
    for pin in pin_list:
        pad = u3["pads"].get(str(pin))
        if pad:
            print(f"  U3.{pin:<3} ({pad[0]:7.3f}, {pad[1]:7.3f})")

# U4 VCC pin
print()
u4 = fps.get("U4")
if u4:
    p = u4["pads"].get("8")
    print(f"U4.8 VCC: ({p[0]:.3f}, {p[1]:.3f})" if p else "U4 no pad 8")

# Y1 pads
print()
y1 = fps.get("Y1")
if y1:
    for k, v in sorted(y1["pads"].items()):
        print(f"Y1.{k} ({v[0]:.3f}, {v[1]:.3f})")

# Locked positions
print()
print("Locked reference points (must not collide):")
for ref in ["MH1", "MH2", "MH3", "SW1"]:
    fp = fps.get(ref)
    if fp:
        print(f"  {ref}: ({fp['x']:.3f}, {fp['y']:.3f}) locked={meta.get(ref,{}).get('locked')}")

# Board extents
xs = [fp["x"] for fp in fps.values()]
ys = [fp["y"] for fp in fps.values()]
print()
print(f"Component bbox: x ∈ [{min(xs):.1f}, {max(xs):.1f}]  y ∈ [{min(ys):.1f}, {max(ys):.1f}]")
