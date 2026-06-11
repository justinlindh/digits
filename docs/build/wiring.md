# Digits -- Wiring Reference

Actual wiring as built on the ElectroCookie protoboard (Phase 3 bench integration).
Last verified: 2026-03-22.

**Source of truth:** These pin assignments match the firmware code in `firmware/src/*.h`. If you change pins here, update the firmware `#define` values too.

## Platform

- **Protoboard:** ElectroCookie perfboard. Pico straddling center gap, columns C and H.
  - ⚠️ ElectroCookie has **connected copper strips** on the underside (not isolated pads like a breadboard). Plan solder points accordingly.
- **Power:** 12V 2A wall wart → Wago splitter:
  - Line 1: 12V → LM2596 buck converter → 5.16V → ElectroCookie power rail → Pi + Pico
  - Line 2: 12V → L298N motor driver 12V input (ringer power)
  - **Cable entry:** Power cable routed through former RJ11 port on phone body, knotted inside for strain relief, hot glued in place.
- **Common GND:** All boards share GND via ElectroCookie rail.

## Connectors

All module-to-board connections use JST-XH 2.54mm or DuPont connectors for serviceability. Pin headers are soldered to the ElectroCookie board; crimp housings attach to component-side wires.

| Module          | Connector Type | Pin Count | Notes                                              |
|-----------------|---------------|-----------|-----------------------------------------------------|
| Keypad          | DuPont 8P     | 7 of 8    | 7-pin ribbon cable. Use 8P housing, leave 1 empty. Pin 1 (red stripe) = Col 0. |
| Hook switch     | JST-XH 2A     | 2         | COM + GND. Polarized to prevent reversal.           |
| Mic kill switch | Direct solder  | 2         | D2F-01F COM + NO. Inline on mic hot wire.           |
| Transformer     | JST-XH 2A     | 2         | L298N OUT1/OUT2 to transformer primary. Use pigtails from thick leads. |
| Bell coil       | JST-XH 2A     | 2         | Transformer secondary to bell coil.                 |
| Status LED      | JST-XH 2A     | 2         | GP14 + GND (220Ω resistor on board side).           |
| L298N control   | JST-XH 2A     | 2         | GP11 (IN1) + GP15 (IN2). GND + 12V wired directly. |
| UART (Pico↔Pi)  | JST-XH 3A     | 3         | TX, RX, GND. Already short run, optional.           |
| SWD debug      | JST-SH 1.0mm 3A | 3         | SWDIO, GND, SWCLK. Pi→Pico firmware flash.  |

**Assembly note:** For thick/stiff wires (transformer, bell coil), solder short pigtails of 22-24 AWG wire to the component leads, then crimp JST terminals onto the pigtails. Keeps crimp connections clean and avoids stress on the connector.

## RP2040 Pico GPIO Map

| GPIO | Phys Pin | Function        | Direction | Notes                                        |
|------|----------|-----------------|-----------|----------------------------------------------|
| 0    | 1        | UART0 TX        | OUT       | → Pi GPIO15 (pin 10). Yellow wire.           |
| 1    | 2        | UART0 RX        | IN        | ← Pi GPIO14 (pin 8). Green wire.            |
| 2    | 4        | Keypad Row 0    | OUT       | 1, 2, 3. Active-low scan.                   |
| 3    | 5        | Keypad Row 1    | OUT       | 4, 5, 6                                     |
| 4    | 6        | Keypad Row 2    | OUT       | 7, 8, 9                                     |
| 5    | 7        | Keypad Row 3    | OUT       | *, 0, #                                     |
| 6    | 9        | Keypad Col 0    | IN        | Internal pull-up. ⚠️ Adjacent to pin 8 (GND) -- solder bridge risk. |
| 7    | 10       | Keypad Col 1    | IN        | Internal pull-up.                            |
| 8    | 11       | Keypad Col 2    | IN        | Internal pull-up.                            |
| 9    | 12       | Keypad Col 3    | IN        | Internal pull-up. Unused (4×3 keypad).       |
| 10   | 14       | Hook switch     | IN        | Internal pull-up. HIGH = off-hook, LOW = on-hook. |
| 11   | 15       | Ringer IN1      | OUT       | → L298N IN1 input.                          |
| 12   | 16       | *(unused)*      | --         | Was Tone PWM A -- now stubbed (tones on Pi).  |
| 13   | 17       | *(unused)*      | --         | Was Tone PWM B -- now stubbed. **NOT pin 18** (pin 18 = GND). |
| 14   | 19       | Status LED      | OUT       | → 220Ω resistor → red LED → GND rail. ~6-7mA at 3.3V. |
| 15   | 20       | Ringer IN2      | OUT       | → L298N IN2 input.                          |

**Pin 18 is GND, not GP13.** GP13 = pin 17, GP14 = pin 19. Triple-check against the official Pico pinout.

### Keypad Matrix

```
             Col 0    Col 1    Col 2
             (GP6)    (GP7)    (GP8)
               │        │        │
Row 0 (GP2) ──[1]──────[2]──────[3]
               │        │        │
Row 1 (GP3) ──[4]──────[5]──────[6]
               │        │        │
Row 2 (GP4) ──[7]──────[8]──────[9]
               │        │        │
Row 3 (GP5) ──[*]──────[0]──────[#]
```

Sangyn 2500 phone: 4×3 matrix, 7-pin ribbon cable.

**Ribbon pin mapping (ribbon pin → GPIO → physical pin):**

| Ribbon Pin | Function | GPIO | Phys Pin |
|------------|----------|------|----------|
| 7          | Row 0    | GP2  | 4        |
| 6          | Row 1    | GP3  | 5        |
| 5          | Row 2    | GP4  | 6        |
| 4          | Row 3    | GP5  | 7        |
| 1 (red stripe) | Col 0 | GP6 | 9       |
| 2          | Col 1    | GP7  | 10       |
| 3          | Col 2    | GP8  | 11       |

## Hook Switch

- **Component:** V-153-1C25 microswitch
- **Wiring:** COM → GP10 (pin 14), terminal → GND rail
- **Logic:** Lever pressed by phone cradle = LOW = on-hook. Handset lifted = HIGH (pull-up) = off-hook.
- **Debounce:** 50ms in firmware (`hook.c`)
- **Mounting:** Super glued into phone body cradle position

## Mic Kill Switch (Hardware Privacy)

- **Component:** Omron D2F-01F subminiature microswitch (SPDT, pin plunger, ~13x6x7mm)
- **Purpose:** Physically breaks the mic signal path when the handset is on the cradle. Hardware privacy guarantee.
- **Wiring:** COM ← mic hot wire from RJ9, NO → mic hot wire to TRS plug, NC → floating
- **Logic:** Plunger pressed by cradle = COM-NC (dead end) = mic disconnected. Plunger released = COM-NO = mic connected.
- **Mounting:** Super glued next to V-153-1C25, same cradle mechanism presses both switches.
- **No firmware changes.** Purely physical break in mic signal path.
- See `docs/hardware-kill-switch.md` for full design rationale.

## UART Connection (Pico ↔ Pi)

```
    Pico                          Pi Zero 2 W
    ┌──────────┐                  ┌──────────────┐
    │ GP0 (TX) ├───── yellow ────►│ GPIO15 (RX)  │  pin 10
    │ GP1 (RX) ├───── green  ◄───│ GPIO14 (TX)  │  pin 8
    │ GND      ├───── black  ────│ GND          │
    └──────────┘                  └──────────────┘
```

- **Baud rate:** 115200, 8N1
- **Level:** 3.3V native on both sides -- no level shifter needed
- **Line endings:** Pico requires `\r\n`

## SWD Connection (Pi → Pico, for firmware flashing)

```
    Pi Zero 2 W                    Pico H (JST-SH debug header)
    ┌──────────────┐               ┌──────────────┐
    │ GPIO22       ├─── orange ───►│ SWDIO (pin 1)│
    │ GND (pin 20) ├─── black  ───│ GND   (pin 2)│
    │ GPIO25       ├─── white  ───►│ SWCLK (pin 3)│
    └──────────────┘               └──────────────┘
```

- **Connector:** Pico H uses JST-SH 1.0mm 3-pin (NOT JST-XH 2.54mm)
- **Purpose:** Allows Pi to flash Pico firmware via OpenOCD SWD bitbang
- **GPIO conflict note:** Standard SWD config uses GPIO 24 for SWDIO, but Codec Zero hardwires GPIO 23/24 to onboard LEDs. GPIO 22 is used instead.
- **Flash command:** `sudo bash /data/digits/swd/flash-pico.sh /data/digits/firmware.elf`
- **Auto-flash:** digitsd automatically flashes if Pico fails POST and `/data/digits/firmware.elf` exists

## Ringer Circuit

```
    Pico GP11 (pin 15) ────► L298N IN1
    Pico GP15 (pin 20) ────► L298N IN2
    12V (Wago splitter) ───► L298N +12V
    GND ───────────────────► L298N GND

    L298N OUT1 ────► kzfuli 1210KY transformer primary winding (yellow wires)
    L298N OUT2 ────► kzfuli 1210KY transformer primary winding (yellow wires)

    Transformer secondary (red wires) ────► Bell coil (3.91kΩ)
```

- **Drive:** H-bridge alternates polarity at 20Hz (25ms half-period) to generate AC
- **Output:** ~100V AC at secondary (step-up transformer)
- **Wire colors:** Yellow = primary/low-voltage winding (L298N side). Red = secondary/high-voltage winding (bell side). Per Amazon listing: red=120V input, yellow=12V output. Used in reverse as step-up.
- **Cadence:** US standard -- 2s ring, 4s silence (6s cycle)
- **L298N 5V output:** NOT used for logic power (separate buck converter powers everything)

**Previous design (retired):** NOYITO FR120N MOSFET + opto-isolator. Replaced by L298N which generates the AC waveform directly.

## Status LED

```
    Pico GP14 (pin 19) ──► 220Ω resistor ──► Red LED anode ──► LED cathode ──► GND rail
```

- **Current:** ~6-7mA at 3.3V (well within Pico's 16mA per-pin max)
- **Behavior:** OFF = idle/on-hook. ON = off-hook (dial tone, dialing, connected, busy). BLINK = ringing.

## Pi Zero 2 W -- Audio (Codec Zero / DA7212)

The Raspberry Pi Codec Zero HAT sits on top of the Pi via the 40-pin GPIO header. It uses I2C + I2S internally -- no additional wiring needed for the HAT itself.

| Pi GPIO | Phys Pin | Function   | Notes                          |
|---------|----------|------------|--------------------------------|
| 2       | 3        | I2C SDA    | DA7212 control                 |
| 3       | 5        | I2C SCL    | DA7212 control                 |
| 18      | 12       | I2S BCLK   | Bit clock                      |
| 19      | 35       | I2S LRCLK  | Word select                    |
| 20      | 38       | I2S DOUT   | Playback (Pi → DA7212 → ear)   |
| 21      | 40       | I2S DIN    | Capture (mic → DA7212 → Pi)    |

### Earpiece

- Earpiece (140Ω) connected to **Lineout** screw terminals on Codec Zero
- ⚠️ NOT the "Speaker" terminals -- those are for 8Ω speakers

### Handset Microphone

- Electret mic in handset → RJ9 cable → splice → **D2F-01F mic kill switch** → TRS 3.5mm plug → Codec Zero 3.5mm mic jack
- D2F-01F breaks the mic hot wire when handset is on cradle (hardware privacy guarantee)
- Codec Zero provides MICBIAS for electret power
- **100nF ceramic bypass cap** soldered at TRS-RJ9 junction (kills 97% ultrasonic noise)
- **CRITICAL:** Keep unshielded cable run from RJ9 splice to TRS plug as short as possible (3-4 inches max). Long runs act as 60Hz EMI antenna.

### Critical ALSA Mixer Settings

These three switches MUST be ON or there is no audio output:

| numid | Control                         | Required |
|-------|---------------------------------|----------|
| 29    | Lineout Switch                  | on       |
| 87    | Mixout Left DAC Left Switch     | on       |
| 94    | Mixout Right DAC Right Switch   | on       |

These three MUST be OFF or audio has fade-in latency:

| numid | Control                    | Required |
|-------|----------------------------|----------|
| 38    | DAC Gain Ramping Switch    | off      |
| 39    | Headphone Gain Ramping Switch | off   |
| 40    | Lineout Gain Ramping Switch | off     |

Mixer state file: `/data/digits_mixer.state`
Restored on boot by `digitsd.service` (its `ExecStartPre` runs `alsactl restore`)

## Power Architecture

```
    12V 2A Wall Wart
          │
          ├──► Wago Splitter
          │        │
          │        ├──► L298N +12V (ringer motor driver)
          │        │
          │        └──► LM2596 Buck Converter
          │                   │
          │                   └──► 5.16V output
          │                          │
          │                          ├──► ElectroCookie power rail
          │                          │       │
          │                          │       ├──► Pico VSYS (pin 39)
          │                          │       └──► Pi 5V (pin 2/4)
          │                          │
          │                          └──► Codec Zero (via Pi header)
          │
          └──► GND (shared across all boards)
```

## Services on Pi

| Service                    | Purpose                                   | Status   |
|----------------------------|-------------------------------------------|----------|
| `digitsd.service`          | Main daemon -- serial, tones, mixer restore, WebRTC, keepalive | Active |
| `digits-ap-check.service` | Boot-time check: AP mode vs normal boot   | Active (oneshot) |
| `digits-ap.service`       | hostapd AP mode (setup only)              | Conditional |
| `digits-dnsmasq-ap.service` | dnsmasq DHCP/DNS for AP mode            | Conditional |
| `digits-setup.service`    | Captive portal web server (setup only)    | Conditional |
| `digits-first-boot.service` | First-boot identity setup: stable hostname from Pi serial, fresh SSH host keys | Conditional (oneshot) |
| `dtmf-uart.service`       | *(DISABLED)* -- replaced by digitsd       | Disabled |
| `digits-dac-keepalive.service` | *(DISABLED)* -- moved into digitsd    | Disabled |

## Lessons Learned

1. **Pin 18 is GND.** GP13 = pin 17, GP14 = pin 19. Verify against official Pico pinout diagram.
2. **ElectroCookie copper strips** run in rows on the underside. Plan pad usage to avoid unintended shorts.
3. **Solder bridge risk** at pin 8 (GND) ↔ pin 9 (GP6). Diagnosed via KEYDUMP showing GP6 stuck LOW at idle.
4. **dmix IPC semaphore is per-UID.** All audio processes must run as the same user, or dmix blocks with Permission denied.
5. **DA7212 gain ramping** adds ~500ms fade-in to every sound. Disable all three ramping switches.
6. **H-bridge off-window:** `stop_hbridge()` must be called unconditionally during silence windows, not conditionally on phase state. DC bias through bell coil causes thermal shutdown and EMI that kills WiFi.
