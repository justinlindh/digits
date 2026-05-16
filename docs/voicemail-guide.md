# Voicemail Guide

Your Digits phone has a built-in answering machine. When a call comes in and
nobody picks up, the phone answers it, plays a greeting, and records a message.
You listen to messages later by dialing a short code on the same phone.

This guide covers using voicemail from the handset and configuring it from the
web app.

## Using voicemail from the phone

### When someone leaves you a message

If a call rings long enough without being answered, the phone picks up on its
own. The caller hears your greeting, then a beep, and can leave a message. Your
handset microphone is muted the whole time a message is being recorded, so a
caller never hears what is happening in your room.

If you pick up the handset while the greeting or recording is playing, the
phone hands you the live call and the answering machine steps out of the way.

### The message-waiting light

When the phone is idle and you have at least one message you have not listened
to yet, the front LED gives a slow pulse instead of staying dark. A dark LED
means no new messages. The slow pulse is your "you have voicemail" light.

### Listening to your messages

Lift the handset and dial `*98`. The phone tells you how many new messages you
have, reads out the keypad controls, then plays the messages one at a time,
oldest first. When you have more than one new message, the phone says "Message
one", "Message two", and so on before each one so you can keep track.

While a message is playing, the keypad controls the session:

| Key | What it does |
|-----|--------------|
| `7` | Delete this message and move to the next one. You hear "Message deleted". |
| `9` | Save this message and move to the next one. You hear "Message saved". |
| `#` | Skip to the next message. The skipped message stays new, so you hear it again next time. |
| `*` | Replay the current message from the start. |

Letting a message play all the way to the end counts as listening to it: the
phone marks it heard and keeps it. Pressing `9` does the same thing on demand.
A heard message is not gone, it is kept and you can hear it again in the saved
review (below). Only `7` deletes a message for good.

### Saved messages

After your new messages, if you have older messages you have already heard, the
phone says how many saved messages you have and plays those too. You can
delete, replay, or skip them with the same keys. If you dial `*98` and have no
new messages but do have saved ones, the phone goes straight to the saved
review.

When there are no more messages of either kind, the phone says "End of
messages". Hang up at any time to stop.

### Hearing your current greeting

To check what callers hear when they reach your voicemail, lift the handset and
dial `*96`. The phone says "Your current answering machine greeting is...", then
plays your active greeting: your own recording if you have made one, otherwise
the standard Digits greeting. It then returns to dial tone. This is playback
only; it never changes your greeting or your messages.

### Recording your own greeting

By default, callers hear a standard Digits greeting. To record your own:

1. Lift the handset and dial `*97`.
2. Wait for the prompt. It tells you to press the pound key when you are
   finished, or hang up, and is followed by a short pause and a tone.
3. After the tone, speak your greeting.
4. Press `#` when you are done. The phone confirms with "Greeting saved".

Your greeting can be up to 60 seconds long. You can also finish by hanging up,
and the phone stops on its own at the 60-second limit; all three ways save what
you recorded. Recording a new greeting replaces the previous one.

To hear your greeting back, dial `*96` (see "Hearing your current greeting"
above).

### Removing your greeting

To delete your custom greeting and go back to the standard one, lift the
handset and dial `*99`. The phone confirms with "Greeting deleted" and returns
to dial tone.

## Configuring voicemail in the web app

Voicemail settings live on the phone's detail page in the web app. From the
phones list, open a phone to reach its detail page, then find the voicemail
section.

### Turning voicemail on and off

The voicemail section has an enabled checkbox. Tick it to turn the answering
machine on for that line, untick it to turn it off. The change saves as soon as
you click; there is no separate save button for the toggle. With voicemail off,
the phone simply rings and never auto-answers.

### The ring timeout

The voicemail section has a ring timeout field: how many seconds a call rings
before the answering machine picks up. Change it, then submit the form to save.
The allowed range is 5 to 60 seconds.

If you enter a number outside that range, the app rejects the form with a
message explaining the limit; nothing is saved until the value is valid.

When voicemail is disabled, the ring timeout field is dimmed and locked. Turn
voicemail on to edit it.

### The unheard-messages badge

When voicemail is on and the phone has messages you have not listened to, the
detail page shows an unheard-count badge. It clears as you listen to messages.
The count is live from the phone, so it can take a moment to update after you
clear messages on the handset.

### When settings take effect

The on/off toggle and ring timeout apply on the phone's next incoming call. If
you edit settings while the phone is offline, the phone picks them up the next
time it connects.

## Limits and edge cases

- **Message length.** A message ends when the caller hangs up. As a backstop
  for a caller who never hangs up, a single recording is capped at 10 minutes;
  a caller still talking when the cap is reached hears a beep and the recording
  is saved. The cap is fixed and not configurable.
- **Greeting length.** A custom greeting can be up to 60 seconds.
- **Storage cap.** The phone keeps up to 50 messages. When the box is full and
  a new message arrives, the oldest message is deleted to make room. Listen to
  and clear your messages so you do not lose old ones to new ones.
- **Lots of messages.** If you have more than nine unheard messages, the phone
  announces the count as "nine"; it does not read an exact number aloud past
  nine.
- **Ring time.** A call has to ring for the configured timeout (20 seconds by
  default) before the answering machine picks up. Setting the timeout to zero
  turns auto-answer off; the phone just keeps ringing.

## Troubleshooting

**The phone never picks up unanswered calls.** Voicemail may be turned off for
the line, or the ring timeout may be set to zero. Check the line's voicemail
settings in the web app. A change to the on/off toggle or the ring timeout
takes effect on the next incoming call.

**Callers say they did not hear my custom greeting.** If recording a greeting
did not finish cleanly the phone falls back to the standard greeting. Dial
`*97` and record it again, and wait for the "Greeting saved" confirmation
before hanging up.

**The message-waiting light is pulsing but `*98` says no messages.** The slow
pulse tracks unheard messages. If you saved or deleted everything, the light
clears on the next idle check. If it persists, restart the phone.

**Messages are missing.** The phone keeps a fixed number of messages and
deletes the oldest when full. If old messages disappeared, the box hit its cap.
Clear messages regularly to avoid this.

## How it works

For how voicemail is built (the call state machine, on-disk message storage,
audio pipeline, signaling, and service-code reference), see
[voicemail.md](voicemail.md).
