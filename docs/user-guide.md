# Digits -- Quick Start Guide

**Welcome to your Digits phone.**  
It's a real telephone. It makes real calls. That's it.

---

```
        ╔═══════════════════════════╗
        ║  ┌─────────────────────┐  ║
        ║  │ [1] [2] [3]         │  ║
        ║  │ [4] [5] [6]  ◉ LED  │  ║
        ║  │ [7] [8] [9]         │  ║
        ║  │ [*] [0] [#]         │  ║
        ║  └─────────────────────┘  ║
        ║                           ║
        ╚═══╗               ╔═══════╝
         handset           cradle
```

---

## 1. What's in the Box

- **The phone** -- a vintage-style desk telephone, fully assembled
- **Power adapter** -- 12V DC wall wart (the plug with the barrel connector)
- **Your pairing code** -- printed on the card tucked inside the box; keep it safe

**What you'll also need:**
- A Wi-Fi network with internet access
- A smartphone or laptop (just for the one-time setup)
- Your phone number -- assigned to you when you registered (format: `XXX-XXXX`)

---

## 2. Plugging In and Powering On

1. Place the phone on a flat surface with the handset resting on the cradle.
2. Plug the power adapter into the port on the back of the phone.
3. Plug the other end into a wall outlet.
4. The **red LED** on the front of the phone will blink briefly as the phone boots up.

> **First boot takes about 30–60 seconds.** The phone is starting up its internal computer. Be patient -- you'll know it's ready when the LED goes dark (idle, on-hook) and you hear a faint click from the ringer.

---

## 3. Wi-Fi Setup

The phone needs to know your Wi-Fi password before it can make calls. You'll do this once from your phone or laptop.

### Step 1 -- Connect to the phone's hotspot

Your Digits phone creates its own temporary Wi-Fi network during setup. Look for a network named:

```
Digits-XXXX
```

(The `XXXX` is a unique code printed on the bottom of your phone.)

Connect to it from your smartphone or laptop. **No password is needed to join this network.**

> On most phones, you'll see a notification saying "Sign in to network" or "Captive portal detected." Tap it. If you don't see that, open a browser and go to **http://digits.local** or **http://192.168.4.1**.

### Step 2 -- Enter your Wi-Fi details

A setup page will load automatically. You'll see:

```
┌─────────────────────────────────────┐
│         Digits Phone Setup          │
│                                     │
│  Your network:  [ dropdown ▼ ]      │
│  Wi-Fi password: [______________]   │
│  Pairing code:   [______________]   │
│                                     │
│            [ Connect ]              │
└─────────────────────────────────────┘
```

- **Your network** -- select your home Wi-Fi from the dropdown
- **Wi-Fi password** -- your home Wi-Fi password
- **Pairing code** -- the 6-digit code from the card in the box

Tap **Connect**.

### Step 3 -- Wait for the reboot

The phone saves your settings and reboots automatically. This takes about 30 seconds.

When it's done, reconnect your phone/laptop to your normal Wi-Fi. Your Digits phone will now be online.

> ✅ **Setup is complete when you pick up the handset and hear a dial tone.**

---

## 4. Making Your First Call

1. **Pick up the handset** -- lift it off the cradle
2. **Listen for the dial tone** -- a steady hum means the phone is ready
3. **Dial the 7-digit number** of the person you want to call
   - Example: `271-0001` → dial `2710001`
4. **Wait** -- you'll hear a ringing sound while their phone rings
5. **Talk!** When they pick up, you're connected

> **That's it.** No apps, no logins, no hold music. Just a phone call.

### What the tones mean

| Sound | Meaning |
|-------|---------|
| Steady hum | Dial tone -- ready to dial |
| Ringing (brrring... brrring...) | Their phone is ringing |
| Busy signal (fast beeping) | They're already on a call |
| Silence or error tone | Call couldn't connect -- hang up and try again |

---

## 5. Receiving a Call

1. **The bell rings** -- your phone has a real mechanical bell
2. **Pick up the handset** -- that's it, you're connected
3. **Talk!**

Simple as that.

---

## 6. Hanging Up

When you're done with a call (or if you pick up and decide not to dial):

1. **Place the handset back on the cradle**

The call ends immediately. The phone returns to idle.

> **Don't leave the handset off the cradle** -- the other person will hear silence, and your phone won't be able to receive calls.

---

## 7. Service Codes

These are hidden codes you can dial (while off-hook) to control the phone. Think of them like shortcuts.

Dial the code as if you were dialing a number -- press the keys in sequence, wait a moment, and the phone responds.

### Volume Control

| Code | What it does |
|------|-------------|
| `*#*0` | Mute (volume 0) |
| `*#*1` | Volume 1 (quiet) |
| `*#*5` | Volume 5 (medium) |
| `*#*9` | Volume 9 (maximum) |

Dial `*#*` followed by any digit `0`–`9` to set the volume level. The setting is saved and persists across reboots.

### Other Codes

| Code | Also written as | What it does |
|------|----------------|-------------|
| `*#8378#` | `*#TEST#` | **Audio test** -- records a 3-second clip and plays it back. Useful for checking mic/speaker. |
| `*#73887#` | `*#SETUP#` | **Re-enter Wi-Fi setup mode.** Use this if you change your Wi-Fi network or password. The phone reboots into setup mode. |
| `*#*#` | -- | **Safe shutdown.** Powers off the phone gracefully. Unplug after the LED goes dark. |
| `*##*` | -- | **Reboot.** Restarts the phone's software. Takes about 60 seconds. |

> **Tip:** The `*#SETUP#` and `*#TEST#` labels are mnemonics -- `SETUP` = `73887` and `TEST` = `8378` on a phone keypad. The shutdown (`*#*#`) and reboot (`*##*`) codes use `*` and `#` keys only.

---

## 8. LED Indicator

The small red LED on the front of the phone tells you what the phone is doing.

### During startup

The LED shows the phone's last known state while it boots (~10 seconds):

| LED pattern | Meaning |
|-------------|---------|
| Smooth breathing (fades in and out) | Paired and working normally |
| Brief flash every 1.7 seconds | Unpaired, waiting for a pairing code |
| Two quick flashes, pause, repeat | In WiFi setup mode |
| Rapid blinking | Recovery mode or error |
| Off | First boot (brand new device) |

### Normal operation

Once the phone finishes booting, the LED reflects what the phone is doing right now:

| LED pattern | Meaning |
|-------------|---------|
| Off | Idle, on-hook. Phone is ready to receive calls. |
| Solid on | Handset is off the cradle (dial tone or dialing) |
| Smooth breathing | Active call in progress |
| Blinking (~1 per second) | Incoming call. Pick up the handset to answer. |
| Brief flash every 1.7 seconds | Unpaired. Pick up the handset to hear your pairing code. |
| Rapid blinking | Something went wrong. Try rebooting. |

---

## 9. Privacy

Your Digits phone was designed with privacy as a core principle -- not a checkbox.

**End-to-end encrypted calls**  
All voice calls are encrypted directly between the two phones. Not even the Digits network can listen to your conversations.

**No recordings, ever**  
Calls are never recorded or stored. When a call ends, it's gone.

**Hardware microphone disconnect**  
When the handset is resting on the cradle, the microphone is **physically disconnected** from the circuit. This is not a software mute -- there is no software that can override it. A phone in your room cannot listen when it's on the hook. That's a hardware guarantee.

**No screen, no apps, no tracking**  
There's nothing to tap, no account to log into, no data to harvest. The phone doesn't know your location, your contacts, or your habits. It makes calls. That's all.

---

## 10. Troubleshooting

### I don't hear a dial tone when I pick up

- Wait 60 seconds and try again -- the phone may still be booting up
- Check that the power adapter is fully plugged in (both ends)
- Make sure your home Wi-Fi is working -- try loading a webpage on another device
- If the LED is blinking rapidly and not stopping, the phone may be having trouble connecting. Try rebooting: pick up the handset and dial `*##*`, then wait about 60 seconds

### The phone never got a dial tone after setup

- The Wi-Fi password may have been entered incorrectly
- Dial `*#73887#` to re-enter setup mode, then go through Wi-Fi setup again
- Make sure you're connecting to a 2.4 GHz Wi-Fi network -- the phone does not support 5 GHz only networks

### I can't connect to the Digits-XXXX hotspot

- Make sure your phone's Wi-Fi is on and you're within range of the Digits phone
- The hotspot is only active when the phone is in setup mode. If it's already set up, dial `*#73887#` first to re-enter setup mode
- On some devices, you may need to "forget" old networks or toggle Wi-Fi off/on

### The setup page didn't load

- After connecting to `Digits-XXXX`, wait 10–15 seconds for the captive portal notification
- If no notification appears, open a browser and type: `http://192.168.4.1`
- Make sure you're connected to `Digits-XXXX` and not your home network (some phones switch back automatically)

### The bell rings but no one is there when I pick up

- The caller may have hung up before you answered
- Check that your phone number is correct with the person trying to call you

### The call sounds bad / echoey

- Try adjusting the volume with `*#*5` or `*#*6` (a lower volume can reduce echo)
- Run the audio test: dial `*#8378#` (`*#TEST#`) while off-hook, speak into the handset, and listen to the playback. If it sounds distorted, the volume may be too high.
- Move the phone away from the Wi-Fi router if they're right next to each other

### The phone isn't ringing for incoming calls

- Make sure the handset is fully on the cradle -- if it's slightly off, the phone thinks you're already using it
- Check that the phone is powered on and the LED is off (idle state)

### I need to change my Wi-Fi network

Dial `*#73887#` (or `*#SETUP#`) while off-hook. The phone will reboot into setup mode, and you can connect to the new network following the Wi-Fi setup steps above.

---

## Quick Reference Card

```
┌─────────────────────────────────────────────────┐
│              DIGITS QUICK REFERENCE             │
├─────────────────────────────────────────────────┤
│  MAKE A CALL    Pick up → dial 7 digits         │
│  ANSWER         Pick up when bell rings         │
│  HANG UP        Place handset on cradle         │
├─────────────────────────────────────────────────┤
│  VOLUME         *#*0 (mute) ... *#*9 (max)      │
│  AUDIO TEST     *#8378#  (*#TEST#)              │
│  WI-FI SETUP    *#73887#  (*#SETUP#)            │
│  REBOOT         *##*                            │
│  SHUTDOWN       *#*#                            │
├─────────────────────────────────────────────────┤
│  LED OFF   = idle   LED ON = in use             │
│  LED BLINK = incoming call                      │
├─────────────────────────────────────────────────┤
│  My number: ___________________                 │
│  Pairing code: _________________                │
└─────────────────────────────────────────────────┘
```

---

*For support or questions, visit [digits.family](https://digits.family) or email support@digits.family.*
