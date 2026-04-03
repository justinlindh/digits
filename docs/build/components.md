# Digits -- Components

## Per Phone

| Component | Notes |
|-----------|-------|
| Sangyn Retro 2500 phone | Donor phone -- gutted, keeping case/handset/keypad/bell |
| RP2040 Pico H | Pre-soldered headers. Firmware handles keypad, hook, bell, tones, LED |
| Raspberry Pi Zero 2 W | Runs digitsd (VoIP stack, signaling, audio) |
| Raspberry Pi Codec Zero (DA7212) | Audio pHAT -- I2S, external mic in (3.5mm TRS), speaker out (screw terminal) |
| V-153-1C25 lever microswitch | Hook switch replacement. SPDT, 51mm lever arm |
| Omron D2F-01F subminiature microswitch | Mic kill switch. Breaks mic line when handset is on cradle |
| L298N H-bridge motor driver | Bell ringer -- alternates coil polarity at 20Hz for AC drive |
| LM2596 buck converter | 12V wall wart down to 5.16V for Pi/Pico power |
| ElectroCookie solderable breadboard | Protoboard for wiring everything together |
| 220 ohm resistor | Status LED current limiter |
| 22 AWG hookup wire | Signal-level GPIO runs |
| Ferrule crimp terminals | Screw terminal connections |
| 6.3mm female spade terminals | Microswitch connections |
| 12V DC wall wart | Power supply |

## Tools

| Tool | Notes |
|------|-------|
| Soldering iron + solder | Protoboard assembly |
| Wire strippers | 22 AWG |
| Multimeter | Continuity checks, voltage verification |
| Ferrule crimper | For screw terminal connections |

See [wiring.md](wiring.md) for the full electrical spec and GPIO map.
