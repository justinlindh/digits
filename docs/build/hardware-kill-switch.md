# Hardware Mic Kill Switch

## Goal
When the handset is on the cradle, the microphone must be **physically incapable** of transmitting audio. Not software mute. A hardware break in the electrical circuit. This is a core privacy guarantee for a device that sits in a child's bedroom.

## Design (Decided 2026-04-02)

**Omron D2F-01F subminiature microswitch** wired inline on the mic hot wire.

### Component
- **Part:** Omron D2F-01F (SPDT, pin plunger, no lever)
- **Body:** ~13x6x7mm
- **Contact resistance:** ~100 milliohms (electrically invisible in the high-impedance mic path)
- **Actuation:** ~0.74N, ~0.5mm travel

### Wiring
```
Handset electret mic
        │
     RJ9 cable (mic hot wire)
        │
        ▼
   D2F-01F COM ◄── mic hot from RJ9
   D2F-01F NO  ──► mic hot to TRS plug (with 100nF cap at TRS end)
   D2F-01F NC  ──► (floating, unconnected)
        │
        ▼
   TRS 3.5mm plug ──► Codec Zero mic jack
```

### Behavior
- **Handset on cradle** (plunger pressed by cradle mechanism): COM connects to NC (dead end). Mic line is physically broken. No audio path exists.
- **Handset lifted** (plunger released): COM connects to NO. Mic hot wire flows through to Codec Zero. Audio works normally.

### Mounting
- Super glue the D2F-01F to the phone body cradle area, positioned so the cradle mechanism presses the plunger alongside the existing V-153-1C25 hook switch.
- The D2F-01F is small enough to fit almost anywhere in the phone body.
- Solder directly to switch terminals (no crimp connectors) for clean signal path.

### Signal Integrity
- 100 milliohms contact resistance in a ~2.2k impedance path is negligible.
- The 100nF bypass cap remains at the TRS end, downstream of the switch. EMI filtering unchanged.
- Extra wire length through switch body is ~10mm. The existing unshielded run is 3-4 inches. No measurable change in EMI pickup.
- No microphonics risk: snap-action contacts are spring-loaded tight once closed, no chatter during calls.
- No firmware changes required. Purely physical break in mic signal path.
- Codec Zero MICBIAS powers the electret instantly when the line connects.

### Why Not Reuse the Existing V-153-1C25?
The V-153-1C25 is SPDT with a shared Common terminal. GP10's internal pull-up sources 3.3V through ~50k. If the mic hot wire shared COM, the 3.3V pull-up would swamp the millivolt mic signal when off-hook (COM-NC closed). Two independent signals cannot share COM on an SPDT switch.

### Why Not a Second V-153-1C25?
The V-153-1C25 has a 51mm lever arm. Two of them would be difficult to fit inside the phone body. The D2F-01F is ~1/10th the size and actuates with a pin plunger, making placement trivial.

## FAQ

### Why not route the mic through the existing hook switch instead of adding a second switch?

The V-153-1C25 hook switch is SPDT with a single Common terminal. COM is wired to GP10, where the Pico H's internal pull-up sources 3.3V through ~50k ohms to read hook state. If the mic hot wire shared that same COM terminal, the 3.3V pull-up would swamp the millivolt-level electret mic signal whenever the handset is off-hook (COM-NO closed). You can't split the two signals across NO and NC either, because COM is shared -- both signals would always be on whichever terminal COM is currently connected to.

You would need a DPDT switch (two independent poles, one mechanical actuator) to switch both the GPIO and mic circuits from one cradle action. The D2F-01F is simpler: a dedicated SPDT switch for the mic circuit alone, tiny enough to mount anywhere, with no firmware changes required.
