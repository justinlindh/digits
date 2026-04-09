# Digits -- UART Protocol Specification

Line-based ASCII protocol between Pico H and Pi Zero 2 W.

## Transport

- **Interface:** UART0 (Pico GP0/GP1) ↔ `/dev/serial0` (Pi)
- **Baud rate:** 115200
- **Format:** 8N1
- **Line terminator:** Pico expects `\r\n` (CRLF) on input, sends `\n` (LF) on output
- **Max line length:** 256 bytes (including terminator)
- **Encoding:** ASCII printable characters only
- **Pi-side service:** `digitsd` owns the serial port. See Hard Rules below.

## Command Format

```
<COMMAND>[:<ARG1>[:<ARG2>...]]\r\n
```

Commands use colon-separated tokens (no spaces). All uppercase.

## Pi → Pico Commands

### Call Control

| Command         | Response       | Description                                       |
|-----------------|----------------|---------------------------------------------------|
| `RING:START`    | `RING:ACK`     | Start ringing (enters RINGING state). Flushes any pending hook event first. |
| `RING:STOP`     | `RING:DONE`    | Stop ringing. Works from any state -- always stops ringer hardware. Cleans up LED. |
| `RING:TEST`     | `RING:TEST:ACK`| **Bypass FSM entirely.** Drives ringer + blinks LED regardless of hook state. Use `RING:STOP` to stop. For bench testing. |
| `TONE:DIAL`     | --              | Play dial tone                                    |
| `TONE:RINGBACK` | --              | Play ringback tone                                |
| `TONE:STOP`     | --              | Stop all tones                                    |

### LED Control

| Command      | Response | Description          |
|--------------|----------|----------------------|
| `LED:ON`     | --        | LED steady on        |
| `LED:OFF`    | --        | LED off              |
| `LED:BLINK`  | --        | LED blinking         |

### Hook Override (Debug)

| Command          | Response              | Description                                     |
|------------------|-----------------------|-------------------------------------------------|
| `HOOK:FORCE:ON`  | `HOOK:FORCED:ON_HOOK` | Override hook to on-hook (handset down). Physical pin ignored. |
| `HOOK:FORCE:OFF` | `HOOK:FORCED:OFF_HOOK`| Override hook to off-hook (handset up). Physical pin ignored. |
| `HOOK:RELEASE`   | `HOOK:RELEASED`       | Clear override, return to physical pin reading.  |
| `HOOK:INVERT:ON` | `HOOK:INVERT:ON`      | Invert hook sense (LOW=off-hook). For PCB carrier boards.    |
| `HOOK:INVERT:OFF`| `HOOK:INVERT:OFF`     | Normal hook sense (HIGH=off-hook). Default for protoboards.  |

### System / Debug

| Command      | Response                  | Description                                                  |
|--------------|---------------------------|--------------------------------------------------------------|
| `PING`       | `PONG`                    | Health check.                                                |
| `RESET`      | `RST:OK`                  | Reset FSM to IDLE, exit keytest mode.                        |
| `STATE?`     | `STATE:<name>` + hook + mode lines | Query current FSM state, hook status, and active modes.  |
| `KEYTEST`    | `MODE:KEYTEST`            | Enter raw keypad test mode. Bypasses FSM, reports all keypresses as `KEY:<char>`. |
| `KEYTEST:OFF`| `MODE:NORMAL`             | Exit keytest mode, return to normal FSM operation.           |
| `KEYDUMP`    | Multi-line GPIO dump      | Raw GPIO state of all keypad row/column pins + active scan results. |

### STATE? Response Format

```
STATE:IDLE
HOOK:ON_HOOK           (or HOOK:OFF_HOOK, HOOK:FORCED:ON_HOOK, HOOK:FORCED:OFF_HOOK)
MODE:KEYTEST           (only if keytest mode is active)
```

## Pico → Pi Messages (Unsolicited)

### Hook Events

| Message    | Description                        |
|------------|------------------------------------|
| `HOOK:OFF` | Handset lifted (off-hook detected) |
| `HOOK:ON`  | Handset replaced (on-hook detected)|

### Keypad Events

| Message       | Description                                          |
|---------------|------------------------------------------------------|
| `KEY:<char>`  | Key pressed. `char` is `0`-`9`, `*`, or `#`. Sent in both normal (DIALING state) and KEYTEST modes. |
| `DIAL:<digits>` | Full number dialed (7 digits collected). Triggers ringback tone. |

### FSM State Changes

| Message        | Description                            |
|----------------|----------------------------------------|
| `FSM:<state>`  | State transition occurred. Printed to USB console and sent over UART. States: `IDLE`, `DIAL_TONE`, `DIALING`, `RINGING`, `CONNECTED`, `BUSY`. |

### Boot

| Message         | Description                |
|-----------------|----------------------------|
| `STATUS:READY`  | Firmware initialized, POST complete. |

## USB Console

The USB serial console (CDC) provides the same command interface as UART. Type commands directly; they're injected into the UART command handler. The console provides:

- Command echo + prompt (`> `)
- Backspace support
- Banner on connect with available commands

## FSM State Machine

```
IDLE ──(off-hook)──→ DIAL_TONE ──(keypress)──→ DIALING ──(7 digits)──→ [ringback]
  ↑                                               │ (timeout)
  │                                               ↓
  │                                             BUSY
  │
  ├──(RING:START)──→ RINGING ──(off-hook)──→ CONNECTED
  │                     │
  │←──(RING:STOP)───────┘
  │
  │←──(on-hook from any state)
```

- **On-hook from any non-IDLE/non-RINGING state → IDLE**
- **RINGING + off-hook → CONNECTED** (answering the call)
- **Dial timeout:** 30s for bench testing (configurable via `DIAL_TIMEOUT_MS`)

## Hard Rules

1. **Never steal the serial port from `digitsd`.** The daemon owns `/dev/serial0`. Use `journalctl -u digitsd` to monitor.
2. **To send a debug command:** Stop the service (`systemctl stop digitsd`), send command, read response, restart immediately.
3. **UART line endings:** Pico requires `\r\n`. Bash `printf 'PING\n'` won't work. Use `printf 'PING\r\n'` or a serial tool.

## Flow Examples

### Normal Outgoing Call
```
Pico→Pi: HOOK:OFF
         FSM:DIAL_TONE
Pico→Pi: KEY:5
         FSM:DIALING
Pico→Pi: KEY:5
Pico→Pi: KEY:5
Pico→Pi: KEY:1
Pico→Pi: KEY:2
Pico→Pi: KEY:3
Pico→Pi: KEY:4
Pico→Pi: DIAL:5551234
(Pi initiates call, Pico plays ringback)
Pico→Pi: HOOK:ON
         FSM:IDLE
```

### Incoming Call (Normal)
```
Pi→Pico: HOOK:FORCE:ON        (ensure on-hook state)
         HOOK:FORCED:ON_HOOK
Pi→Pico: RING:START
         RING:ACK
         FSM:RINGING
(user picks up handset)
Pico→Pi: HOOK:OFF
         FSM:CONNECTED
```

### Bench Test Ring (RING:TEST)
```
Pi→Pico: RING:TEST
         RING:TEST:ACK
(bell rings, LED blinks -- hook state irrelevant)
Pi→Pico: RING:STOP
         RING:DONE
```
