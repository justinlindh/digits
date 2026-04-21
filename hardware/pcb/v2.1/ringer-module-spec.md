# Bell Ringer Module Spec

Scope: self-contained DRV8871 H-bridge driver for the electromechanical
ringer coils of a Western Electric phone handset. Lives in
`hardware/pcb/v2/kicad/ringer.kicad_sch` as a hierarchical sheet of
`digits-pcb.kicad_sch`.

## ⚠️ Hardware architecture warning

`BELL_A` and `BELL_B` on the PCB are **low-voltage** (≤12V peak) H-bridge
outputs. They feed the **primary side** of an **off-board 120V↔12V 10W
mains transformer used in reverse as a 1:10 step-up** (Amazon generic
"12V/10W AC/AC Power Transformer", K-001). The transformer's high-voltage
secondary drives the bell coils externally via wiring harness.

Do NOT wire the bell coils directly across BELL_A/BELL_B on the PCB.
Direct drive at 12V gives a weak buzz instead of a real ring because
Western Electric coils are specified at ~90V RMS, 20Hz. The prototype
hardware used an L298N + the same transformer; v2 swaps the L298N for a
DRV8871 but keeps the transformer external.

## Module interface (6 ports)

| Port | Direction | Description |
|---|---|---|
| `+12V` | in | Post-fuse raw supply (12V nominal from J3 barrel jack) |
| `GND` | in | System ground |
| `RINGER_IN1` | in (digital) | H-bridge input 1, driven by RP2040 `GPIO19` (U3.30) |
| `RINGER_IN2` | in (digital) | H-bridge input 2, driven by RP2040 `GPIO15` (U3.18) |
| `BELL_A` | out (analog) | H-bridge output 1, 0-12V square wave, connects to J7.1 |
| `BELL_B` | out (analog) | H-bridge output 2, 0-12V square wave, connects to J7.2 |

## Internal component inventory (4 components)

| Internal ref | Parent ref | Value | Part | Role |
|---|---|---|---|---|
| U1 | U2 | DRV8871DDAR | SOIC-8-EP | H-bridge driver |
| R1 | R2 | 33 kΩ | 0402 | ILIM: 64 / 33 ≈ 1.94A fault trip (SLVSDA2 §7.3.2 eqn 1) |
| C1 | C54 | 100 nF | 0402 X7R ≥16V | VM HF bypass, ≤3mm from U1.5 |
| C2 | C55 | 47 µF | electrolytic ≥25V | VM bulk reservoir, ≤6mm from U1.5 |

## Firmware drive waveform

Matches the working prototype in `firmware/src/ringer.c`:

- US cadence: 2 s on, 4 s off (6 s cycle).
- Active window: H-bridge polarity flipped every 25 ms (20 Hz square wave).
- Silent window: both inputs low (coast/stop), never DC.

At 20 Hz with a 10W transformer primary (~24Ω DC resistance, ~50-100mH
inductance), the DRV8871 sees roughly 150-400 mA peak primary current
during ringing — well within the 3.6A continuous rating and below the
1.94A ILIM trip.

## Invariants (check_ringer_subsheet.py)

1. Sheet has exactly 6 hierarchical ports: +12V, GND, RINGER_IN1,
   RINGER_IN2, BELL_A, BELL_B.
2. U1 pin map matches datasheet SLVSDA2: 1=GND, 2=IN2, 3=IN1, 4=ILIM,
   5=VM, 6=OUT1, 7=PGND, 8=OUT2, pad=GND.
3. U1.4 connects to R1 pin 1; R1 pin 2 connects to GND; R1 value 33kΩ.
4. C1 (100nF) connects between U1.5 and GND.
5. C2 (≥10µF, nominal 47µF) connects between U1.5 and GND.
6. U1.6 is the sole driver of BELL_A inside the sheet.
7. U1.8 is the sole driver of BELL_B inside the sheet.
8. Sheet has exactly 4 components: U1, R1, C1, C2. No extras.

## References

- DRV8871 datasheet SLVSDA2 §7.3.2 (ILIM formula), §7.3.3 (body-diode
  freewheel, no external flyback needed), §9.2 Fig 11 (typical
  application: 0.1µF + 47µF on VM), §10.1 (layout guidelines).
- Prototype firmware: `firmware/src/ringer.c`, `firmware/src/ringer.h`.
- Parent schematic: `hardware/pcb/v2/kicad/digits-pcb.kicad_sch`.
- Codec pattern this mirrors: `hardware/pcb/v2/codec-module-spec.md`,
  `hardware/pcb/v2/kicad/codec.kicad_sch`.
