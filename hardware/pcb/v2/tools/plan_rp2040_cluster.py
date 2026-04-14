#!/usr/bin/env python3
"""Final placement plan using Minimal-KiCAD reference offsets (3.05mm)."""
import sys, math
sys.path.insert(0, '/home/justin/src/digits/hardware/pcb/v2/tools')
from check_decoupling import load_pcb
import pathlib

PCB = pathlib.Path('/home/justin/src/digits/hardware/pcb/v2/kicad/digits-pcb.kicad_pcb')
fps = load_pcb(PCB)
u3 = fps["U3"]
u4 = fps["U4"]

OFFSET = 3.05  # reference radial offset
# For strict 1:1 with 6 top-edge caps, we need to spread them along edge.

def pin(n): return u3["pads"][str(n)]

# Top edge: 6 caps in a single row, x offset slightly from each pin to achieve 0.8mm cap pitch
top_pin_x = [46.0, 46.4, 46.8, 48.0, 48.4, 48.8]  # pins 50, 49, 48, 45, 44, 43
# Reference has 2 caps serving all top-edge pins with multiple pin sharing (dx up to 2.4mm).
# Strict 1:1: 6 caps with pitch 0.8mm, centered on pin span midpoint (47.4)
# gives cap x = 45.4, 46.2, 47.0, 47.8, 48.6, 49.4
# Assign each cap to nearest pin: 45.4->pin50, 46.2->pin49, 47.0->pin48, 47.8->pin45, 48.6->pin44, 49.4->pin43
cap_x_top = [45.4, 46.2, 47.0, 47.8, 48.6, 49.4]
pin_assign_top = [50, 49, 48, 45, 44, 43]  # corresponding pin for each cap position

# Our cap refs per pin (from decoupling_targets)
top_pin_to_ref = {43: "C33", 44: "C31", 45: "C10", 48: "C32", 49: "C28", 50: "C30"}
bot_pin_to_ref = {22: "C14", 23: "C29", 26: "C35"}
left_pin_to_ref = {1: "C12", 10: "C13"}
right_pin_to_ref = {33: "C15", 42: "C16"}

targets = []  # (ref, pin, target_x, target_y, rot, rationale)

# Top edge
top_y = 35.46 - OFFSET  # 32.41
for cx, pn in zip(cap_x_top, pin_assign_top):
    ref = top_pin_to_ref[pn]
    targets.append((ref, pn, cx, top_y, 90, f"top edge, single row"))

# Bottom edge: 3 caps
bot_y = 42.335 + OFFSET  # 45.385
targets.append(("C14", 22, 46.40, bot_y, 270, "bottom edge"))
targets.append(("C29", 23, 47.20, bot_y, 270, "bottom edge (offset 0.4 from pin)"))
targets.append(("C35", 26, 48.00, bot_y, 270, "bottom edge"))

# Left edge: 2 caps
left_x = 42.763 - OFFSET  # 39.713
for pn, ref in left_pin_to_ref.items():
    px, py = pin(pn)
    targets.append((ref, pn, left_x, py, 180, f"left edge"))

# Right edge: 2 caps
right_x = 49.638 + OFFSET  # 52.688
for pn, ref in right_pin_to_ref.items():
    px, py = pin(pn)
    targets.append((ref, pn, right_x, py, 0, f"right edge"))

# Flash C34: U4.8 with dy=-3.05 offset
u4_8 = u4["pads"]["8"]
targets.append(("C34", None, u4_8[0], u4_8[1] - OFFSET, 90, "flash VCC decap"))

# Y1: move south of bottom cap row. Y1 center x = (pin20.x + pin21.x)/2 = (45.6+46.0)/2 = 45.8
# Y1 center y = bot cap row y + cap body (0.5) + gap + Y1 half-height (0.85) ~ 45.385 + 0.5 + 1.0 + 0.85 ~ 47.7
# Let's put Y1 at (45.80, 48.00) for symmetric routing.
Y1_TARGET = (45.80, 48.00, 0)
# Y1 pads at Y1_TARGET ± (1.10, 0.85):
y1_pad_1 = (45.80 - 1.10, 48.00 + 0.85)  # (44.70, 48.85) bottom-left
y1_pad_2 = (45.80 + 1.10, 48.00 + 0.85)  # (46.90, 48.85) bottom-right
y1_pad_3 = (45.80 + 1.10, 48.00 - 0.85)  # (46.90, 47.15) top-right
y1_pad_4 = (45.80 - 1.10, 48.00 - 0.85)  # (44.70, 47.15) top-left

# C5 near Y1.1, C6 near Y1.2, both at Y1's bottom row y=48.85 flanking outside
C5_TARGET = (43.20, 48.85, 0)  # 1.5mm left of Y1.1
C6_TARGET = (48.40, 48.85, 0)  # 1.5mm right of Y1.2
targets.append(("C5", "Y1.1", C5_TARGET[0], C5_TARGET[1], 0, "crystal load cap XIN side"))
targets.append(("C6", "Y1.2", C6_TARGET[0], C6_TARGET[1], 0, "crystal load cap XOUT side"))

# R9 damping: between U3.21 and Y1.2. Place near Y1 so trace from R9 to Y1.2 is short.
# Top edge of Y1 footprint at y=47.15, so R9 above Y1 at y=46.5 or so
# But bottom cap row B at y=45.885 body. Gap: 46.5-0.5=46.0, 45.885+0.5=46.385. Overlap!
# Put R9 to the right of Y1: (48.40, 48.00) rot 90 - but that overlaps C6 at (48.40, 48.85)!
# Put R9 at (46.45, 46.5) rot 90 - between bot cap row and Y1 top
# Actually safer: put R9 LEFT of Y1 at (43.20, 48.00) rot 0 - but that overlaps C5 at (43.20, 48.85)
# Put R9 below Y1 at y=49.4: too tight with C5/C6
# Best: R9 at (46.45, 46.60) rot 90 - vertical in narrow space
R9_TARGET = (46.45, 46.60, 90)

# R5 (RUN pullup) can go anywhere. Near pin 26 (48.0, 42.335): place just outside U3 to right of bottom row
R5_TARGET = (49.20, 45.385, 90)  # beside C35 on bottom row

# R3/R4 USB termination: near pins 46/47 on U3 top edge. They don't exist in our pin dump...
# Actually RP2040 USB_DM/DP are 46/47. For our U3 footprint top edge has pads 43-56 going right-to-left:
# x = 48.8(43), 48.4(44), 48.0(45), 47.6(46), 47.2(47), 46.8(48), 46.4(49), 46.0(50), 45.6(51), 45.2(52), 44.8(53), 44.4(54), 44.0(55), 43.6(56)
# Pin 46 USB_DM at (47.6, 35.46), Pin 47 USB_DP at (47.2, 35.46)
# But these are hidden under the top cap row. Place R3/R4 further out (outer row)
R3_TARGET = (47.6, 32.41 - 1.60, 90)  # (47.6, 30.81) outside top cap row
R4_TARGET = (47.2, 32.41 - 1.60, 90)  # (47.2, 30.81)

# Print
print(f"{'ref':5} {'pin':6} {'target_xy':>18} {'rot':>4} {'dist_pad':>9}  rationale")
print("-" * 90)
for ref, pn, x, y, rot, note in targets:
    if isinstance(pn, int):
        pad = u3["pads"].get(str(pn))
        if pad is None:
            dist_s = "?"
        else:
            dist_s = f"{math.hypot(x-pad[0], y-pad[1]):6.2f}mm"
    elif isinstance(pn, str) and pn.startswith("Y1."):
        # resolve
        pad_n = pn.split(".")[1]
        if fps.get("Y1"):
            pad = fps["Y1"]["pads"].get(pad_n)
            # but we're moving Y1! target Y1 pads, not current
            if pad_n == "1": pad = y1_pad_1
            elif pad_n == "2": pad = y1_pad_2
            elif pad_n == "3": pad = y1_pad_3
            elif pad_n == "4": pad = y1_pad_4
            dist_s = f"{math.hypot(x-pad[0], y-pad[1]):6.2f}mm"
    elif pn is None and ref == "C34":
        pad = u4_8
        dist_s = f"{math.hypot(x-pad[0], y-pad[1]):6.2f}mm"
    else:
        dist_s = "-"
    print(f"{ref:5} {str(pn):6} ({x:7.2f},{y:7.2f}) {rot:4} {dist_s:>9}  {note}")

print()
print(f"Y1 target: ({Y1_TARGET[0]:.2f}, {Y1_TARGET[1]:.2f}) rot {Y1_TARGET[2]}")
print(f"R9 target: ({R9_TARGET[0]:.2f}, {R9_TARGET[1]:.2f}) rot {R9_TARGET[2]}")
print(f"R5 target: ({R5_TARGET[0]:.2f}, {R5_TARGET[1]:.2f}) rot {R5_TARGET[2]}")
print(f"R3 target: ({R3_TARGET[0]:.2f}, {R3_TARGET[1]:.2f}) rot {R3_TARGET[2]}")
print(f"R4 target: ({R4_TARGET[0]:.2f}, {R4_TARGET[1]:.2f}) rot {R4_TARGET[2]}")

# Collision check with realistic body sizes
def body_halfsize(rot):
    # 0402 is 1.0 x 0.5mm body. rot 0/180 -> 1.0 in x, 0.5 in y. rot 90/270 -> 0.5 in x, 1.0 in y.
    if rot % 180 == 0:
        return (0.5, 0.25)
    else:
        return (0.25, 0.5)

print()
print("Collision audit (body touches):")
pts = []
for ref, _, x, y, rot, _ in targets:
    pts.append((ref, x, y, rot))
pts.append(("R9", *R9_TARGET))
pts.append(("R5", *R5_TARGET))
pts.append(("R3", *R3_TARGET))
pts.append(("R4", *R4_TARGET))

collisions = 0
for i in range(len(pts)):
    for j in range(i+1, len(pts)):
        ri, xi, yi, roti = pts[i]
        rj, xj, yj, rotj = pts[j]
        hxi, hyi = body_halfsize(roti)
        hxj, hyj = body_halfsize(rotj)
        dx = abs(xi - xj) - (hxi + hxj)
        dy = abs(yi - yj) - (hyi + hyj)
        if dx < 0 and dy < 0:
            pen = max(-dx, -dy)
            print(f"  COLLISION: {ri} vs {rj}  body overlap {pen:.2f}mm")
            collisions += 1
print(f"  {collisions} body collisions")
