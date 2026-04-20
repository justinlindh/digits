# Digits PCB v2.1

Carrier board that sits under a Raspberry Pi Zero 2 W inside a gutted vintage desk phone. Provides power (12 V → 5 V → 3.3 V), onboard audio codec for handset mic + earpiece, an RP2040 microcontroller that runs keypad scanning + hookswitch + bell ringer, and all the mechanical/electrical interfaces to the phone shell.

This directory is the source of truth for PCB v2.1. Schematic, footprint placement, and design notes all live here.

V2.1 is a minor revision of V2. The only electrical change is the J8 handset connector pin assignment, which now matches the stock Sangyn Retro 2500 cable directly (no per-unit adapter rework). See `CHANGES_FROM_V2.md` and `hardware/pcb/v2/ERRATA.md` for background.

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
hardware/pcb/v2.1/
├── README.md                   # this file
├── CHANGES_FROM_V2.md          # what changed between V2 and V2.1
├── NET_TOPOLOGY.md             # net-by-net wiring description with citations
├── COMPONENTS.md               # per-component catalogue
├── codec-module-spec.md        # TLV320AIC3104 codec sheet spec
├── ringer-module-spec.md       # DRV8871 ringer sheet spec
├── SOFTWARE_CONFIG.md          # Pi-side config needed for bring-up
├── kicad/
│   ├── digits-pcb.kicad_pro    # KiCad project file
│   ├── digits-pcb.kicad_sch    # root schematic
│   ├── codec.kicad_sch         # codec hierarchical sheet
│   ├── ringer.kicad_sch        # ringer hierarchical sheet
│   ├── digits-pcb.kicad_pcb    # PCB layout
│   ├── digits-pcb.kicad_sym    # project-local symbol library
│   └── production/bom.csv      # hand-audited assembly BOM
└── gerber/                     # last-known-good fab output (regenerate before ordering)
```

---

## Validating before a commit

### Schematic ERC

```bash
kicad-cli sch erc --severity-error --exit-code-violations \
  hardware/pcb/v2.1/kicad/digits-pcb.kicad_sch -o /tmp/erc.rpt
```

Must report **0 errors**.

### PCB DRC

```bash
kicad-cli pcb drc --refill-zones \
  hardware/pcb/v2.1/kicad/digits-pcb.kicad_pcb -o /tmp/drc.rpt
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
