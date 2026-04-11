# PCB V2 Board Flip Plan

## Goal

Flip the board's mounting orientation inside the phone so all SMD components face up (toward the top of the phone enclosure) except SW1 (hook switch), which faces down toward the phone mechanism.

## Why

The current orientation has F.Cu (component side) facing the top of the phone. Flipping the board so components face the other direction improves clearance/fit inside the phone body.

## Current State (pre-flip)

- **F.Cu (KiCad "top")** = faces phone top = all SMD components, SW1
- **B.Cu (KiCad "bottom")** = faces phone bottom = J1 (Pi header), ground pour
- **Mounting holes** H1-H3 positioned for this orientation
- Board outline: 76.2 x 56.9mm
- Extents: (14.975, 14.435) to (91.225, 71.385)

### Current mounting hole positions
- H1: (23.4, 47.96)
- H2: (82.3, 61.16)
- H3: (87.4, 30.46)

### Current SW1 position
- SW1: (62.4, 31.96) on F.Cu

## Target State (post-flip)

The board physically flips over in the phone. In KiCad terms:

- **B.Cu (KiCad "bottom")** now faces phone top = move all SMD here
- **F.Cu (KiCad "top")** now faces phone bottom = SW1 and J1 go here

| Component | Current Layer | New Layer | Reason |
|-----------|--------------|-----------|--------|
| All SMD (U1-U6, caps, resistors, etc.) | F.Cu | B.Cu | Faces phone top (new orientation) |
| SW1 (hook switch) | F.Cu | F.Cu (stays) | Faces phone bottom (mechanism side) |
| J1 (2x20 Pi header) | B.Cu | F.Cu | Pi plugs in from phone bottom; J1 must face down |

## Mounting Hole Recalculation

When the board flips, the screw posts in the phone body stay fixed. The board's hole positions mirror along the flip axis.

**Confirmed: Flip A -- horizontal axis (top-bottom mirror).** X coordinates stay, Y coordinates mirror. y_new = 85.82 - y_old. Verified from phone body photos: three screw posts are symmetric left-to-right, board flips so top edge swaps with bottom edge.

### New positions after flip:
- H1: (23.4, 85.82 - 47.96) = **(23.4, 37.86)**
- H2: (82.3, 85.82 - 61.16) = **(82.3, 24.66)**
- H3: (87.4, 85.82 - 30.46) = **(87.4, 55.36)**
- SW1: (62.4, 85.82 - 31.96) = **(62.4, 53.86)**

**Justin to verify** these calculated positions against the physical phone body before implementation.

## Implementation Steps

### Phase 1: Preparation (no KiCad changes)
- [x] 1. Justin confirms flip axis (horizontal or vertical edge) -- **Flip A confirmed**
- [ ] 2. Update board outline and mounting holes to new positions in KiCad (holes only, no components yet)
- [ ] 3. Export 1:1 scale PDF of board outline + mounting holes (Edge.Cuts + drill marks only). Print on paper, cut out, and physically test-fit against the phone body to verify hole alignment before any component work
- [ ] 4. Justin confirms SW1 position after flip still aligns with hook mechanism

### Phase 2: Component Layer Changes
- [ ] 5. Move all SMD footprints from F.Cu to B.Cu (select all, flip)
- [ ] 6. Move SW1 back to F.Cu (it should stay on the phone-bottom side)
- [ ] 7. Move J1 from B.Cu to F.Cu (Pi needs to plug in from phone-bottom)

### Phase 3: Re-route
- [ ] 8. Delete all traces (copper pour stays, zones stay)
- [ ] 9. Re-route all traces for new component positions
- [ ] 10. Refill zones
- [ ] 11. DRC -- target 0 errors, 0 warnings, 0 unconnected

### Phase 4: Verify
- [ ] 12. Run ERC (schematic unchanged, should still pass)
- [ ] 13. Visual inspection of board views (F.Cu and B.Cu)
- [ ] 14. Verify J1 pin 1 orientation matches Pi header
- [ ] 15. Verify SW1 actuator aligns with hook mechanism

## What Does NOT Change

- **Schematic** -- no changes at all. Netlist is identical.
- **Component selection** -- same BOM, same LCSC part numbers
- **Board outline** -- same 76.2 x 56.9mm rectangle
- **Net connections** -- same signals, same pins
- **Ground pour** -- still GND on both layers
- **COMPONENTS.md / PLAN.md / CODEC-WIRING-REFERENCE.md** -- no changes needed (these describe the schematic/electrical design, not physical placement)

## Risks

1. **J1 pin orientation:** When J1 flips from B.Cu to F.Cu, pin 1 might end up on the wrong side. Need to verify pin 1 position matches the Pi's header. May need to rotate J1 180 degrees after the flip.

2. **SW1 actuation direction:** The tact switch button faces a specific direction. After the flip, verify the button still presses correctly against the hook mechanism.

3. **Silkscreen:** All ref des labels will need to move to the correct silkscreen layer (B.Silkscreen for B.Cu components, F.Silkscreen for F.Cu components).

4. **Assembly:** JLCPCB SMT assembly supports single-side or dual-side. With components on B.Cu and two THT parts on F.Cu (SW1, J1), JLCPCB assembles the B.Cu SMD parts and we hand-solder SW1 and J1. Verify JLCPCB's assembly side selection supports this.
