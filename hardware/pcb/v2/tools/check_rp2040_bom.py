#!/usr/bin/env python3
"""Verify RP2040 cluster BOM satisfies audit invariants."""
import sys
import xml.etree.ElementTree as ET
from collections import defaultdict

tree = ET.parse('/tmp/digits-sch.xml')
root = tree.getroot()

# Build ref -> (value, footprint)
refs = {}
for comp in root.findall(".//components/comp"):
    ref = comp.get("ref")
    val = comp.findtext("value", "")
    fp = comp.findtext("footprint", "")
    refs[ref] = (val, fp)

# Build net -> [(ref, pin)]
nets = defaultdict(list)
for net in root.findall(".//nets/net"):
    name = net.get("name")
    for node in net.findall("node"):
        nets[name].append((node.get("ref"), node.get("pin")))

def caps_on(net_name, value_filter=None):
    out = []
    for ref, pin in nets.get(net_name, []):
        if ref.startswith("C"):
            val, fp = refs.get(ref, ("", ""))
            if value_filter is None or val == value_filter:
                out.append((ref, val, fp))
    # dedupe by ref
    seen = {}
    for r, v, f in out:
        seen[r] = (v, f)
    return [(r, v, f) for r, (v, f) in seen.items()]

def caps_between(netA, netB, value_filter=None):
    a = {r for r, v, f in caps_on(netA, value_filter)}
    b = {r for r, v, f in caps_on(netB)}
    both = sorted(a & b)
    return [(r, *refs[r]) for r in both]

print("=" * 60)
print("RP2040 BOM Audit - Invariant Check")
print("=" * 60)

fails = 0

def check(label, ok, detail=""):
    global fails
    mark = "PASS" if ok else "FAIL"
    if not ok:
        fails += 1
    print(f"  [{mark}] {label}{(' - ' + detail) if detail else ''}")

# Identify +3V3 and DVDD nets (may be prefixed by sheet or /)
def find_net(candidates):
    for n in nets:
        if n in candidates or n.split("/")[-1] in candidates:
            return n
    return None

pwr3v3 = find_net({"+3V3", "/+3V3"})
dvdd = find_net({"DVDD_1V1", "/DVDD_1V1", "Net-(U3-DVDD)"})
gnd = find_net({"GND"})
print(f"Nets: +3V3={pwr3v3}  DVDD_1V1={dvdd}  GND={gnd}")

# All caps between +3V3 and GND
caps_3v3 = caps_between(pwr3v3, gnd)
caps_dvdd = caps_between(dvdd, gnd)
print(f"\nAll caps +3V3<->GND ({len(caps_3v3)}):")
for r, v, f in sorted(caps_3v3):
    print(f"  {r:6} {v:8} {f}")
print(f"\nAll caps DVDD_1V1<->GND ({len(caps_dvdd)}):")
for r, v, f in sorted(caps_dvdd):
    print(f"  {r:6} {v:8} {f}")

# Invariants
print("\nInvariants:")

# C5/C6 = 15pF
for ref in ("C5", "C6"):
    v, f = refs.get(ref, ("", ""))
    check(f"{ref} value 15pF", v == "15pF", v)
    check(f"{ref} footprint 0402", "0402" in f, f)

# C10 = 1uF 0402
v, f = refs.get("C10", ("", ""))
check("C10 value 1uF", v == "1uF", v)
check("C10 footprint 0402", "0402" in f, f)

# C12-C16 = 0402
for ref in ("C12", "C13", "C14", "C15", "C16"):
    v, f = refs.get(ref, ("", ""))
    check(f"{ref} footprint 0402", "0402" in f, f)

# Y1 value
v, f = refs.get("Y1", ("", ""))
check("Y1 value ABM8-272-T3", v == "ABM8-272-T3", v)

# Count of 100nF caps on +3V3: IOVDD(6) + VREGIN? wait 1uF + USBVDD + AVDD + FLASH
# §2.9.1 IOVDD: 6 caps 100nF
# §2.9.3 VREG_VIN: 1 cap 1uF
# §2.9.4 USB_VDD: 1 cap 100nF
# §2.9.5 ADC_AVDD: 1 cap 100nF
# Flash VCC: 1 cap 100nF
# Total on +3V3 from RP2040 cluster: 6 + 1 + 1 + 1 + 1 = 9 (plus the VREG_VIN 1uF)
# But there may be other unrelated caps on +3V3 from other clusters.

# Count 100nF on +3V3
n_100nF_3v3 = sum(1 for r, v, f in caps_3v3 if v == "100nF")
check(f">=9 100nF caps on +3V3 (need 6 IOVDD + USBVDD + AVDD + FLASH)",
      n_100nF_3v3 >= 9, f"found {n_100nF_3v3}")

# 1uF on +3V3 for VREG_VIN
n_1uF_3v3 = sum(1 for r, v, f in caps_3v3 if v == "1uF")
check(">=1 1uF cap on +3V3 (VREG_VIN)", n_1uF_3v3 >= 1, f"found {n_1uF_3v3}")

# DVDD_1V1: 2 x 100nF per-pin + 1 x 1uF bulk (C10)
n_100nF_dvdd = sum(1 for r, v, f in caps_dvdd if v == "100nF")
n_1uF_dvdd = sum(1 for r, v, f in caps_dvdd if v == "1uF")
check(">=2 100nF caps on DVDD_1V1 (pins 23, 50)",
      n_100nF_dvdd >= 2, f"found {n_100nF_dvdd}")
check(">=1 1uF cap on DVDD_1V1 (C10 bulk)",
      n_1uF_dvdd >= 1, f"found {n_1uF_dvdd}")

# New caps C28-C34 must exist
for ref in ("C28", "C29", "C30", "C31", "C32", "C33", "C34"):
    check(f"{ref} exists", ref in refs, str(refs.get(ref)))

print(f"\nResult: {'ALL PASS' if fails == 0 else f'{fails} FAIL(S)'}")
sys.exit(1 if fails else 0)
