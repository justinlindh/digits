# Sangyn Retro 2500 — Teardown Notes

**Date:** 2026-03-09 (initial), updated 2026-03-22
**Phone:** Sangyn Retro 2500 (hotel-style desk phone)
**Manufacturer:** Bittel (bittelgroup.com) — hotel phone OEM
**Board ID:** `HA(41)T-25 KB T1 V2.1` (dated 2020.1.15) / Main IC: UTC1062 (TEA1062 clone)
**Photos:** `docs/photos/`

---

## Summary

The Sangyn 2500 is a simple, cheap POTS phone with no surprises. All components are passive or basic through-hole — ideal for gutting and replacing with our Pico + Pi Zero stack. No SMD, no scanning ICs, no active electronics in the handset. Original PCB removed and set aside.

---

## Component Findings

### Keypad ✅ FULLY MAPPED & VERIFIED

- **Type:** Standard 4×3 passive matrix (1-9, *, 0, #)
- **Construction:** Silicone dome membrane over green PCB with interleaved finger traces
- **No scanning IC** — pure passive matrix, conductive rubber dome bridges traces on press
- **Connector:** FFC/FPC ribbon cable ("PX" header), 7 data lines (4 rows + 3 columns). Col 3 (GP9) unused — no A/B/C/D column.
- **Matrix map:** ✅ Fully traced with multimeter. All 12 keys mapped to wire→row/col→key.
- **Wiring:** ✅ Ribbon cable soldered to DuPont jumpers. All 12 keys verified on both Pico USB serial AND Pi UART.
- **Interface:** 7 wires to Pico GPIO2-8.

### Hook Switch ✅ VERIFIED

- **Original switch:** SPST tact switch, was soldered to main PCB. **Destroyed during desoldering** — plastic body melted/separated. Not reusable.
- **Replacement:** V-153-1C25 snap-action lever microswitch (SPDT, 51mm straight hinge arm). Mounted on chassis wall beside lever path — long arm intercepts cradle lever sweep.
- **Lever mechanism:** Internal dark gray plastic lever arm with cylindrical post at bottom. Pivots when cradle is depressed by handset weight.
- **Electrical verification:** ✅ HOOK:ON/HOOK:OFF confirmed on Pi. Firmware expects LOW = off-hook with internal pull-up — verified working.
- **Wired to:** Pico GP10 + GND via pre-wired 6.3mm female spade terminals.

### Handset (Speaker + Mic) ✅ MEASURED & VERIFIED

- **Earpiece:** Dynamic receiver, 140Ω impedance (measured). Connected to Codec Zero SPKR OUT screw terminals (Lineout). Verified working — tones and RNNoise-denoised audio play through earpiece.
- **Microphone:** Electret condenser mic element. Connected via RJ9 → coiled cord → TRS 3.5mm splice → Codec Zero mic jack. MICBIAS auto-provided by Codec Zero.
- **RJ9 pinout (clip facing you, L→R):** Pin 1 (black)=Mic+, Pin 2 (red)=Earpiece, Pin 3 (green)=Earpiece, Pin 4 (yellow)=Mic-. Earpiece=middle two, Mic=outer two.
- **Handset housing:** Caps unscrew (earpiece and mouthpiece caps twist off counterclockwise).
- **Volume switch:** 3-position volume control (series resistance), NOT mute.
- **No active electronics** in handset.

### Bell Ringer ✅ MEASURED & VERIFIED

- **Type:** Dual mechanical bells (classic phone ringer)
- **Coil resistance:** 3.91kΩ (measured)
- **Drive:** L298N H-bridge → kzfuli 1210KY step-up transformer (12V → ~100V AC at 20Hz) → bell coil
- **Verified working:** ✅ Bell rings with correct US cadence (2s on, 4s off) via Pico GP11 control
- **Original MOSFET/optocoupler plan RETIRED** — L298N generates the AC waveform needed for the bell coil via transformer. Direct DC MOSFET switching can't ring a phone bell.

### Main PCB ✅ REMOVED

- **Components:** Relay (blue, ringer circuit), 2× DIP/SOP ICs (DTMF generator UTC1062 + line interface), through-hole passives
- **Construction:** Single-sided through-hole, no SMD
- **Status:** ✅ Removed and set aside for reference. All functions replaced by Pico + Pi stack.

### Indicator LED ✅ VERIFIED

- LED connected and functioning on breadboard. Wired to Pico GP14 through 220Ω resistor. Shows off/on/blink modes.

---

## Mounting & Power

- **ElectroCookie protoboard** used for permanent mounting (replaced breadboard).
- **Pico soldered on ElectroCookie**, power from rail, UART wired to Pi.
- **Power distribution:** 12V wall wart → Wago 12V splitter → LM2596 buck converter (5.16V output) → ElectroCookie rail → Pi Zero + Pico. Single power source, no USB needed.
- **L298N 12V feed:** From Wago 12V splitter (not yet wired to ElectroCookie).

---

## Outstanding Physical Work

- [ ] Wire keypad (7 wires) from DuPont jumpers to ElectroCookie — currently disconnected since breadboard abandoned
- [ ] Wire hook switch (2 wires) to ElectroCookie
- [ ] Wire L298N signal lines (2 wires) to ElectroCookie
- [ ] Wire LED + resistor to ElectroCookie
- [ ] Wire L298N 12V feed from Wago splitter
- [ ] Final chassis fit and closure

---

## Decisions Made

1. **Gut the PCB** — removed entirely, wire components directly to Pico/Pi.
2. **Reuse handset speaker + mic** — earpiece (140Ω) to Codec Zero Lineout, electret mic to Codec Zero 3.5mm jack via TRS splice.
3. **Keypad is pure matrix** — no IC bypass needed. Direct GPIO scan from Pico.
4. **Hook switch replacement** — original destroyed during desolder. V-153-1C25 lever microswitch mounted on chassis wall.
5. **Codec Zero** replaces WM8960 HAT ($60 lesson learned — MEMS mics hardwired, no external mic input).
6. **L298N + transformer** for bell ringer (not MOSFET DC switching).
7. **ElectroCookie protoboard** for permanent component mounting (not breadboard).
8. **Single 12V supply** for entire system; LM2596 buck for 5V.
9. **RNNoise** for real-time noise suppression (cross-compiled static lib, verified real-time capable on Pi Zero 2 W).
