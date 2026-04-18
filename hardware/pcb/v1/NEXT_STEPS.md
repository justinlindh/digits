# v1.1 Design — Next Steps

This branch (`pcb-v1.1-design`) is a work-in-progress respin of v1 that adds onboard support for the Codec Zero HAT plugging directly into the carrier (J_CODEC) plus a J11 mic-to-codec jumper. **NOT ready for fabrication.**

## What's been done

### Schematic (ERC clean: 0 errors)
- `J_CODEC` 2x20 male header for Codec Zero HAT (`Conn_02x20_Odd_Even`, footprint `PinHeader_2x20_P2.54mm_Vertical`)
- All 40 pins of J1 (Pi) and J_CODEC tied together pin-for-pin via global labels for full GPIO passthrough — Codec Zero sees the same signals as if it were stacked on a Pi
- `CODEC_MCLK` (J1/J_CODEC pin 7, Pi GPCLK0) wired — required for TLV320AIC3104 master clock
- Standard HAT power: +3V3 (pins 1, 17), +5V (pins 2, 4), GND on all 8 GND pins
- HAT_ID_SD/SC (pins 27/28) wired for boot-time HAT auto-detect
- `J11` 1x2 mic-to-codec jumper added (pin 1 = `MIC_FROM_SW`, pin 2 = `MIC_GND`); mirrors J10 earpiece pattern
- 3 mounting hole symbols (MH1/2/3) added with markup matching v2 (`Reference` MH#, `Value` MountingHole, `attr exclude_from_bom`)
- Schematic ↔ PCB linked via matching UUIDs on the MH symbols

### PCB (DRC clean with `--refill-zones`)
- Mounting holes restored at original v1 positions: MH1 (82.3, 61.16), MH2 (23.4, 47.96), MH3 (87.4, 30.46)
- `/VSW` net renamed from auto-generated `Net-(D1-K)`, traces widened from 0.25mm to 0.75mm (errata #1 PCB-side fix)
- Power netclass pattern updated in `.kicad_pro`: `VSW` → `/VSW` (sheet-prefixed name match)

## What still needs work before fab

### Mechanical (showstopper)
- [ ] **Pi and Codec Zero are too close** — they will physically overlap given the current J1/J_CODEC orientation. Need to relocate one or rotate. This is the main blocker.
- [ ] After mechanical rework, verify hookswitch (SW1) clearance against handset cradle
- [ ] Verify mounting holes still align with phone shell

### Routing
- [ ] J_CODEC is placed but not all 40 pins are routed to J1 yet — need to complete passthrough traces
- [ ] Widen `+3V3` traces — Codec Zero draws 50–150 mA per IQAudIO docs; current 0.25mm is borderline. Add `/+3V3` to Power netclass pattern in `.kicad_pro` (note the slash prefix — same gotcha as `/VSW`).
- [ ] Verify CODEC_MCLK (pin 7 passthrough) is physically routed
- [ ] Verify all 8 GND pins on each connector tie to the GND pour
- [ ] Verify HAT_ID_SD/SC (pins 27/28) routed — required for HAT auto-detect

### Errata follow-ups (from `ERRATA.md`)
- [x] #1 Switching node trace width — APPLIED in PCB (this commit)
- [ ] #2 J1 rotation fix (rotate to 0° to fix odd/even column swap)
- [ ] #3 Reverse polarity protection on 12V input (1N5822 inline with J3)
- [ ] #4 Decoupling cap near Pico VSYS (100nF 0402)
- [ ] #5 J1 pin 4 (+5V redundancy) — tie to +5V rail along with pin 2

### Pre-fab validation
- [ ] Re-export Gerbers (the untracked `gerber-v1.1/` dir is stale from before today's work)
- [ ] Run `kicad-cli pcb drc --severity-error --refill-zones --exit-code-violations` — must report 0 violations and 0 unconnected
- [ ] Per `feedback_pcb_power_integrity_audit` memory: re-verify all netclass patterns against actual net names (look for `/`-prefixed sheet labels) and check IPC-2221 width math for every >0.5A net
