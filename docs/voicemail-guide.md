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
have, then plays them one at a time, oldest first.

While a message is playing, the keypad controls the session:

| Key | What it does |
|-----|--------------|
| `7` | Delete this message and move to the next one. You hear "Message deleted". |
| `9` | Save this message and move to the next one. You hear "Message saved". |
| `#` | Skip to the next message without saving or deleting this one. |
| `*` | Replay the current message from the start. |

A saved message is marked as heard and is kept; a deleted message is gone for
good. When there are no more new messages, the phone says "End of messages".
Hang up at any time to stop.

### Recording your own greeting

By default, callers hear a standard Digits greeting. To record your own:

1. Lift the handset and dial `*97`.
2. Wait for the prompt: "Record your greeting after the tone."
3. After the tone, speak your greeting.
4. The phone confirms with "Greeting saved".

Your greeting can be up to 60 seconds long. Recording a new greeting replaces
the previous one.

### Removing your greeting

To delete your custom greeting and go back to the standard one, lift the
handset and dial `*99`. The phone confirms with "Greeting deleted" and returns
to dial tone.

## Configuring voicemail in the web app

Voicemail settings live on the phone's detail page in the web app. From the
phones list, open a phone to its detail page (`/phones/{number}`), then find
the voicemail section.

### Turning voicemail on and off

The voicemail section has an enabled checkbox. Tick it to turn the answering
machine on for that line, untick it to turn it off. The change saves as soon as
you click; there is no separate save button for the toggle. With voicemail off,
the phone simply rings and never auto-answers.

### The settings form

Open the advanced settings block in the voicemail section to edit four fields.
Change what you need, then submit the form to save.

| Field | What it controls | Allowed range |
|-------|------------------|---------------|
| Ring timeout | How many seconds a call rings before the answering machine picks up | 5 to 60 seconds |
| Max message | The longest a single caller message can be | 15 to 180 seconds |
| Max stored messages | How many messages the phone keeps before the oldest is dropped | 5 to 200 |
| Retrieval code | The code you dial on the phone to listen to messages | 2 to 6 characters, digits and `*` and `#` only, must include at least one `*` or `#` |

If you enter a number outside its range, or a retrieval code that breaks the
rules, the app rejects the form with a message explaining the limit; nothing is
saved until the values are valid. A retrieval code cannot be all digits, so it
can never be mistaken for a real phone number.

When voicemail is disabled, the four fields are dimmed and locked. Turn
voicemail on to edit them.

### The unheard-messages badge

When voicemail is on and the phone has messages you have not listened to, the
detail page shows an unheard-count badge. It clears as you listen to messages.
The count is live from the phone, so it can take a moment to update after you
clear messages on the handset.

### When settings take effect

The on/off toggle, ring timeout, and retrieval code apply on the phone's next
incoming call. The max message length and storage cap apply after the phone
restarts. If you edit settings while the phone is offline, the phone picks them
up the next time it connects.

## Limits and edge cases

- **Message length.** Each message can be up to 90 seconds by default. When a
  caller reaches the limit, recording stops and the message is saved.
- **Greeting length.** A custom greeting can be up to 60 seconds.
- **Storage cap.** The phone keeps up to 50 messages by default. When the box
  is full and a new message arrives, the oldest message is deleted to make
  room. Listen to and clear your messages so you do not lose old ones to new
  ones.
- **Lots of messages.** If you have ten or more unheard messages, the phone
  stops counting them out one by one and just tells you that you have many
  messages. The exact number is not read aloud past nine.
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

**I changed the message limit or storage cap and nothing changed.** The
maximum message length and the storage cap are read when the phone's daemon
starts. A change to either takes effect after the phone restarts. The on/off
toggle, ring timeout, and retrieval code apply right away.

**The message-waiting light is pulsing but `*98` says no messages.** The slow
pulse tracks unheard messages. If you saved or deleted everything, the light
clears on the next idle check. If it persists, restart the phone.

**Messages are missing.** The phone keeps a fixed number of messages and
deletes the oldest when full. If old messages disappeared, the box hit its cap.
Clear messages regularly to avoid this.
