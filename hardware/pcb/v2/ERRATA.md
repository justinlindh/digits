# PCB V2 Errata and Assembly Notes

Issues discovered after V2 entered fabrication (2026-04-19). These apply to all fabricated V2 boards unless noted otherwise.

The board-level fixes below are carried into the V3 revision (`hardware/pcb/v3/`, see `CHANGES_FROM_V2.md`). An interim V2.1 revision was considered for some of them but was superseded by V3.

---

## Must Fix at Assembly

### 1. Components face the wrong way relative to mounting holes

Boards arrived with components on the side that ends up facing *down* into the phone shell when the mounting holes are aligned to their bosses. Intended orientation is components facing *up* (away from the shell base, toward the Pi piggybacked on top).

**Symptom:** with the carrier oriented so MH1/MH2/MH3 line up with the enclosure standoffs, every populated component (Pi header, JSTs, LM2596, codec, etc.) is on the underside, sandwiched between the carrier and the shell. Pi header is unreachable from above.

**Root cause:** during placement, the "front" copper layer was used for the active components but the mounting hole positions were defined relative to a board orientation that flips when the assembly is dropped into the enclosure. The fix is one of:

- Move every component to `B.Cu` (mirror the layout), keeping mounting holes where they are; or
- Mirror the mounting hole positions across the board's Y-axis so they line up with the bosses with the existing component side facing up.

Either approach is purely a placement/copper-side change; net topology and routing geometry are preserved.

**Workaround for fabricated boards:** none clean. The board can be powered and tested on the bench with the components facing up (no enclosure), but it cannot be mounted in the phone shell as-is.

**Fix for V3:** flip components to the opposite copper side. Decide whether mounting holes or component side moves; the simpler patch is whichever requires the fewer reroutes. Add a placement-review checklist item: with the board oriented as it will sit in the enclosure (mounting holes aligned to standoffs), confirm components face the intended direction before sign-off.

### 2. Missing pull-up on RP2040 UART TX (GPIO28) net (optional fix)

The RP2040's UART_TX_PI net (RP2040 GP28 to Pi J1.10 / GPIO15) has no external pull-up. Per RP2040 datasheet section 5.2.3.4, every GPIO defaults to input-with-weak-pull-down at reset (50-80 kohm to GND). Until firmware drives GP28 high in `main()`, the line sits near 0 V via that pull-down, and the Pi's RX line is therefore not at UART idle (which is high).

**Symptom on V2 with empty flash:** the Pi's PL011 picks up phantom RX bytes that look like real UART traffic, paced 1:1 with TX bytes, because each TX edge capacitively couples onto the floating RX trace. We initially mis-diagnosed this as a hardware loopback bug; it is not.

**Workaround for fabricated boards:** firmware-side, drive both candidate UART_TX pins HIGH at the very top of `main()` before any other init (done in `firmware/src/main.c::uart_tx_idle_high()`). Eliminates the floating-line window after firmware reaches the chip.

**Status:** firmware-side workaround is permanent in the unified firmware. `main.c` drives both candidate UART_TX pins (GP0 for V1, GP28 for V2) high in the pre-bootstrap window before `board_init()` reads the rev byte. The hardware pullup remains a "nice to have" so the line stays at UART idle even when the RP2040 is unflashed, held in reset, or before the firmware reaches `main()`. The firmware workaround is permanent, so V3 does not add the pullup.

### 3. PICO_DEFAULT_BOOT_STAGE2 must be boot2_generic_03h, not the SDK default

The Pico SDK ships `boot2_w25q080` as the default boot stage 2 because that matches the Winbond W25Q16/W25Q080 fitted on a genuine Pi Pico. The V2 carrier uses `W25Q16JVSSIQ` (a similar but not identical Winbond part). The w25q080 boot2 issues QSPI-quad continuation commands the JV variant rejects; the chip then fails to enter XIP, watchdog reset fires, and the chip enters a tight reset loop that also keeps SWD silent (cores held in reset every iteration).

**Status:** closed by unified firmware. The Pico firmware now uses `boot2_generic_03h` unconditionally for all boards (V1, V2, future). The W25Q16JV flash on V2 carriers (and on the Pi Pico module that V1 PCB uses, as bench-verified during the unified firmware migration) is fully supported. No flash chip swap is needed for V3. See `docs/architecture/unified-firmware.md`.

### 4. No BOOTSEL button: first-flash and recovery require a paperclip

V2 has no USB connector and no BOOTSEL button. The only way to flash a virgin RP2040 over SWD, or to recover a unit running firmware that doesn't yet support the soft-reboot REBOOT command, is to manually short the flash chip's CS pin (U4 pin 1, the QSPI_SS net) to GND while powering on. This is genuinely fiddly: the pad is sub-millimeter, U4 sits next to U3 whose pin 1 is the +3.3 V rail (so a slipped wire shorts the supply), and the bootrom samples QSPI_SS in the first ~10 ms after power-on so timing is unforgiving.

During V2 bring-up (2026-04-25) one of these paperclip attempts shorted +3.3 V to GND repeatedly through the wire while the operator was trying to find U4 pin 1 in the cluster of decoupling caps near U3. The Pi USB polyswitch tripped each time and the rails recovered, but on that specific board the RP2040's SWD subsystem stopped responding afterward; the chip still booted and ran firmware, but the DAP would never enumerate again. Effectively a one-way unit, salvageable only for non-firmware-iteration tests.

**Workaround for fabricated boards:** for the first flash on each board, follow the procedure with extreme caution, using C1's negative pad (the largest GND surface on the board, far from any +3.3 V neighbor) as the GND target. Once REBOOT-capable firmware is on, all subsequent flashes work over SSH via reset_usb_boot and no physical access is needed.

**Fix for V3:** add a 6 mm momentary tact switch wired from the QSPI_SS net (U4 pin 1) to GND, located on an accessible board edge. Press during power-on to enter BOOTSEL. Standard Pi Pico practice. Eliminates an entire class of bring-up failure mode.

### 5. Stale V1 SWD pin assignment in image rootfs-overlay

`pi/image/rootfs-overlay/usr/local/share/digits/swd/digits-swd.cfg` shipped with `bcm2835gpio swd_nums 25 22` (V1 prototype's SWDIO=GP22, SWCLK=GP25). V2 moved SWDIO to GP24 to free GP22 for CODEC_RESET. Until this was caught, openocd bitbanged SWD on the wrong pin (GP22 = CODEC_RESET on V2) and reported "Failed to connect multidrop rp2040.dap0" / "Too long SWD WAIT" forever, presenting as a dead chip.

**Status:** fixed in repo at commit 517af11 ("fix(image): correct SWD pin number for V2 carrier"). All future V2 image builds carry the corrected `swd_nums 25 24`.

### 6. J3 power input connector sized at the limit, not with margin

J3 is a JST ZH 2-pin (`B2B-ZR-SM4-TF`) carrying the entire +12 V input for the board. JST rates ZH contacts at 1.0 A per pin. The actual current profile through J3:

- Continuous (Pi Zero 2 W typical + codec + RP2040 + flash, through the LM2596 buck): ~300 mA at 12 V.
- Worst-case overlap (Pi peak load coinciding with ringer mid-stroke at peak coil current): ~1.07 A at 12 V.

The peak sits right at the connector rating with effectively zero margin. F1 is a 1.5 A PTC, so faults downstream of the fuse are bounded above the connector rating but below the wall supply's 2 A capacity. Normal operation produces only a few °C rise at the contact (40 mW total I²R at 1 A across 20 mΩ contacts), so this is not an immediate failure mode, but it violates the customary 50% derating practice for power connectors.

**Workaround for fabricated boards:** none needed. Build harnesses with quality pre-crimped 28 AWG silicone leads (avoids the dominant failure mode, bad crimps adding series resistance) and mechanically secure the cable so axial strain on the barrel jack does not transfer to the JST contacts. Inspect the connector after the first month of real use; if the housing or wire shows any discoloration, downgrade the supply or upsize the harness gauge.

**Fix for V3:** swap J3 from JST ZH to JST PH 2-pin (`B2B-PH-SM4-TBT` or equivalent, 2.0 mm pitch, rated 2.0 A per contact). Schematic edit plus footprint swap; no other routing changes required. Optionally apply the same swap to J7 (ringer output) for consistency, since J7 carries the same coil current that contributes to the J3 peak.

### 7. J8 pin-to-net assignment collides with stock Sangyn handset cable

The stock Sangyn Retro 2500 handset RJ9 ribbon cable (when terminated into a 4-pin JST ZH) delivers:

| JST pin | Wire color | Handset function |
|---|---|---|
| 1 | Black | Mic + |
| 2 | Yellow | Mic − |
| 3 | Red | Earpiece |
| 4 | Green | Earpiece |

Mic pair on inner-left pins (1, 2); earpiece pair on inner-right pins (3, 4).

V2's J8 assigns: pin 1 = `MIC_HOT`, pin 2 = `EAR_P`, pin 3 = `EAR_N`, pin 4 = `GND`. This assumes mic pair on outer pins (1, 4), not inner. With a stock cable plugged in unmodified:

- **J8.2** (Yellow / Mic−) lands on `EAR_P`, tying the mic capsule's return wire to the codec's HPLOUT driven output. Playback signal injects directly into the mic capsule ground reference. Full-duplex breaks: the mic captures whatever the earpiece is playing.
- **J8.3** (Red / Earpiece) lands on `EAR_N`. Earpiece becomes single-ended drive at roughly half the BTL amplitude.
- **J8.4** (Green / Earpiece) lands on GND. Completes the single-ended earpiece path.

**Symptom:** earpiece-only tests sound fine (reduced volume, hard to notice). Mic-only capture works. Full-duplex calls put playback into the mic, audible as echo or feedback.

**Workaround for fabricated boards:** rebuild the J8 adapter with pins 2 and 4 swapped relative to the stock cable, giving JST pin 1=Black, 2=Green, 3=Red, 4=Yellow.

**Fix for V3:** update J8 pin assignment so pin 1 = `MIC_HOT`, pin 2 = `GND`, pin 3 = `EAR_P`, pin 4 = `EAR_N`. Stock Sangyn cable then plugs in directly with no per-unit adapter rework.

### 8. J6 LED connector polarity is reversed relative to the stock phone LED cable

J6 (2-pin JST ZH) drives the indicator LED in the phone housing. V2 wires:
- `J6.1` → `LED_A` (anode side, GP16 → R1 220Ω → J6.1)
- `J6.2` → `GND` (cathode return)

The stock LED cable that ships in the donor phone is wired with the opposite polarity for these two positions. Plugging it into J6 unmodified leaves the LED reverse-biased, which never lights regardless of firmware state.

**Symptom:** firmware drives the LED net (correctly, after the GP14→GP16 firmware fix in commit fc3f4b62 and the LED_PIN HARDWARE_REV switch from this branch), but the indicator never illuminates with the stock cable installed.

**Workaround for fabricated boards:** rework the J6 cable end so the two crimps are swapped in the JST housing. Pin 1 receives the cathode wire, pin 2 receives the anode wire. Verified working on the bench unit (192.168.2.229).

**Fix for V3:** swap J6.1 and J6.2 net assignments in the schematic so pin 1 = `GND` and pin 2 = `LED_A`. Stock cable then plugs in directly with no per-unit rework. Single-row 2-pin, no other routing changes.

### 9. Firmware HOOK_PIN was hardcoded to GP10, V2 SW1 is on GP20

V2 routes the on-board hookswitch SW1 to U3 pin 31 (GP20). The earlier firmware hardcoded `HOOK_PIN = 10` (V1's pin), so on V2 hardware the FSM read a floating pin and never observed any hook transitions.

**Status:** closed by unified firmware. The Pico firmware now reads `hook_pin` from a runtime board profile populated by `board_init()` from the rev byte at flash 0x101FF000. V1 boards get GP10; V2 boards get GP20. No `HARDWARE_REV` fork. See `docs/architecture/unified-firmware.md`.

**Bring-up note:** for cradle-actuated switches, wire across SW1's two pads (the existing on-board tactile is in parallel and harmless). One pad is HOOK_SW (GP20), the other is GND; switch polarity does not matter for an SPST. If your switch is normally-closed instead of normally-open, set `hook_inverted: true` in the device config.

