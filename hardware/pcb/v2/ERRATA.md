# PCB V2 Errata and Assembly Notes

Issues discovered after V2 entered fabrication (2026-04-19). These apply to all fabricated V2 boards unless noted otherwise.

---

## Must Fix at Assembly

### 1. J8 pin-to-net assignment collides with stock Sangyn handset cable

The stock Sangyn Retro 2500 handset RJ9 ribbon cable (when terminated into a 4-pin JST ZH via the usual adapter) delivers:

| JST pin | Wire color | Handset function |
|---|---|---|
| 1 | Black | Mic + |
| 2 | Yellow | Mic − |
| 3 | Red | Earpiece |
| 4 | Green | Earpiece |

Mic pair is on the inner-left pins (1, 2). Earpiece pair is on the inner-right pins (3, 4).

V2's J8 (JST ZH 4-pin, footprint `JST_ZH_B4B-ZR-SM4-TF`) assigns:

| Pin | Net |
|---|---|
| 1 | MIC_HOT |
| 2 | EAR_P |
| 3 | EAR_N |
| 4 | GND |

Assuming mic pair on **outer** pins (1, 4), not inner. With the stock cable plugged in:

- **J8.1**: Black lands on MIC_HOT. ✅
- **J8.2**: Yellow (Mic−) lands on EAR_P, which routes to the codec's HPLOUT (active driven earpiece output). The mic capsule's return wire is electrically tied to a driven audio signal. When playback is active, the playback signal is injected directly into the mic capsule's ground reference. Mic captures playback + room audio, duplex breaks.
- **J8.3**: Red lands on EAR_N. One earpiece terminal sees HPLCOM. Single-ended drive results. Earpiece plays audibly but at roughly half the BTL amplitude.
- **J8.4**: Green lands on GND. The other earpiece terminal is grounded. Completes the single-ended earpiece path.

**Symptom:** mic captures alone (quietly) work; earpiece playback works (reduced volume); full-duplex audio (real calls, audio test) puts playback directly into the mic path, producing noise on capture.

**Root cause:** schematic author assumed mic pair was on the outer pin positions (1, 4) of the handset cable. Actual Sangyn cable puts mic pair on the inner-left pair (1, 2). Not caught by any ERC rule.

**Workaround for fabricated boards:** do not use the stock cable-to-JST adapter as-is. Build the adapter so that pins 2 and 4 at the JST end are swapped relative to the stock handset cable, giving this order:

| JST pin | Wire | V2 J8 signal |
|---|---|---|
| 1 | Black (Mic +) | MIC_HOT |
| 2 | Green (Earpiece) | EAR_P |
| 3 | Red (Earpiece) | EAR_N |
| 4 | Yellow (Mic −) | GND |

Easiest form: cut the stock handset cable, strip the four conductors, crimp JST ZH pins, insert into the housing in the order above. One minor per-unit rework.

**Fix for V3:** update J8 pin assignment in the schematic so `pin 1 = MIC_HOT, pin 2 = GND, pin 3 = EAR_P, pin 4 = EAR_N`, matching the stock Sangyn cable directly. Then no per-unit adapter rework is needed. Add a schematic-review checklist item: for every connector that mates to an external cable with a documented wire color code, the schematic must explicitly assert the color-to-function map, not silently adopt a convention.

### 2. Components face the wrong way relative to mounting holes

Boards arrived with components on the side that ends up facing *down* into the phone shell when the mounting holes are aligned to their bosses. Intended orientation is components facing *up* (away from the shell base, toward the Pi piggybacked on top).

**Symptom:** with the carrier oriented so MH1/MH2/MH3 line up with the enclosure standoffs, every populated component (Pi header, JSTs, LM2596, codec, etc.) is on the underside, sandwiched between the carrier and the shell. Pi header is unreachable from above.

**Root cause:** during placement, the "front" copper layer was used for the active components but the mounting hole positions were defined relative to a board orientation that flips when the assembly is dropped into the enclosure. The fix is one of:

- Move every component to `B.Cu` (mirror the layout), keeping mounting holes where they are; or
- Mirror the mounting hole positions across the board's Y-axis so they line up with the bosses with the existing component side facing up.

Either approach is purely a placement/copper-side change; net topology and routing geometry are preserved.

**Workaround for fabricated boards:** none clean. The board can be powered and tested on the bench with the components facing up (no enclosure), but it cannot be mounted in the phone shell as-is.

**Fix for V2.1 / V3:** flip components to the opposite copper side. Decide whether mounting holes or component side moves; the simpler patch is whichever requires the fewer reroutes. Add a placement-review checklist item: with the board oriented as it will sit in the enclosure (mounting holes aligned to standoffs), confirm components face the intended direction before sign-off.

### 3. Missing pull-up on RP2040 UART TX (GPIO28) net

The RP2040's UART_TX_PI net (RP2040 GP28 to Pi J1.10 / GPIO15) has no external pull-up. Per RP2040 datasheet section 5.2.3.4, every GPIO defaults to input-with-weak-pull-down at reset (50-80 kohm to GND). Until firmware drives GP28 high in `main()`, the line sits near 0 V via that pull-down, and the Pi's RX line is therefore not at UART idle (which is high).

**Symptom on V2 with empty flash:** the Pi's PL011 picks up phantom RX bytes that look like real UART traffic, paced 1:1 with TX bytes, because each TX edge capacitively couples onto the floating RX trace. We initially mis-diagnosed this as a hardware loopback bug; it is not.

**Workaround for fabricated boards:** firmware-side, drive PROTO_UART_TX_PIN HIGH at the very top of `main()` before any other init (done in `firmware/src/main.c::uart_tx_idle_high()`). Eliminates the floating-line window after firmware reaches the chip.

**Fix for V2.1 / V3:** add a 10 kohm pull-up from UART_TX_PI to +3V3 on the carrier. Holds the line at UART idle even when RP2040 is unflashed, held in reset, or in deep sleep. Documents intent in the schematic, so anyone bringing up a board without firmware loaded does not see this confusing symptom.

### 4. PICO_DEFAULT_BOOT_STAGE2 must be boot2_generic_03h, not the SDK default

The Pico SDK ships `boot2_w25q080` as the default boot stage 2 because that matches the Winbond W25Q16/W25Q080 fitted on a genuine Pi Pico. The V2 carrier uses `W25Q16JVSSIQ` (a similar but not identical Winbond part). The w25q080 boot2 issues QSPI-quad continuation commands the JV variant rejects; the chip then fails to enter XIP, watchdog reset fires, and the chip enters a tight reset loop that also keeps SWD silent (cores held in reset every iteration).

**Workaround for fabricated boards:** firmware-side, set `PICO_DEFAULT_BOOT_STAGE2 boot2_generic_03h` for `HARDWARE_REV=2` (done in `firmware/CMakeLists.txt`). XIP throughput drops from ~24 MB/s to ~5 MB/s, but the firmware still fits comfortably in flash and our app is not bandwidth-bound.

**Fix for V2.1 / V3:** pick a flash part the SDK's default boot2 already supports correctly (e.g., the genuine Pi Pico flash), or build a custom boot2 sequence verified against the W25Q16JV datasheet's quad-mode init. Until then, the firmware-side override is sufficient.

### 5. Stale V1 SWD pin assignment in image rootfs-overlay

`pi/image/rootfs-overlay/usr/local/share/digits/swd/digits-swd.cfg` shipped with `bcm2835gpio swd_nums 25 22` (V1 prototype's SWDIO=GP22, SWCLK=GP25). V2 moved SWDIO to GP24 to free GP22 for CODEC_RESET. Until this was caught, openocd bitbanged SWD on the wrong pin (GP22 = CODEC_RESET on V2) and reported "Failed to connect multidrop rp2040.dap0" / "Too long SWD WAIT" forever, presenting as a dead chip.

**Status:** fixed in repo at commit 517af11 ("fix(image): correct SWD pin number for V2 carrier"). All future V2 image builds carry the corrected `swd_nums 25 24`.
