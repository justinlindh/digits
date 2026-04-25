# PCB V2 Errata and Assembly Notes

Issues discovered after V2 entered fabrication (2026-04-19). These apply to all fabricated V2 boards unless noted otherwise.

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

**Fix for V2.1 / V3:** flip components to the opposite copper side. Decide whether mounting holes or component side moves; the simpler patch is whichever requires the fewer reroutes. Add a placement-review checklist item: with the board oriented as it will sit in the enclosure (mounting holes aligned to standoffs), confirm components face the intended direction before sign-off.

### 2. Missing pull-up on RP2040 UART TX (GPIO28) net

The RP2040's UART_TX_PI net (RP2040 GP28 to Pi J1.10 / GPIO15) has no external pull-up. Per RP2040 datasheet section 5.2.3.4, every GPIO defaults to input-with-weak-pull-down at reset (50-80 kohm to GND). Until firmware drives GP28 high in `main()`, the line sits near 0 V via that pull-down, and the Pi's RX line is therefore not at UART idle (which is high).

**Symptom on V2 with empty flash:** the Pi's PL011 picks up phantom RX bytes that look like real UART traffic, paced 1:1 with TX bytes, because each TX edge capacitively couples onto the floating RX trace. We initially mis-diagnosed this as a hardware loopback bug; it is not.

**Workaround for fabricated boards:** firmware-side, drive PROTO_UART_TX_PIN HIGH at the very top of `main()` before any other init (done in `firmware/src/main.c::uart_tx_idle_high()`). Eliminates the floating-line window after firmware reaches the chip.

**Fix for V2.1 / V3:** add a 10 kohm pull-up from UART_TX_PI to +3V3 on the carrier. Holds the line at UART idle even when RP2040 is unflashed, held in reset, or in deep sleep. Documents intent in the schematic, so anyone bringing up a board without firmware loaded does not see this confusing symptom.

### 3. PICO_DEFAULT_BOOT_STAGE2 must be boot2_generic_03h, not the SDK default

The Pico SDK ships `boot2_w25q080` as the default boot stage 2 because that matches the Winbond W25Q16/W25Q080 fitted on a genuine Pi Pico. The V2 carrier uses `W25Q16JVSSIQ` (a similar but not identical Winbond part). The w25q080 boot2 issues QSPI-quad continuation commands the JV variant rejects; the chip then fails to enter XIP, watchdog reset fires, and the chip enters a tight reset loop that also keeps SWD silent (cores held in reset every iteration).

**Workaround for fabricated boards:** firmware-side, set `PICO_DEFAULT_BOOT_STAGE2 boot2_generic_03h` for `HARDWARE_REV=2` (done in `firmware/CMakeLists.txt`). XIP throughput drops from ~24 MB/s to ~5 MB/s, but the firmware still fits comfortably in flash and our app is not bandwidth-bound.

**Fix for V2.1 / V3:** pick a flash part the SDK's default boot2 already supports correctly (e.g., the genuine Pi Pico flash), or build a custom boot2 sequence verified against the W25Q16JV datasheet's quad-mode init. Until then, the firmware-side override is sufficient.

### 4. No BOOTSEL button: first-flash and recovery require a paperclip

V2 has no USB connector and no BOOTSEL button. The only way to flash a virgin RP2040 over SWD, or to recover a unit running firmware that doesn't yet support the soft-reboot REBOOT command, is to manually short the flash chip's CS pin (U4 pin 1, the QSPI_SS net) to GND while powering on. This is genuinely fiddly: the pad is sub-millimeter, U4 sits next to U3 whose pin 1 is the +3.3 V rail (so a slipped wire shorts the supply), and the bootrom samples QSPI_SS in the first ~10 ms after power-on so timing is unforgiving.

During V2 bring-up (2026-04-25) one of these paperclip attempts shorted +3.3 V to GND repeatedly through the wire while the operator was trying to find U4 pin 1 in the cluster of decoupling caps near U3. The Pi USB polyswitch tripped each time and the rails recovered, but on that specific board the RP2040's SWD subsystem stopped responding afterward; the chip still booted and ran firmware, but the DAP would never enumerate again. Effectively a one-way unit, salvageable only for non-firmware-iteration tests.

**Workaround for fabricated boards:** for the first flash on each board, follow the procedure with extreme caution, using C1's negative pad (the largest GND surface on the board, far from any +3.3 V neighbor) as the GND target. Once REBOOT-capable firmware is on, all subsequent flashes work over SSH via reset_usb_boot and no physical access is needed.

**Fix for V2.1 / V3:** add a 6 mm momentary tact switch wired from the QSPI_SS net (U4 pin 1) to GND, located on an accessible board edge. Press during power-on to enter BOOTSEL. Standard Pi Pico practice. Eliminates an entire class of bring-up failure mode.

### 5. Stale V1 SWD pin assignment in image rootfs-overlay

`pi/image/rootfs-overlay/usr/local/share/digits/swd/digits-swd.cfg` shipped with `bcm2835gpio swd_nums 25 22` (V1 prototype's SWDIO=GP22, SWCLK=GP25). V2 moved SWDIO to GP24 to free GP22 for CODEC_RESET. Until this was caught, openocd bitbanged SWD on the wrong pin (GP22 = CODEC_RESET on V2) and reported "Failed to connect multidrop rp2040.dap0" / "Too long SWD WAIT" forever, presenting as a dead chip.

**Status:** fixed in repo at commit 517af11 ("fix(image): correct SWD pin number for V2 carrier"). All future V2 image builds carry the corrected `swd_nums 25 24`.
