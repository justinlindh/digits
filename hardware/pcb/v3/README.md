# Digits PCB v3

Carrier board that sits under a Raspberry Pi Zero 2 W inside a gutted vintage desk phone. Takes +5 V in, derives +3.3 V (U5 AMS1117) and +1.8 V (U7 XC6206) for the audio codec, and boosts +5 V up to ~37 V on-board (U10 XL6019) to drive the mechanical bell through the DRV8871 H-bridge. Hosts the TLV320AIC3104 codec for handset mic + earpiece and an RP2040 that runs keypad scanning, hook sense, and bell ringer. A single DPDT cradle switch (SW1) handles both hook sense and series mic-kill.

This directory is the source of truth for PCB v3. Schematic, footprint placement, and design notes all live here.

V3 is a major revision of V2. Headline changes versus V2:

- +5 V input (was 12 V); the LM2596 buck stage is removed. Input connector `PWR` is JST XH (~3 A), upsized from V2's JST ZH.
- On-board XL6019 boost to ~37 V (`VBOOST`) drives the DRV8871 motor supply, replacing V2's external mains step-up transformer.
- Single 6-pin DPDT cradle switch `SW1` does both hook sense and series mic-kill, retiring V2's separate tactile hookswitch and the J9 mic-kill connector.
- `SW2` BOOTSEL tact switch across `QSPI_SS` to `GND`, retiring the paperclip bootstrap that destroyed a V2 RP2040's SWD interface during bring-up.
- Power indicator LEDs D2 (red, +5 V) and D3 (green, +3V3).
- Components flipped to face up so the Pi header is reachable; SW1 is the only back-side part.
- J8 handset and `LED` connector pinouts match the stock cables directly (no per-unit adapter rework).

See `CHANGES_FROM_V2.md` for the full delta, `PLANNED.md` for the bell-drive and mic-kill design rationale, and `hardware/pcb/v2/ERRATA.md` for V2 background.

---

## Sources of truth

When two artefacts disagree, the higher entry in this table wins. Never argue with ERC or DRC; fix the schematic or the doc.

| Rank | Artefact | Authority |
|---|---|---|
| 1 | `kicad/digits-pcb.kicad_sch` | Canonical electrical netlist. If a doc says a pin is wired to X and the schematic says Y, the schematic is correct. |
| 2 | `kicad/digits-pcb.kicad_pcb` | Canonical physical placement and routing. Authoritative for component positions, layer assignments, copper geometry. |
| 3 | `NET_TOPOLOGY.md` | Prose description of *why* every net exists and how every IC is wired. Cite datasheet sections here. |
| 4 | `COMPONENTS.md` | Per-component catalogue: value, package, LCSC part, purpose, and which nets each pin is on. |
| 5 | `codec-module-spec.md`, `ringer-module-spec.md` | Module design specifications for the two hierarchical sheets. |
| 6 | `SOFTWARE_CONFIG.md` | Pi-side configuration required for the board to operate (UART, I²C, I²S, GPCLK0). |

---

## File map

```
hardware/pcb/v3/
├── README.md                   # this file
├── CHANGES_FROM_V2.md          # full v2-to-v3 delta
├── PLANNED.md                  # design decisions and rejected alternatives
├── NET_TOPOLOGY.md             # net-by-net wiring description with citations
├── COMPONENTS.md               # per-component catalogue
├── codec-module-spec.md        # TLV320AIC3104 codec sheet spec
├── ringer-module-spec.md       # DRV8871 + XL6019 ringer sheet spec
├── SOFTWARE_CONFIG.md          # Pi-side config needed for bring-up
├── kicad/
│   ├── digits-pcb.kicad_pro    # KiCad project file
│   ├── digits-pcb.kicad_sch    # root schematic
│   ├── codec.kicad_sch         # codec hierarchical sheet
│   ├── ringer.kicad_sch        # ringer hierarchical sheet
│   ├── digits-pcb.kicad_pcb    # PCB layout
│   ├── digits-pcb.kicad_sym    # project-local symbol library
│   └── production/             # hand-audited assembly BOM and fab output (regenerate before ordering)
```

---

## Validating before a commit

### Schematic ERC

```bash
kicad-cli sch erc --severity-error --exit-code-violations \
  hardware/pcb/v3/kicad/digits-pcb.kicad_sch -o /tmp/erc.rpt
```

Must report **0 errors**.

### PCB DRC

```bash
kicad-cli pcb drc --refill-zones \
  hardware/pcb/v3/kicad/digits-pcb.kicad_pcb -o /tmp/drc.rpt
```

Must report **0 unconnected, 0 clearance, 0 dangling**. `--refill-zones` is mandatory; without it, DRC reads stale zone fills and misses zone-island and same-layer clearance violations.

---

## Do-not-regress invariants

Things we have caught once; regressing any of them is a production defect.

- **Never use `Device:Crystal` (2-pin) with a 4-pad crystal footprint.** The signals are diagonal on the physical part; the 2-pin symbol wires XOUT to a case-GND pad and the crystal never oscillates. Use `Device:Crystal_GND24` (pins 1/3 signal, 2/4 GND).
- **XOUT must pass through R9 (1 kΩ) before reaching Y1.3.** Raspberry Pi *Hardware design with RP2040* §2.3 mandates this for IOVDD = 3.3 V. Do not omit; do not substitute 0 Ω.
- **C5/C6 crystal load caps are 15 pF C0G 0402.** Previous rev had 22 pF which corresponds to no real-world crystal.
- **Y1 is specifically Abracon ABM8-272-T3**, not a generic 12 MHz crystal. RP2040 datasheet §2.16.1.1 says Pico was tuned for this exact part.
- **Six IOVDD caps (C12–C16 + C28), not five.** RP2040 has six IOVDD pins. Per-pin decoupling is mandated.
- **Both DVDD pins get 100 nF** (C29 on pin 23, C30 on pin 50). Plus the 1 µF bulk C10 on VREG_VOUT.
