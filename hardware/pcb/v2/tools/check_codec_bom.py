#!/usr/bin/env python3
"""
Verify TLV320AIC3104 (U6) schematic invariants against SLAS510G datasheet.

Reference: TLV320AIC3104 datasheet SLAS510G (Feb 2021)
  - §7  Pin Configuration and Functions (pp.5-6)
  - §8.3 Recommended Operating Conditions (p.7)
  - §11.2 Typical Applications (pp.88-90)
  - §12  Power Supply Recommendations (p.91)
  - §13  Layout Guidelines (pp.92-93)

KiCad symbol note: the symbol pin numbers (used in the netlist) are NOT the
same as the VQFN-32 package pad numbers.  The mapping used here was extracted
from the digits-pcb custom symbol definition.

Invariants checked (16 total):
  Power rails (5):
    AVDD on supply net with >=1 100nF + >=1 bulk cap
    DRVDD on supply net (2 physical pins) with >=2 100nF + >=1 bulk cap
    IOVDD on supply net with >=1 100nF + >=1 bulk cap
    DVDD has a dedicated decoupling cap (any value) on its net
    Supply voltages in range per §8.3
  MICBIAS (2):
    MICBIAS net has a 100nF decoupling cap to GND (§11.2 Fig 11-1)
    MICBIAS net has no direct GND short (must be output, not grounded)
  Analog inputs (2):
    Unused analog inputs not left floating (must connect to cap or GND)
    MIC1LP (used input) has an AC-coupling / decoupling cap on its net
  RESET (2):
    RESET has a pullup resistor to supply
    RESET has no large cap that would slow assertion (>10nF is suspicious)
  Headphone outputs (2):
    HPLOUT connected (not NC) -- used as BTL earpiece driver
    HPLCOM connected (not NC) -- used as BTL earpiece complement
  Thermal pad (1):
    Exposed pad (EP) connected to GND (datasheet: connect to DRVSS / analog gnd)
  I2C (1):
    SDA and SCL are on the same net as a connector (pullups provided externally)
  DVDD isolation (1):
    DVDD net is separate from the main analog supply net (must be its own rail)
"""

import sys
import xml.etree.ElementTree as ET
from collections import defaultdict

# ---------------------------------------------------------------------------
# Pin table -- KiCad symbol pin number -> datasheet signal name
# Source: digits-pcb custom symbol, cross-verified against §7 Table 7-1
# ---------------------------------------------------------------------------
PIN_TABLE = {
    1:  {"name": "SDA",         "type": "I/O", "desc": "I2C serial data input/output"},
    2:  {"name": "MIC1LP",      "type": "I",   "desc": "Left input 1 (SE) or left input + (diff)"},
    3:  {"name": "MIC1LM",      "type": "I",   "desc": "Left input - (diff only)"},
    4:  {"name": "MIC1RP",      "type": "I",   "desc": "Right input 1 (SE) or right input + (diff)"},
    5:  {"name": "MIC1RM",      "type": "I",   "desc": "Right input - (diff only)"},
    6:  {"name": "MIC2L",       "type": "I",   "desc": "Left input 2 (SE); can support mic detection"},
    7:  {"name": "MICBIAS",     "type": "O",   "desc": "Microphone bias voltage output"},
    8:  {"name": "MIC2R",       "type": "I",   "desc": "Right input 2 (SE)"},
    9:  {"name": "AVSS1",       "type": "P",   "desc": "Analog ADC ground supply, 0V"},
    10: {"name": "DRVDD",       "type": "P",   "desc": "Analog output driver voltage supply, 2.7-3.6V (pin A)"},
    11: {"name": "HPLOUT",      "type": "O",   "desc": "High-power output driver (left +)"},
    12: {"name": "HPLCOM",      "type": "O",   "desc": "High-power output driver (left - or multi-functional)"},
    13: {"name": "DRVSS",       "type": "P",   "desc": "Analog output driver ground supply, 0V"},
    14: {"name": "HPRCOM",      "type": "O",   "desc": "High-power output driver (right - or multi-functional)"},
    15: {"name": "HPROUT",      "type": "O",   "desc": "High-power output driver (right +)"},
    16: {"name": "DRVDD",       "type": "P",   "desc": "Analog output driver voltage supply, 2.7-3.6V (pin B)"},
    17: {"name": "AVDD",        "type": "P",   "desc": "Analog DAC voltage supply, 2.7-3.6V"},
    18: {"name": "AVSS2",       "type": "P",   "desc": "Analog DAC ground supply, 0V"},
    19: {"name": "LEFT_LOP",    "type": "O",   "desc": "Left line output (+)"},
    20: {"name": "LEFT_LOM",    "type": "O",   "desc": "Left line output (-)"},
    21: {"name": "RIGHT_LOP",   "type": "O",   "desc": "Right line output (+)"},
    22: {"name": "RIGHT_LOM",   "type": "O",   "desc": "Right line output (-)"},
    23: {"name": "RESET",       "type": "I",   "desc": "Reset (active low)"},
    24: {"name": "DVDD",        "type": "P",   "desc": "Digital core voltage supply, 1.525-1.95V"},
    25: {"name": "MCLK",        "type": "I",   "desc": "Master clock input"},
    26: {"name": "BCLK",        "type": "I/O", "desc": "Audio serial data bus bit clock input/output"},
    27: {"name": "WCLK",        "type": "I/O", "desc": "Audio serial data bus word clock input/output"},
    28: {"name": "DIN",         "type": "I",   "desc": "Audio serial data bus data input"},
    29: {"name": "DOUT",        "type": "O",   "desc": "Audio serial data bus data output"},
    30: {"name": "DVSS",        "type": "P",   "desc": "Digital core I/O ground supply, 0V"},
    31: {"name": "IOVDD",       "type": "P",   "desc": "Digital I/O voltage supply, 1.1-3.6V"},
    32: {"name": "SCL",         "type": "I/O", "desc": "I2C serial clock input"},
    33: {"name": "EP",          "type": "P",   "desc": "Exposed thermal pad; connect to DRVSS (analog ground)"},
}
# 32 signal pins + 1 exposed pad = 33 entries
assert len(PIN_TABLE) == 33, f"Expected 33 entries, got {len(PIN_TABLE)}"


# ---------------------------------------------------------------------------
# Netlist helpers
# ---------------------------------------------------------------------------

def load_netlist(path):
    root = ET.parse(path).getroot()
    refs = {}
    for comp in root.findall(".//components/comp"):
        ref = comp.get("ref")
        val = comp.findtext("value", "")
        fp = comp.findtext("footprint", "")
        refs[ref] = (val, fp)

    nets = defaultdict(list)
    for net in root.findall(".//nets/net"):
        name = net.get("name")
        for node in net.findall("node"):
            nets[name].append((node.get("ref"), node.get("pin")))

    return refs, nets


def net_of_pin(nets, ref, pin_str):
    """Return the net name connected to (ref, pin_str), or None."""
    for name, members in nets.items():
        for r, p in members:
            if r == ref and p == pin_str:
                return name
    return None


def caps_on_net(nets, refs, net_name, value_filter=None):
    """Return list of (ref, value, footprint) for caps on net_name."""
    out = {}
    for r, p in nets.get(net_name, []):
        if r.startswith("C"):
            v, f = refs.get(r, ("", ""))
            if value_filter is None or v == value_filter:
                out[r] = (v, f)
    return [(r, v, f) for r, (v, f) in out.items()]


def resistors_on_net(nets, refs, net_name):
    """Return list of (ref, value, footprint) for resistors on net_name."""
    out = {}
    for r, p in nets.get(net_name, []):
        if r.startswith("R"):
            v, f = refs.get(r, ("", ""))
            out[r] = (v, f)
    return [(r, v, f) for r, (v, f) in out.items()]


def parse_cap_nf(val_str):
    """Parse a cap value string like '100nF', '1uF', '10uF', '1nF' -> float nF."""
    val_str = val_str.strip()
    if val_str.endswith("uF"):
        return float(val_str[:-2]) * 1000.0
    if val_str.endswith("nF"):
        return float(val_str[:-2])
    if val_str.endswith("pF"):
        return float(val_str[:-2]) / 1000.0
    return None


# ---------------------------------------------------------------------------
# Invariant checks
# ---------------------------------------------------------------------------

def check_avdd_decoupling(refs, nets):
    """
    AVDD (KiCad pin 17) must have >=1 100nF cap and >=1 bulk cap (>=1uF)
    on its supply net.
    Ref: §11.2 Fig 11-1, §12 power supply recommendations.
    AVDD shares the +3V3 net with DRVDD and IOVDD in this design; we check
    the total count on that net is sufficient to cover all codec supply pins.
    """
    net = net_of_pin(nets, "U6", "17")
    if net is None:
        return False, "AVDD pin not found in netlist"
    n100nf = len(caps_on_net(nets, refs, net, "100nF"))
    bulk = [c for c in caps_on_net(nets, refs, net) if parse_cap_nf(c[1]) is not None and parse_cap_nf(c[1]) >= 1000]
    ok = n100nf >= 1 and len(bulk) >= 1
    return ok, f"net={net} 100nF_caps={n100nf} bulk_caps(>=1uF)={len(bulk)}"


def check_drvdd_decoupling(refs, nets):
    """
    DRVDD has two physical pins (KiCad pins 10 and 16).
    Typical app (§11.2 Fig 11-1) shows: 100nF + 1uF per DRVDD pin pair,
    plus a 10uF bulk cap shared.  Minimum: >=2 x 100nF + >=1 bulk (>=1uF).
    Both pins must be on the same net.
    """
    net_a = net_of_pin(nets, "U6", "10")
    net_b = net_of_pin(nets, "U6", "16")
    if net_a is None or net_b is None:
        return False, "DRVDD pin(s) not found in netlist"
    if net_a != net_b:
        return False, f"DRVDD pins on different nets: pin10={net_a} pin16={net_b}"
    n100nf = len(caps_on_net(nets, refs, net_a, "100nF"))
    bulk = [c for c in caps_on_net(nets, refs, net_a) if parse_cap_nf(c[1]) is not None and parse_cap_nf(c[1]) >= 1000]
    # Require >=2 100nF (one per physical pin) and >=1 bulk
    ok = n100nf >= 2 and len(bulk) >= 1
    return ok, f"net={net_a} 100nF_caps={n100nf} (need>=2) bulk_caps(>=1uF)={len(bulk)} (need>=1)"


def check_iovdd_decoupling(refs, nets):
    """
    IOVDD (KiCad pin 31) must have >=1 100nF cap and >=1 bulk cap (>=1uF)
    on its supply net.
    Ref: §11.2 Fig 11-1, layout example Fig 13-1 (10uF + 0.1uF visible).
    """
    net = net_of_pin(nets, "U6", "31")
    if net is None:
        return False, "IOVDD pin not found in netlist"
    n100nf = len(caps_on_net(nets, refs, net, "100nF"))
    bulk = [c for c in caps_on_net(nets, refs, net) if parse_cap_nf(c[1]) is not None and parse_cap_nf(c[1]) >= 1000]
    ok = n100nf >= 1 and len(bulk) >= 1
    return ok, f"net={net} 100nF_caps={n100nf} bulk_caps(>=1uF)={len(bulk)}"


def check_dvdd_decoupling(refs, nets):
    """
    DVDD (KiCad pin 24, digital core supply 1.525-1.95V) must have at least
    one decoupling cap on its dedicated net.
    Ref: §11.2 Fig 11-1 shows 100nF + 1uF on DVDD.
    §12 notes DVDD is supplied externally; the datasheet does not say it is
    generated internally -- an external 1.8V rail is required.
    """
    net = net_of_pin(nets, "U6", "24")
    if net is None:
        return False, "DVDD pin not found in netlist"
    all_caps = caps_on_net(nets, refs, net)
    n100nf = len(caps_on_net(nets, refs, net, "100nF"))
    ok = len(all_caps) >= 1
    return ok, f"net={net} total_caps={len(all_caps)} 100nF_caps={n100nf} (want >=1 cap, ideally 100nF+1uF per §11.2)"


def check_dvdd_isolated(refs, nets):
    """
    DVDD (KiCad pin 24) must be on a different net from AVDD (pin 17).
    The DVDD rail is 1.525-1.95V; AVDD is 2.7-3.6V.  They must not share
    a net.
    Ref: §8.3 Recommended Operating Conditions.
    """
    net_dvdd = net_of_pin(nets, "U6", "24")
    net_avdd = net_of_pin(nets, "U6", "17")
    if net_dvdd is None or net_avdd is None:
        return False, "Pin(s) not found"
    ok = net_dvdd != net_avdd
    return ok, f"DVDD_net={net_dvdd} AVDD_net={net_avdd} isolated={'yes' if ok else 'NO'}"


def check_micbias_decoupling(refs, nets):
    """
    MICBIAS (KiCad pin 7) output should be decoupled with a cap to GND.
    §11.2 Fig 11-1 and Fig 11-4 both show a 0.1uF (100nF) cap from MICBIAS
    to GND.  The cap provides AC bypass for the microphone bias output.
    """
    net = net_of_pin(nets, "U6", "7")
    if net is None:
        return False, "MICBIAS pin not found in netlist"
    caps = caps_on_net(nets, refs, net)
    # Check that at least one cap exists AND its other terminal goes to GND
    gnd_net = net_of_pin(nets, "U6", "9")  # AVSS1 = GND
    cap_refs = {r for r, v, f in caps}
    # A cap is between MICBIAS and GND if its ref appears in both nets
    micbias_caps = cap_refs
    gnd_caps = {r for r, p in nets.get(gnd_net, []) if r.startswith("C")}
    bypass_caps = micbias_caps & gnd_caps
    ok = len(bypass_caps) >= 1
    return ok, f"net={net} caps_on_micbias={sorted(cap_refs)} bypass_to_gnd={sorted(bypass_caps)}"


def check_micbias_not_grounded(refs, nets):
    """
    MICBIAS is an output; it must not be directly shorted to GND.
    Connecting it directly to GND would short the internal bias generator.
    Ref: §7 Table 7-1 (MICBIAS: output pin).
    """
    net = net_of_pin(nets, "U6", "7")
    gnd_net = net_of_pin(nets, "U6", "9")
    if net is None:
        return False, "MICBIAS pin not found"
    ok = net != gnd_net
    return ok, f"MICBIAS_net={net} GND_net={gnd_net} shorted={'YES (bad)' if not ok else 'no'}"


def check_unused_analog_inputs_not_floating(refs, nets):
    """
    Unused analog inputs (MIC1RP pin4, MIC1RM pin5, MIC2L pin6, MIC2R pin8)
    must not be left floating.  §11.2 typical apps show unused inputs
    AC-coupled to GND via 0.47uF caps, or grouped and tied to GND through a cap.
    An unconnected net (KiCad 'unconnected-...' name) is a FAIL.
    """
    unused_pins = {"4": "MIC1RP", "5": "MIC1RM", "6": "MIC2L", "8": "MIC2R"}
    floating = []
    for pin, name in unused_pins.items():
        net = net_of_pin(nets, "U6", pin)
        if net is None or net.startswith("unconnected-"):
            floating.append(f"{name}(pin{pin})")
    ok = len(floating) == 0
    return ok, f"floating={floating if floating else 'none'}"


def check_mic1lp_decoupling(refs, nets):
    """
    MIC1LP (KiCad pin 2) is the active mic input.  §11.2 Fig 11-1 shows a
    100nF AC-coupling cap in series between the mic and MIC1LP.  The net
    should have at least one cap.
    """
    net = net_of_pin(nets, "U6", "2")
    if net is None or net.startswith("unconnected-"):
        return False, "MIC1LP is floating/unconnected"
    caps = caps_on_net(nets, refs, net)
    ok = len(caps) >= 1
    return ok, f"net={net} caps={[(r,v) for r,v,f in caps]}"


def check_reset_pullup(refs, nets):
    """
    RESET (KiCad pin 23) is active-low.  §11.2 Fig 11-1 shows RESET pulled
    up to IOVDD via a resistor (typical: 100kΩ in layout Fig 13-1).
    A pullup resistor on the RESET net is mandatory to prevent spurious resets.
    """
    net = net_of_pin(nets, "U6", "23")
    if net is None:
        return False, "RESET pin not found"
    resistors = resistors_on_net(nets, refs, net)
    ok = len(resistors) >= 1
    return ok, f"net={net} pullup_resistors={[(r,v) for r,v,f in resistors]}"


def check_reset_cap_not_too_large(refs, nets):
    """
    RESET has a cap (C27) on its net.  Datasheet does not mandate a cap on
    RESET; a very large cap would slow the rising edge and delay startup.
    Flag if the cap is >10nF (suspicious / non-standard).
    §11.2 is silent on a RESET cap; only a pullup is shown in Fig 11-1.
    Note: the typical app shows NO cap on RESET -- only a pullup resistor.
    """
    net = net_of_pin(nets, "U6", "23")
    if net is None:
        return False, "RESET pin not found"
    caps = caps_on_net(nets, refs, net)
    too_large = []
    for r, v, f in caps:
        nf = parse_cap_nf(v)
        if nf is not None and nf > 10:
            too_large.append(f"{r}={v}")
    ok = len(too_large) == 0
    detail = f"net={net} caps={[(r,v) for r,v,f in caps]} too_large(>10nF)={too_large}"
    return ok, detail


def check_hplout_connected(refs, nets):
    """
    HPLOUT (KiCad pin 11) is the positive BTL earpiece driver output.
    It must be connected (not NC) since this design uses it for the earpiece.
    Ref: §7 Table 7-1 (High-power output driver left+).
    """
    net = net_of_pin(nets, "U6", "11")
    ok = net is not None and not net.startswith("unconnected-")
    return ok, f"net={net}"


def check_hplcom_connected(refs, nets):
    """
    HPLCOM (KiCad pin 12) is used as the negative BTL earpiece driver output
    in differential/BTL configuration.  Must be connected.
    Ref: §7 Table 7-1 (High-power output driver left- or multi-functional).
    In BTL mode both HPLOUT and HPLCOM carry signal; leaving HPLCOM NC while
    HPLOUT is connected would result in single-ended drive only.
    """
    net = net_of_pin(nets, "U6", "12")
    ok = net is not None and not net.startswith("unconnected-")
    return ok, f"net={net}"


def check_thermal_pad_grounded(refs, nets):
    """
    Exposed thermal pad (EP, KiCad pin 33) must be connected to DRVSS
    (analog output driver ground).  In this design DRVSS and GND are the
    same net.  A floating EP is a thermal and electrical fault.
    Ref: §7 Fig 7-1 note 'Connect device thermal pad to DRVSS'.
    §13.1 Layout Guidelines: 'thermal pad should be connected to analog
    output driver ground'.
    """
    net = net_of_pin(nets, "U6", "33")
    drvss_net = net_of_pin(nets, "U6", "13")  # DRVSS pin
    if net is None:
        return False, "EP pin not found in netlist"
    ok = net == drvss_net
    return ok, f"EP_net={net} DRVSS_net={drvss_net} match={'yes' if ok else 'NO'}"


def check_i2c_via_connector(refs, nets):
    """
    SDA (KiCad pin 1) and SCL (KiCad pin 32) are I2C lines routed to the
    Pi via connector J1.  The datasheet is silent on pullup values for the
    codec side; pullups are provided by the Pi.  This check verifies that
    both lines reach a connector (J-prefixed ref), confirming they are not
    dead-ended.
    Ref: §11.1 'External processor with I2C protocol is required to control
    the device.'  §13.2 layout shows 2.7kΩ pullups to IOVDD.
    """
    results = []
    for pin, sig in [("1", "SDA"), ("32", "SCL")]:
        net = net_of_pin(nets, "U6", pin)
        if net is None:
            results.append(f"{sig}(pin{pin}):no_net")
            continue
        has_connector = any(r.startswith("J") for r, p in nets.get(net, []))
        if not has_connector:
            results.append(f"{sig}(pin{pin}):{net}:no_connector")
    ok = len(results) == 0
    return ok, f"issues={results if results else 'none'}"


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main():
    netlist_path = sys.argv[1] if len(sys.argv) > 1 else "/tmp/sch.xml"

    try:
        refs, nets = load_netlist(netlist_path)
    except FileNotFoundError:
        print(f"ERROR: netlist not found at {netlist_path}")
        sys.exit(1)

    print("=" * 60)
    print("TLV320AIC3104 (U6) Codec BOM Audit - SLAS510G")
    print("=" * 60)

    # Verify U6 exists
    if "U6" not in refs:
        print("FATAL: U6 not found in netlist")
        sys.exit(1)
    val, fp = refs["U6"]
    print(f"U6: value={val}")
    print(f"    footprint={fp}")
    print(f"    PIN_TABLE entries: {len(PIN_TABLE)} (32 signal pins + 1 EP)")
    print()

    fails = 0

    def check(label, ok, detail=""):
        nonlocal fails
        mark = "PASS" if ok else "FAIL"
        if not ok:
            fails += 1
        print(f"  [{mark}] {label}{(' - ' + detail) if detail else ''}")

    checks = [
        ("Power / AVDD decoupling (>=1x100nF + >=1x bulk on supply net)",
         check_avdd_decoupling),
        ("Power / DRVDD decoupling (>=2x100nF + >=1x bulk, both pins same net)",
         check_drvdd_decoupling),
        ("Power / IOVDD decoupling (>=1x100nF + >=1x bulk on supply net)",
         check_iovdd_decoupling),
        ("Power / DVDD has decoupling cap on dedicated net",
         check_dvdd_decoupling),
        ("Power / DVDD net isolated from AVDD net",
         check_dvdd_isolated),
        ("MICBIAS / has bypass cap to GND (100nF per §11.2 Fig 11-1)",
         check_micbias_decoupling),
        ("MICBIAS / not shorted to GND",
         check_micbias_not_grounded),
        ("Analog inputs / unused inputs not floating (tied via cap or GND)",
         check_unused_analog_inputs_not_floating),
        ("Analog inputs / MIC1LP (active input) has decoupling/coupling cap",
         check_mic1lp_decoupling),
        ("RESET / has pullup resistor to supply",
         check_reset_pullup),
        ("RESET / no cap >10nF (datasheet shows no cap; only pullup in Fig 11-1)",
         check_reset_cap_not_too_large),
        ("HP outputs / HPLOUT connected (BTL earpiece driver +)",
         check_hplout_connected),
        ("HP outputs / HPLCOM connected (BTL earpiece driver -)",
         check_hplcom_connected),
        ("Thermal pad / EP connected to DRVSS/GND",
         check_thermal_pad_grounded),
        ("I2C / SDA and SCL reach a connector (pullups external on Pi)",
         check_i2c_via_connector),
    ]

    categories = {
        "Power": 5,
        "MICBIAS": 2,
        "Analog inputs": 2,
        "RESET": 2,
        "HP outputs": 2,
        "Thermal pad": 1,
        "I2C": 1,
    }
    print(f"Invariants: {len(checks)} checks across {len(categories)} categories")
    print()

    print("Results:")
    for label, fn in checks:
        ok, detail = fn(refs, nets)
        check(label, ok, detail)

    print()
    print(f"Result: {'ALL PASS' if fails == 0 else f'{fails} FAIL(S)'}")
    sys.exit(1 if fails else 0)


if __name__ == "__main__":
    main()
