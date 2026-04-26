# V3 planning notes

V3 has not been spec'd or fabricated. This file is a parking lot for changes already discussed but not ready to commit to a full revision plan.

V3 inherits everything from V2.1 (see `hardware/pcb/v2.1/CHANGES_FROM_V2.md`) and adds the items below.

## Headline change: PCB-mount mains transformer for the bell ringer

V2 (and V2.1) require an external 120V/12V mains transformer wired between J7 (BELL_A/BELL_B from the DRV8871 H-bridge) and the phone's bell coils. The transformer provides ~10x voltage step-up so the coils see their design ~120V drive. It is bulky, costs ~$14 per unit, and adds two flying-wire pairs to the harness.

V3 replaces the external transformer with a PCB-mount equivalent reflow-soldered to the carrier.

Candidate parts (all PCB-mount, ~10 W, 120V:12V, through-hole or PCB-pin):

- Bourns SCG12-005
- Triad FS12-200
- Hammond 162C12

Roughly 25-30 mm square footprint. Needs a high-voltage zone on the secondary side with proper creepage (2-3 mm spacing for ~120V at 20 Hz). The DRV8871 (U2) drives the transformer primary from BELL_A/BELL_B exactly as today; only the routing past J7 changes. J7 itself can be removed once the transformer is on-board.

Bench-validated alternatives that did NOT make the cut:

- **Direct 12V drive (no transformer):** rings on tested WE bells but at roughly 1% of transformer power; subjectively quieter and dependent on individual coil mechanics.
- **24V supply + direct drive:** ~16x the hammer force of 12V direct, ~4% of transformer power. Cheaper than transformer per unit but requires a 24V wall adapter and audit of input-cap voltage ratings (C1, C55, F1).
- **On-board boost converter + higher-V H-bridge (e.g., 12V→48V boost into a 50V H-bridge):** ~16% of transformer power. Smaller PCB area than mains transformer and no high-voltage zone, but more design work and more SMD parts.

Decision: stick with the transformer for V3 because it matches landline ringer loudness without coil-by-coil tuning. Move the part on-board to eliminate the external wart.

## Other items deferred to V3

These were discussed in the V2 bring-up cycle but not put into V2.1 because they break cable or harness compatibility, and a V3 build implies new harnesses anyway.

- **J3 connector upsize JST ZH → JST PH 2-pin** (V2 ERRATA item 6). ZH is rated 1.0 A per contact and the V2 design sits at the limit during ringer peaks. PH is rated 2.0 A. Footprint and connector body change; new pigtails required.
- **SW2 BOOTSEL tact switch** (V2 ERRATA item 4). 6 mm momentary between QSPI_SS and GND on an accessible board edge. Held during power-on to enter BOOTSEL. Eliminates the paperclip-on-U4-pin-1 procedure that has already cost one V2 unit its SWD subsystem.
- **J9 repinning for SPDT cradle switch + on-board FET mute.** Replace the V2 series-interrupt mic kill switch (which uses a separate physical microswitch wired through J9) with a single SPDT cradle switch shared with the hookswitch. J9 repins as `HOOK_SW / GND / MUTE_DRIVE`, MIC_HOT shorts directly to MIC_FROM_SW on the PCB, and a 2N7002 N-MOSFET (gated by MUTE_DRIVE through a 10 kΩ pull-up) shunts MIC_FROM_SW to GND when on-hook. SW1 (on-board hook tactile) becomes redundant and can be removed. Privacy property survives because MUTE_DRIVE is not connected to any GPIO. Adds two SMD parts; eliminates one external microswitch and simplifies the cradle mechanism to a single SPDT.

## Out of scope for V3 (for now)

- Codec or audio path changes
- Rail architecture changes
- RP2040 or flash subsystem changes beyond what V2.1 already does
- Pi-side interface changes
