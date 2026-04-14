#!/usr/bin/env python3
"""Build ringer.kicad_sch from scratch using kiutils.

Creates a hierarchical sub-sheet for the DRV8871 ringer module with:
- U1 (DRV8871DDA), R1 (33k), C1 (100nF), C2 (47uF)
- 6 hierarchical labels: +12V, GND, RINGER_IN1, RINGER_IN2, BELL_A, BELL_B
- All wiring per the ringer-module-spec.md connectivity requirements
"""
from __future__ import annotations

import copy
import math
import uuid as uuid_mod

from kiutils.items.common import ColorRGBA, Effects, Font, Justify, Position, Stroke
from kiutils.items.schitems import (
    Connection,
    HierarchicalLabel,
    Junction,
    SchematicSymbol,
    SymbolProjectInstance,
    SymbolProjectPath,
)
from kiutils.schematic import Schematic, HierarchicalSheetInstance

REPO_ROOT = __import__("pathlib").Path(__file__).resolve().parents[4]
RINGER_SCH = REPO_ROOT / "hardware/pcb/v2/kicad/ringer.kicad_sch"
CODEC_SCH = REPO_ROOT / "hardware/pcb/v2/kicad/codec.kicad_sch"
PARENT_SCH = REPO_ROOT / "hardware/pcb/v2/kicad/digits-pcb.kicad_sch"

RINGER_UUID = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"


def new_uuid() -> str:
    return str(uuid_mod.uuid4())


def pin_abs(sym_x: float, sym_y: float, sym_angle: float,
            pin_x: float, pin_y: float) -> tuple[float, float]:
    """Compute absolute schematic position of a pin tip.

    KiCad lib coords use Y-up; schematic uses Y-down.
    For angle=0: abs = (sym_x + pin_x, sym_y - pin_y).
    """
    rad = math.radians(sym_angle)
    cos_a = round(math.cos(rad), 6)
    sin_a = round(math.sin(rad), 6)
    rot_x = pin_x * cos_a - pin_y * sin_a
    rot_y = pin_x * sin_a + pin_y * cos_a
    return sym_x + rot_x, sym_y - rot_y


def make_wire(x1: float, y1: float, x2: float, y2: float) -> Connection:
    conn = Connection()
    conn.type = "wire"
    conn.points = [
        Position(X=x1, Y=y1, angle=None, unlocked=False),
        Position(X=x2, Y=y2, angle=None, unlocked=False),
    ]
    conn.stroke = Stroke(width=0, type="default", color=None)
    conn.uuid = new_uuid()
    return conn


def make_junction(x: float, y: float) -> Junction:
    j = Junction()
    j.position = Position(X=x, Y=y, angle=None, unlocked=False)
    j.diameter = 0
    j.color = ColorRGBA(R=0, G=0, B=0, A=0, precision=4)
    j.uuid = new_uuid()
    return j


def make_hlabel(text: str, x: float, y: float, shape: str, angle: float) -> HierarchicalLabel:
    h = HierarchicalLabel()
    h.text = text
    h.position = Position(X=x, Y=y, angle=angle, unlocked=False)
    h.shape = shape
    h.effects = Effects(
        font=Font(face=None, height=1.27, width=1.27, thickness=None,
                  bold=False, italic=False, lineSpacing=None, color=None),
        justify=Justify(horizontally="right", vertically=None, mirror=False),
        hide=False,
        href=None,
    )
    h.uuid = new_uuid()
    return h


def make_schematic_symbol(
    lib_id: str,
    entry_name: str,
    ref: str,
    value: str,
    footprint: str,
    datasheet: str,
    x: float,
    y: float,
    angle: float,
    sheet_uuid: str,
    unit: int = 1,
) -> SchematicSymbol:
    from kiutils.items.common import Property as KiProperty

    sym = SchematicSymbol()
    # Setting libId parses "Library:EntryName" into libraryNickname + entryName.
    # Do NOT overwrite libraryNickname or entryName afterward or the prefix is lost.
    sym.libId = lib_id
    sym.libName = None
    sym.position = Position(X=x, Y=y, angle=angle, unlocked=False)
    sym.unit = unit
    sym.inBom = True
    sym.onBoard = True
    sym.dnp = False
    sym.fieldsAutoplaced = False
    sym.mirror = None
    sym.uuid = new_uuid()
    sym.pins = {}

    def mkprop(key, val, px, py, pangle=0, hide=False):
        p = KiProperty()
        p.key = key
        p.value = val
        p.position = Position(X=px, Y=py, angle=pangle, unlocked=False)
        p.effects = Effects(
            font=Font(face=None, height=1.27, width=1.27, thickness=None,
                      bold=False, italic=False, lineSpacing=None, color=None),
            justify=Justify(horizontally=None, vertically=None, mirror=False),
            hide=hide,
            href=None,
        )
        p.showName = False
        p.id = None
        return p

    props = []
    props.append(mkprop("Reference", ref, x + 2.54, y - 2.54))
    props.append(mkprop("Value", value, x + 2.54, y + 2.54))
    props.append(mkprop("Footprint", footprint, x, y, hide=True))
    props.append(mkprop("Datasheet", datasheet, x, y, hide=True))
    props.append(mkprop("Description", "", x, y, hide=True))
    sym.properties = props

    path = SymbolProjectPath()
    path.sheetInstancePath = f"/{sheet_uuid}"
    path.reference = ref
    path.unit = unit

    inst = SymbolProjectInstance()
    inst.name = "digits-pcb"
    inst.paths = [path]

    sym.instances = [inst]
    return sym


def build_ringer():
    # Load parent to extract lib symbols
    parent = Schematic().from_file(str(PARENT_SCH))

    drv_libsym = None
    r_libsym = None
    c_libsym = None
    cp_libsym = None
    for ls in parent.libSymbols:
        if ls.entryName == "DRV8871DDA":
            drv_libsym = copy.deepcopy(ls)
        elif ls.entryName == "R":
            r_libsym = copy.deepcopy(ls)
        elif ls.entryName == "C":
            c_libsym = copy.deepcopy(ls)
        elif ls.entryName == "C_Polarized":
            cp_libsym = copy.deepcopy(ls)

    if drv_libsym is None:
        raise RuntimeError("DRV8871DDA not found in parent libSymbols")
    if r_libsym is None:
        raise RuntimeError("R not found in parent libSymbols")
    if c_libsym is None:
        raise RuntimeError("C not found in parent libSymbols")

    # Bootstrap from codec schematic
    codec = Schematic().from_file(str(CODEC_SCH))
    ringer = copy.deepcopy(codec)

    ringer.uuid = RINGER_UUID
    ringer.schematicSymbols = []
    ringer.hierarchicalLabels = []
    ringer.globalLabels = []
    ringer.graphicalItems = []
    ringer.junctions = []
    ringer.noConnects = []
    ringer.labels = []
    ringer.libSymbols = []
    ringer.sheets = []
    ringer.busEntries = []
    ringer.busAliases = []

    si = HierarchicalSheetInstance()
    si.instancePath = "/"
    si.page = "1"
    ringer.sheetInstances = [si]
    ringer.symbolInstances = []

    # Set lib symbols
    ringer.libSymbols = [drv_libsym, r_libsym, c_libsym]
    if cp_libsym:
        ringer.libSymbols.append(cp_libsym)

    # ----------------------------------------------------------------
    # Layout (all in mm, Y increases downward in schematic)
    # ----------------------------------------------------------------
    # U1 (DRV8871DDA) at (130, 80), angle=0
    # Pin positions (verified against Y-down convention):
    #   5 VM    (0, +10.16) local => abs (130, 80-10.16) = (130, 69.84)  [top]
    #   3 IN1  (-10.16, +5.08)  => abs (119.84, 80-5.08) = (119.84, 74.92) [left]
    #   2 IN2  (-10.16, +2.54)  => abs (119.84, 80-2.54) = (119.84, 77.46) [left]
    #   4 ILIM  (10.16, -5.08)  => abs (140.16, 80+5.08) = (140.16, 85.08) [right]
    #   6 OUT1  (10.16, +5.08)  => abs (140.16, 80-5.08) = (140.16, 74.92) [right]
    #   8 OUT2  (10.16, +2.54)  => abs (140.16, 80-2.54) = (140.16, 77.46) [right]
    #   1/7/9 GND (0,-10.16)   => abs (130, 80+10.16) = (130, 90.16)  [bottom]

    U1_X, U1_Y = 130.0, 80.0

    DRV_PINS_LOCAL = {
        "1": (0.0, -10.16),
        "2": (-10.16, 2.54),
        "3": (-10.16, 5.08),
        "4": (10.16, -5.08),
        "5": (0.0, 10.16),
        "6": (10.16, 5.08),
        "7": (0.0, -10.16),
        "8": (10.16, 2.54),
        "9": (0.0, -10.16),
    }

    def u1p(n):
        px, py = DRV_PINS_LOCAL[n]
        return pin_abs(U1_X, U1_Y, 0, px, py)

    # Compute key U1 pin positions
    p_vm  = u1p("5")    # (130, 69.84) -- top, VM
    p_in1 = u1p("3")    # (119.84, 74.92) -- left, IN1
    p_in2 = u1p("2")    # (119.84, 77.46) -- left, IN2
    p_ilim = u1p("4")   # (140.16, 85.08) -- right, ILIM
    p_out1 = u1p("6")   # (140.16, 74.92) -- right, OUT1
    p_out2 = u1p("8")   # (140.16, 77.46) -- right, OUT2
    p_gnd_bottom = u1p("1")  # (130, 90.16) -- bottom, GND

    # R1: vertical (angle=0). Place at X=148, with pin1 (upper) at same Y as U1.ILIM
    # pin1 local (0, +3.81): abs = (R_X, R_Y - 3.81) = upper pin
    # pin2 local (0, -3.81): abs = (R_X, R_Y + 3.81) = lower pin
    # We want R1.pin1 = (148, 85.08) so R1_Y = 85.08 + 3.81 = 88.89
    R1_X = 148.0
    R1_Y = p_ilim[1] + 3.81   # pin1 at same Y as ILIM

    def r1p(n):
        if n == "1":
            return pin_abs(R1_X, R1_Y, 0, 0, 3.81)   # upper
        return pin_abs(R1_X, R1_Y, 0, 0, -3.81)      # lower

    p_r1_1 = r1p("1")  # top of R1 -- connects to ILIM
    p_r1_2 = r1p("2")  # bottom of R1 -- connects to GND

    # C1 (100nF HF bypass): vertical, pin1 (upper) at same Y as VM
    # We want C1.pin1 = (115, 69.84) so C1_Y = 69.84 + 3.81 = 73.65
    C1_X = 115.0
    C1_Y = p_vm[1] + 3.81

    def c1p(n):
        if n == "1":
            return pin_abs(C1_X, C1_Y, 0, 0, 3.81)   # upper
        return pin_abs(C1_X, C1_Y, 0, 0, -3.81)      # lower

    p_c1_1 = c1p("1")
    p_c1_2 = c1p("2")

    # C2 (47uF bulk): vertical, pin1 (upper) at same Y as VM
    C2_X = 105.0
    C2_Y = p_vm[1] + 3.81

    def c2p(n):
        if n == "1":
            return pin_abs(C2_X, C2_Y, 0, 0, 3.81)
        return pin_abs(C2_X, C2_Y, 0, 0, -3.81)

    p_c2_1 = c2p("1")
    p_c2_2 = c2p("2")

    print("Pin positions:")
    print(f"  U1.5 VM:    {p_vm}")
    print(f"  U1.3 IN1:   {p_in1}")
    print(f"  U1.2 IN2:   {p_in2}")
    print(f"  U1.4 ILIM:  {p_ilim}")
    print(f"  U1.6 OUT1:  {p_out1}")
    print(f"  U1.8 OUT2:  {p_out2}")
    print(f"  U1 GND bot: {p_gnd_bottom}")
    print(f"  R1.1: {p_r1_1}")
    print(f"  R1.2: {p_r1_2}")
    print(f"  C1.1: {p_c1_1}")
    print(f"  C1.2: {p_c1_2}")
    print(f"  C2.1: {p_c2_1}")
    print(f"  C2.2: {p_c2_2}")

    # ----------------------------------------------------------------
    # Create schematic symbols
    # ----------------------------------------------------------------
    u1 = make_schematic_symbol(
        "Driver_Motor:DRV8871DDA", "DRV8871DDA",
        "U1", "DRV8871DDAR",
        "Package_SO:SOIC-8-1EP_3.9x4.9mm_P1.27mm_EP2.29x3mm",
        "https://www.ti.com/lit/ds/symlink/drv8871.pdf",
        U1_X, U1_Y, 0.0, RINGER_UUID,
    )

    r1 = make_schematic_symbol(
        "Device:R", "R",
        "R1", "33k",
        "Resistor_SMD:R_0402_1005Metric", "~",
        R1_X, R1_Y, 0.0, RINGER_UUID,
    )

    c1 = make_schematic_symbol(
        "Device:C", "C",
        "C1", "100nF",
        "Capacitor_SMD:C_0402_1005Metric", "~",
        C1_X, C1_Y, 0.0, RINGER_UUID,
    )

    c2_lib_id = "Device:C_Polarized" if cp_libsym else "Device:C"
    c2_entry = "C_Polarized" if cp_libsym else "C"
    c2 = make_schematic_symbol(
        c2_lib_id, c2_entry,
        "C2", "47uF",
        "Capacitor_SMD:CP_Elec_5x5.3", "~",
        C2_X, C2_Y, 0.0, RINGER_UUID,
    )

    ringer.schematicSymbols = [u1, r1, c1, c2]

    # ----------------------------------------------------------------
    # Hierarchical labels (sheet ports)
    # ----------------------------------------------------------------
    # Angle convention: 0=right, 90=up, 180=left, 270=down
    # The wire connection point of an hlabel is at (x, y)
    # angle=0 means label text extends to the right, connection on left side
    # angle=180 means label text extends to the left, connection on right side

    # +12V - left of C2, connection at (98, VM_Y)
    # VM net runs horizontally at Y = p_vm[1] = 69.84
    VM_Y = p_vm[1]
    PLUS12V_X = 95.0
    hl_12v = make_hlabel("+12V", PLUS12V_X, VM_Y, "input", 0)
    # Wire from label to C2.1: (95, VM_Y) -> (105, VM_Y)
    # We need label connection point. For angle=0, connection is at (x, y).

    # GND labels - one at each GND connection point
    # R1.2 GND
    GND_R1_Y = p_r1_2[1] + 5.0
    hl_gnd_r1 = make_hlabel("GND", R1_X, GND_R1_Y, "input", 270)

    # C1.2 GND
    GND_C1_Y = p_c1_2[1] + 5.0
    hl_gnd_c1 = make_hlabel("GND", C1_X, GND_C1_Y, "input", 270)

    # C2.2 GND
    GND_C2_Y = p_c2_2[1] + 5.0
    hl_gnd_c2 = make_hlabel("GND", C2_X, GND_C2_Y, "input", 270)

    # U1 bottom GND (pins 1, 7, 9)
    GND_U1_Y = p_gnd_bottom[1] + 5.0
    hl_gnd_u1 = make_hlabel("GND", U1_X, GND_U1_Y, "input", 270)

    # RINGER_IN1 - left of U1.3 (IN1) at (119.84, 74.92)
    IN1_X = 107.0
    IN1_Y = p_in1[1]
    hl_in1 = make_hlabel("RINGER_IN1", IN1_X, IN1_Y, "input", 0)

    # RINGER_IN2 - left of U1.2 (IN2) at (119.84, 77.46)
    IN2_X = 107.0
    IN2_Y = p_in2[1]
    hl_in2 = make_hlabel("RINGER_IN2", IN2_X, IN2_Y, "input", 0)

    # BELL_A - right of U1.6 (OUT1) at (140.16, 74.92)
    BELLA_X = 153.0
    BELLA_Y = p_out1[1]
    hl_bella = make_hlabel("BELL_A", BELLA_X, BELLA_Y, "output", 180)

    # BELL_B - right of U1.8 (OUT2) at (140.16, 77.46)
    BELLB_X = 153.0
    BELLB_Y = p_out2[1]
    hl_bellb = make_hlabel("BELL_B", BELLB_X, BELLB_Y, "output", 180)

    ringer.hierarchicalLabels = [
        hl_12v, hl_gnd_r1, hl_gnd_c1, hl_gnd_c2, hl_gnd_u1,
        hl_in1, hl_in2, hl_bella, hl_bellb,
    ]

    # ----------------------------------------------------------------
    # Wires
    # ----------------------------------------------------------------
    wires = []
    junctions = []

    # VM net (+12V): label(95, VM_Y) -> C2.1(105, VM_Y) -> C1.1(115, VM_Y) -> U1.5(130, VM_Y)
    wires.append(make_wire(PLUS12V_X, VM_Y, p_c2_1[0], p_c2_1[1]))
    wires.append(make_wire(p_c2_1[0], p_c2_1[1], p_c1_1[0], p_c1_1[1]))
    wires.append(make_wire(p_c1_1[0], p_c1_1[1], p_vm[0], p_vm[1]))
    junctions.append(make_junction(p_c2_1[0], p_c2_1[1]))
    junctions.append(make_junction(p_c1_1[0], p_c1_1[1]))

    # GND for C2.2: C2.2 -> GND label
    wires.append(make_wire(p_c2_2[0], p_c2_2[1], C2_X, GND_C2_Y))

    # GND for C1.2: C1.2 -> GND label
    wires.append(make_wire(p_c1_2[0], p_c1_2[1], C1_X, GND_C1_Y))

    # GND for U1 bottom: U1 GND pins -> GND label
    wires.append(make_wire(p_gnd_bottom[0], p_gnd_bottom[1], U1_X, GND_U1_Y))

    # ILIM: U1.4 -> R1.1
    wires.append(make_wire(p_ilim[0], p_ilim[1], p_r1_1[0], p_r1_1[1]))

    # GND for R1.2: R1.2 -> GND label
    wires.append(make_wire(p_r1_2[0], p_r1_2[1], R1_X, GND_R1_Y))

    # RINGER_IN1: label -> U1.3
    wires.append(make_wire(IN1_X, IN1_Y, p_in1[0], p_in1[1]))

    # RINGER_IN2: label -> U1.2
    wires.append(make_wire(IN2_X, IN2_Y, p_in2[0], p_in2[1]))

    # BELL_A: U1.6 -> label
    wires.append(make_wire(p_out1[0], p_out1[1], BELLA_X, BELLA_Y))

    # BELL_B: U1.8 -> label
    wires.append(make_wire(p_out2[0], p_out2[1], BELLB_X, BELLB_Y))

    ringer.graphicalItems = wires
    ringer.junctions = junctions

    # Title block
    if ringer.titleBlock:
        ringer.titleBlock.title = "Ringer Module (DRV8871)"
        ringer.titleBlock.revision = "1"

    ringer.to_file(str(RINGER_SCH))
    print(f"\nSaved to {RINGER_SCH}")


if __name__ == "__main__":
    build_ringer()
