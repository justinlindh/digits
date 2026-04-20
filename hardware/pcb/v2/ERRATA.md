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
