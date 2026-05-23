# Bell Ringer Module Spec

Scope: self-contained bell ringer for the electromechanical ringer coils of
a Western Electric phone. Lives in `hardware/pcb/v3/kicad/ringer.kicad_sch`
as a hierarchical sheet of `digits-pcb.kicad_sch`. The sheet contains both an
XL6019 boost converter (U10) that steps +5V up to ~37V (`VBOOST`) and the
DRV8871 H-bridge (U2) that drives the bell from VBOOST.

## Bell drive architecture

The DRV8871 (U2) motor supply (VM, pin 5) is fed from `VBOOST`, an on-board
~37V rail produced by the XL6019 boost converter (U10). The H-bridge outputs
`BELL_A` / `BELL_B` drive the bell coils directly through the off-board
`BELL` connector. There is no transformer, on-board or external, in V3.

V1 and V2 drove the bell through an external 120V:12V mains transformer used
in reverse as a step-up. V3 replaces that with the on-board boost: the bell
mechanically saturates, so ~37V drive reaches comparable loudness (~78 dBA at
33V bench-measured, against ~79 dBA for the transformer) without the bulk,
cost, or harness of a transformer. See `PLANNED.md` for the full bell-drive
decision and rejected alternatives.

### Boost subcircuit (U10 XL6019)

```
+5V ── L10 47uH ── SW_NODE ── D10 SS56 ── VBOOST (~37V)
                      │                       │
                  U10.3 SW                C100 100uF / C101 1uF
                  U10.6 tab (SW_NODE, NOT GND)
U10.2 EN  ← +5V  (enable, tied high for always-on)
U10.4 VIN ← +5V
U10.5 FB  ← FB_NODE
R20 57.6k : VBOOST → FB_NODE
R21 2k    : FB_NODE → GND
Vout = 1.25 * (1 + R20/R21) ~= 37.25V
```

U10's metal tab (pad 6) is on SW_NODE, NOT GND. Wiring it to GND would
dead-short the switch node.

## Module interface (6 ports)

| Port | Direction | Description |
|---|---|---|
| `+5V` | in | Post-fuse +5V rail (from PWR connector via F1). Boost converter input. |
| `GND` | in | System ground |
| `RINGER_IN1` | in (digital) | H-bridge input 1, driven by RP2040 `GPIO19` (U3.30) |
| `RINGER_IN2` | in (digital) | H-bridge input 2, driven by RP2040 `GPIO15` (U3.18) |
| `BELL_A` | out (analog) | H-bridge output 1, ~37V square wave, connects to BELL connector pin 1 |
| `BELL_B` | out (analog) | H-bridge output 2, ~37V square wave, connects to BELL connector pin 2 |

`VBOOST`, `SW_NODE`, and `FB_NODE` are internal to the sheet; they do not
cross the sheet boundary.

The mic-kill function is not part of this sheet. On V3 it is the second pole
of the DPDT cradle switch SW1 in series with the mic line, documented in
`NET_TOPOLOGY.md`.

## Internal component inventory

| Internal ref | Parent ref | Value | Part | LCSC | Role |
|---|---|---|---|---|---|
| U2 | U2 | DRV8871DDAR | SOIC-8-1EP | C75864 | H-bridge driver |
| U10 | U10 | XL6019E1 | TO-263-5 | C73018 | +5V to ~37V boost converter |
| L10 | L10 | 47 uH | IND-SMD 12.3x12.3 | C9906 | Boost inductor |
| D10 | D10 | SS56 | SMA | C65009 | Boost rectifier |
| C100 | C100 | 100 uF / 63V | 10mm dia SMD | C28241 | VBOOST bulk |
| C101 | C101 | 1 uF | 0805 | C105952 | VBOOST HF bypass |
| R20 | R20 | 57.6 kohm | 0402 | C26983 | Boost FB divider top |
| R21 | R21 | 2 kohm | 0402 | C4109 | Boost FB divider bottom |
| R2 | R2 | 33 kohm | 0402 | C25779 | ILIM: 64 / 33 ~= 1.94A fault trip (SLVSCY9B sec 7.3.3 eqn 1) |
| C54 | C54 | 100 nF | 0402 X7R | C307331 | DRV8871 VM (VBOOST) HF bypass, <=3mm from U2.5 |
| C55 | C55 | 10 uF | electrolytic 50V | C116402 | DRV8871 VM (VBOOST) bulk, <=6mm from U2.5 |

## DRV8871 pin map (U2)

| Pin | Name | Net |
|---|---|---|
| 1 | GND | GND |
| 2 | IN2 | RINGER_IN2 |
| 3 | IN1 | RINGER_IN1 |
| 4 | ILIM | to R2, then GND |
| 5 | VM | VBOOST |
| 6 | OUT1 | BELL_A |
| 7 | GND | GND |
| 8 | OUT2 | BELL_B |
| pad | GND | GND |

## Firmware drive waveform

Matches the working prototype in `firmware/src/ringer.c`:

- US cadence: 2 s on, 4 s off (6 s cycle).
- Active window: H-bridge polarity flipped every 25 ms (20 Hz square wave).
- Silent window: both inputs low (coast/stop), never DC.

Driving the bell coils directly from ~37V at 20 Hz, the DRV8871 sees a peak
current set by the coil DC resistance and inductance; R2 sets the ILIM fault
trip at ~1.94A, well above the expected ringing current and below the 3.6A
continuous rating, so it only fires on a fault such as a shorted coil.

## Invariants (check_ringer_subsheet.py)

1. Sheet has exactly 6 hierarchical ports: +5V, GND, RINGER_IN1, RINGER_IN2,
   BELL_A, BELL_B. VBOOST/SW_NODE/FB_NODE are internal and do not cross the
   boundary.
2. U2 pin map matches datasheet: 1=GND, 2=IN2, 3=IN1, 4=ILIM, 5=VM, 6=OUT1,
   7=GND, 8=OUT2, pad=GND.
3. U2.4 connects to R2 pin 1; R2 pin 2 connects to GND; R2 value 33kohm.
4. C54 (100nF) connects between U2.5 (VBOOST) and GND.
5. C55 (>=10uF, nominal 10uF) connects between U2.5 (VBOOST) and GND.
6. U2.6 is the sole driver of BELL_A inside the sheet.
7. U2.8 is the sole driver of BELL_B inside the sheet.
8. Boost topology: U10.2/4 on +5V, U10.3 and tab pad 6 on SW_NODE, U10.5 on
   FB_NODE; L10 between +5V and SW_NODE; D10 anode SW_NODE, cathode VBOOST;
   R20 VBOOST-to-FB_NODE, R21 FB_NODE-to-GND; C100/C101 on VBOOST.
9. Sheet has exactly 11 components: U2, U10, L10, D10, C100, C101, R20, R21,
   R2, C54, C55. No extras.

## References

- DRV8871 datasheet SLVSCY9B sec 7.3.3 (ILIM formula), body-diode freewheel
  (no external flyback needed), typical application (0.1uF + 47uF on VM),
  layout guidelines.
- XL6019 datasheet (XLSEMI): boost topology, feedback reference 1.25V.
- Prototype firmware: `firmware/src/ringer.c`, `firmware/src/ringer.h`.
- Parent schematic: `hardware/pcb/v3/kicad/digits-pcb.kicad_sch`.
- Boost decision and rejected alternatives: `PLANNED.md`.
- Codec pattern this mirrors: `hardware/pcb/v3/codec-module-spec.md`,
  `hardware/pcb/v3/kicad/codec.kicad_sch`.
