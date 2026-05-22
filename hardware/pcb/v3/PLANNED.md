# V3 design decisions

V3 is the carrier board as built. This file records the design decisions behind the parts of V3 that had real alternatives, so the rationale survives outside the schematic. For the full v2-to-v3 delta see `CHANGES_FROM_V2.md`; for the per-component and per-net detail see `COMPONENTS.md` and `NET_TOPOLOGY.md`.

## Bell drive: on-board XL6019 boost to ~37 V

V1 and V2 ring the bell by driving the DRV8871 H-bridge (U2) into the primary of an external 120V:12V mains transformer used in reverse as a step-up, with the high-voltage secondary on the bell coils. The transformer is bulky, costs ~$14 per unit, and adds two flying-wire pairs to the harness.

V3 removes the external transformer. The DRV8871 motor supply (VM, U2 pin 5) is fed from `VBOOST`, an on-board rail produced by an XL6019 boost converter (U10, TO-263-5). The boost steps the +5 V rail up to ~37 V. Output is set by the feedback divider R20 = 57.6 kΩ (VBOOST to FB) and R21 = 2 kΩ (FB to GND): Vout = 1.25 * (1 + R20/R21) ~= 37.25 V. The chain is +5V -> L10 (47 µH) -> SW_NODE -> D10 (SS56 Schottky) -> VBOOST, bulked by C100 (100 µF / 63 V) and C101 (1 µF). U10's metal tab (pad 6) is on SW_NODE, not GND.

Bench-validated: ~78 dBA at 33 V is comparable to the ~79 dBA of the transformer. The bell mechanically saturates, so above a threshold loudness barely tracks drive even though hammer power scales with V^2; ~37 V sits past that knee with margin.

### Rejected alternatives

- **On-board mains transformer (Bourns SCG12-005, Triad FS12-200, Hammond 162C12).** Rejected: a ~25-30 mm square part plus a high-voltage zone needing 2-3 mm creepage, all to match a loudness the boost already reaches. Largest, costliest, and the only option that puts mains-class voltage on the board.
- **24 V supply + direct drive.** Rejected: needs a 24 V wall adapter and re-rating of input caps and fuse, and still leaves the bell well below saturation loudness.
- **12 V supply + direct drive.** Rejected: rings the tested WE bell but at a fraction of saturated loudness; subjectively a weak buzz, and dependent on individual coil mechanics.

## Hook and mic-kill: single DPDT cradle switch (SW1)

V2 used a separate 6 mm tactile hookswitch (SW1) for hook sense and a separate physical microswitch wired through the J9 connector for mic kill. V3 collapses both into one 6-pin DPDT telephone hook switch (SW1, custom footprint `SW_DPDT_Hook_24.2x17.1mm`) that presses the cradle plunger.

- Pole 1 (hook sense): common pin 2 = `HOOK_SW` switches between pin 3 = GND and pin 1 (unused). On-hook grounds HOOK_SW; off-hook opens it and the RP2040 internal pull-up reads high.
- Pole 2 (mic kill): common pin 5 = `MIC_HOT` switches between pin 4 = `MIC_FROM_SW` and pin 6 (unused). On-hook breaks the mic path in series, so the mic is dead on the cradle. Privacy is a hardware property: no GPIO can override it.

This retires both V2's tactile SW1 and the J9 mic-kill connector.

### Rejected alternative

- **SPDT cradle switch + on-board 2N7002 FET mute via a repinned J9.** Rejected: the DPDT series-interrupt is simpler, uses no active parts, and keeps the mic-mute property purely passive. The FET approach added two SMD parts and a gate pull-up to do what one extra switch pole does directly.

## BOOTSEL: SW2 tact switch

V3 adds SW2, a 6 mm momentary tact switch between `QSPI_SS` and GND. Hold it during power-on to enter the RP2040 bootrom. This eliminates the V2 procedure of shorting U4 pin 1 with a paperclip, which has already cost one V2 unit its SWD subsystem.

## Power input and rail architecture

V3 input is +5 V, not 12 V. The LM2596 buck stage (U1 + L1 + D1 + C2) is removed. The input connector is `PWR` (JST XH B2B-XH-A, 2.5 mm pitch, ~3 A), upsized from V2's JST ZH which sat at its 1 A contact limit during ringer peaks. The path is `PWR` -> `/VIN_RAW` -> F1 (1.5 A PTC) -> +5V. The +3V3 rail still comes from U5 (AMS1117-3.3) off +5 V; the codec's +1V8 still comes from U7 (XC6206P182MR) off +3V3.
