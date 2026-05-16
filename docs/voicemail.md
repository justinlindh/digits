# Voicemail (Answering Machine)

How an unanswered call is auto-answered, recorded, and played back later, how a
household configures the feature, and where the work is split between the phone
daemon and the server.

## Overview

Voicemail is a per-line answering machine. When an incoming call rings for
longer than a configured timeout and nobody picks up, `digitsd` answers the
call itself, plays an outgoing greeting and a beep, mutes the handset
microphone, and records the caller's audio to local storage. The household
member retrieves messages later by dialing a retrieval code on the same phone.

Responsibility is split in two:

- **`digitsd`** owns everything on the device: the call FSM, auto-answer, audio
  recording and playback, on-disk message storage, the outgoing greeting, the
  spoken voice prompts, and the dial-in retrieval and management codes.
- **The server** owns the per-line settings: it stores each line's voicemail
  configuration, exposes a web UI to edit it, and pushes the settings down to
  the daemon over the signaling WebSocket. The server never sees or stores
  message audio. Recordings live only on the phone.

Recorded audio is never uploaded. A caller's message exists as a file on the
callee's Pi and nowhere else.

## digitsd internals

### FSM states

The phone controller (`internal/phone/controller.go`) adds five states for
voicemail:

| State | Meaning |
|-------|---------|
| `VOICEMAIL_GREETING` | Call auto-answered; playing the outgoing greeting, then the beep |
| `VOICEMAIL_RECORDING` | Recording the caller's audio |
| `VOICEMAIL_RECORD_GREETING` | The household member is recording a custom outgoing greeting (`*97`) |
| `VOICEMAIL_PLAY_GREETING` | The household member dialed `*96`; the active outgoing greeting is auditioning through the earpiece |
| `VOICEMAIL_PLAYBACK` | The household member dialed the retrieval code; stored messages are playing back |

### Auto-answer on ring timeout

When an inbound call arrives, `onSignalRing()` checks whether voicemail is
enabled for the line (`VoicemailEnabled()`), reading the live config so a
setting change takes effect on the next call. If voicemail is on and the
configured ring timeout is greater than zero, the controller increments a
generation counter (`ringTimeoutGen`) and spawns a `ringTimeoutWatcher`
goroutine.

The watcher sleeps for the ring timeout. When it wakes, it transitions to
`VOICEMAIL_GREETING` and calls `VoicemailAutoAnswer()` only if the phone is
still in `RINGING` and the generation counter still matches. The generation
counter is the cancellation mechanism: picking up the handset, a remote hangup
during ring, or a controller `Reset()` each increment `ringTimeoutGen`, which
makes the stale watcher a no-op when it wakes.

If the household member picks up while the greeting or recording is in
progress (`VOICEMAIL_GREETING` or `VOICEMAIL_RECORDING`), the controller
transitions to `CONNECTED` and calls `VoicemailPickup()`, which unmutes the
microphone and restores a normal two-way call.

### Storage package and on-disk format

`internal/voicemail/store.go` owns message storage. The store directory is
`voicemail/` next to the config file, so on a device it resolves to
`/data/digits/voicemail/`. The store is opened once at daemon startup; if
`Voicemail.Enabled` is false at boot the store is never opened.

Directory layout:

```
/data/digits/voicemail/
  <unix_ms>.frames        recorded message audio
  <unix_ms>.meta          message metadata (JSON)
  <unix_ms>.frames.tmp    temp file held open during recording
  greeting.frames         custom outgoing greeting (optional, single file)
  greeting.frames.tmp     temp file held open during greeting recording
```

Each message is identified by `<unix_ms>`, the Unix-millisecond timestamp of
when recording started.

**Frame file format.** A `.frames` file is a flat sequence of length-prefixed
Opus payloads. Each frame is a 4-byte little-endian `uint32` length header
followed by that many bytes of Opus payload. Payloads carry 20 ms of audio
(`opusFrameMs = 20`, matching `internal/codec`). A payload larger than
`0xffff` bytes is rejected. Recording tees the caller's raw inbound Opus
payloads straight into the file, so no re-encode happens on the record path.

**Metadata file format.** A `.meta` file is JSON:

```go
type metaFile struct {
    Heard      bool      `json:"heard"`
    DurationMs int64     `json:"duration_ms"`
    RecordedAt time.Time `json:"recorded_at"`
}
```

In memory a message is a `Message`: `ID int64` (the Unix-millisecond id),
`Heard bool`, `Duration time.Duration`, `Path string` (absolute path to the
`.frames` file), and `RecordedAt time.Time`.

**Atomic writes.** A recording is written to `<id>.frames.tmp`. On `Finalize()`
the temp file is fsynced, closed, and atomically renamed to `<id>.frames`. The
metadata is written the same way: to `<id>.meta.tmp`, fsynced, then renamed to
`<id>.meta`. A crash mid-recording leaves a `.tmp` file behind; `Open()` sweeps
orphan `.tmp` files on startup.

**FIFO eviction.** After a recording is finalized, `evictLocked()` enforces the
`MaxMessages` cap. If the message count exceeds the cap, the oldest messages
(lowest id first) are deleted, both the `.frames` and `.meta` file, until the
count is back within the cap. The greeting (id `0`) is exempt from both the
retention count and eviction.

### Audio recording flow and microphone mute

When `VoicemailAutoAnswer()` runs, the daemon answers the WebRTC call and
installs an `OnRemoteTrack` handler. Caller audio arrives as RTP packets and is
handled in three phases: discard packets until the pipeline is ready (keeping
the Opus decoder state in sync), drain the buffered backlog, then treat the
stream as live. In the live phase the decoded PCM is mixed to the handset
speaker so the household can hear a message being left, and each inbound
packet's raw Opus payload is appended to the recorder with
`AppendFrame(pkt.Payload)`.

**Recording starts after the greeting, not at connect.** The recorder tee is
gated by a `voicemailRecordArmed` flag that stays false from auto-answer until
the outgoing greeting and the prompt beep have both finished playing. The
remote track goes live well before then (the caller is hearing the greeting),
so before the flag is armed those frames are decoded but not appended. Arming
the recorder at the greeting-to-recording transition means a stored message
begins at the caller's first word, with no leading greeting-duration silence. A
caller who hangs up while the greeting is still playing leaves no message at
all, because the recorder was never armed.

When the recorder reports it has hit the message-duration cap, the daemon
finalizes the recording and calls `VoicemailRecordEnded()`.

**Microphone mute is a security property.** The caller must never hear the
callee's environment while leaving a message. The capture pipeline
(`internal/audio/pipeline.go`) exposes `SetMuted(bool)`, an atomic flag; when
set, `maybeMute()` zeros every int16 sample in each captured frame before it
reaches the encoder. The greeting and beep play first; once `PlayGreetingBeep()`
finishes, the daemon calls `SetMuted(true)` before transitioning into
`VOICEMAIL_RECORDING`, so the entire time the caller is being recorded the
handset microphone is sending silence. The mute is lifted only by
`VoicemailPickup()`, when a household member picks up to take the call live.

### Greeting playback and embedded-WAV fallback

The outgoing greeting is played by `playVoicemailGreeting()`, which tries the
custom greeting first and falls back to the default.

`playCustomGreeting()` opens `greeting.frames` through `Store.OpenGreeting()`.
If no custom greeting has been recorded the open returns `os.ErrNotExist` and
the function returns false to signal fallback. Otherwise it decodes every Opus
frame into a flat PCM buffer and injects it into the pipeline with
`PlayGreetingSamples()`.

`playDefaultGreeting()` loads the embedded WAV asset
`internal/assets/embed/data/tones/voicemail_greeting.wav` through the mixer's
`ToneSamples("voicemail_greeting")` and injects it the same way. If even that
asset is missing, the daemon logs a warning and proceeds with the beep only.

After the greeting comes a 500 ms 1 kHz beep synthesized by
`PlayGreetingBeep()`.

### Greeting recording (`*97`)

Dialing `*97` enters `VOICEMAIL_RECORD_GREETING` and calls
`VoicemailRecordGreeting()`. The daemon brings up the audio pipeline in
microphone-capture mode (no WebRTC peer), plays the `vm_record_greeting` spoken
prompt followed by a short beep, then opens a greeting recorder and starts a
loop that encodes captured microphone frames to Opus and appends them to
`greeting.frames`.

The prompt and beep play through the mixer into the earpiece, not the outbound
capture path: the `*97` flow has no remote peer, so injecting the beep into
capture would mean nobody hears it. The spoken prompt tells the user how to end
the recording ("press pound when finished, or hang up") and is followed by a
short pause before the beep, so the instruction lands clearly before the
recorder goes hot. `waitForOnceComplete` gates the recording loop until the
beep has finished, keeping the prompt and beep out of the recording itself.

A greeting recording ends on any of three paths, and all three finalize and
save the recording:

- **`#` key.** `VoicemailRecordGreetingKey("#")` calls
  `finalizeGreetingRecording()`.
- **Duration cap.** The recorder caps at `greetingMaxDuration` (60 s); on the
  cap the loop finalizes itself.
- **Hang-up.** An on-hook during recording routes through the controller's
  hang-up path, which finalizes the partial greeting.

`finalizeGreetingRecording()` is idempotent: the first caller wins, renames the
temp file over any prior `greeting.frames`, plays the `vm_greeting_saved`
confirmation, stops the pipeline, and returns the phone to dial tone.

### Greeting audition (`*96`)

Dialing `*96` enters `VOICEMAIL_PLAY_GREETING` and calls
`VoicemailPlayGreeting()`. It is a pure read-only audition of the active
outgoing greeting: no recorder is opened and the stored greeting is never
modified.

The daemon decodes the active greeting with `decodeCustomGreeting()`, the same
Opus-decode helper the auto-answer path uses. If a custom greeting is recorded
it is decoded; otherwise the daemon loads the embedded default through
`ToneSamples("voicemail_greeting")`. Either way the daemon first plays the
`vm_current_greeting` spoken intro ("Your current answering machine greeting
is..."), then the greeting itself, both through the mixer one-shot into the
earpiece. When playback finishes the FSM returns to dial tone.

`VoicemailPlayGreeting` runs on its own goroutine. A hook-on mid-audition
routes through `VoicemailExitGreetingPlayback()`, which clears the one-shot
queue with `Mixer.StopOnce()` so the audition stops at once. Completion goes
through `Controller.FinishGreetingAudition()`, which checks the FSM state and,
only if the audition is still active, transitions to dial tone, all under the
controller lock so a racing hook-on cannot leave a dial tone looping against an
on-hook handset.

### Voice prompt assets

Every voicemail interaction has audible spoken feedback. The prompt WAVs live
in `internal/assets/embed/data/tones/`:

| Asset | Spoken content |
|-------|----------------|
| `vm_you_have.wav` | "You have" |
| `vm_new_message.wav` | "new message" (singular) |
| `vm_new_messages.wav` | "new messages" (plural) |
| `vm_no_messages.wav` | "You have no messages" |
| `vm_lost_count.wav` | A self-contained "many messages" phrase for counts of 10 or more |
| `vm_message_deleted.wav` | "Message deleted" |
| `vm_message_saved.wav` | "Message saved" |
| `vm_message.wav` | "Message" (composed with a digit clip for the per-message announcement) |
| `vm_saved_message.wav` | "saved message" (singular) |
| `vm_saved_messages.wav` | "saved messages" (plural) |
| `vm_end_of_messages.wav` | "End of messages" |
| `vm_playback_controls.wav` | The spoken playback-controls prompt, played once per `*98` session |
| `vm_record_greeting.wav` | The greeting-recording prompt, including how to finish ("press pound when finished, or hang up") |
| `vm_greeting_saved.wav` | "Greeting saved" |
| `vm_greeting_deleted.wav` | "Greeting deleted" |
| `vm_current_greeting.wav` | "Your current answering machine greeting is...", the `*96` audition intro |
| `voicemail_greeting.wav` | The default outgoing greeting |

The per-digit clips `spoken_0.wav` through `spoken_9.wav` live in the
`tones/pairing/` subdirectory and are shared with the pairing-code readout
feature.

**Composing "you have N messages".** `announceMessageCount(count)` builds the
phrase from individual clips:

- `count <= 0`: play `vm_no_messages` alone.
- `count >= 10`: play `vm_lost_count` alone. The system does not read out
  counts of ten or more digit by digit; one phrase covers the whole range.
- otherwise: play the sequence `vm_you_have`, `spoken_<count>`, then
  `vm_new_message` for one or `vm_new_messages` for more than one.

**Composing "Message N".** During a `*98` retrieval session that holds two or
more messages, `announceMessageNumber(number)` plays a spoken "Message N"
before each message so the listener can tell them apart. It composes
`vm_message` followed by `spoken_<number>`, where `number` is the 1-based
position of the message in the session. A position above 9 has no digit clip,
so the bare `vm_message` word plays as a separator cue. A session with a
single message skips the per-message announcement, since the "you have 1
message" count intro already identifies it.

`playAnnouncementSequence()` plays the clips back to back through the mixer's
`PlayOnce()`, waiting for each clip to finish before starting the next, with a
30 ms silence between clips.

### Retrieval playback (`*98`)

Dialing the retrieval code enters `VOICEMAIL_PLAYBACK` and calls
`VoicemailEnterPlayback()`. A retrieval session runs in two phases.

**New-message phase.** The daemon counts the unheard messages, announces the
count, plays the `vm_playback_controls` spoken prompt once, then plays the
unheard messages oldest first. When two or more messages are present each one
is preceded by its spoken "Message N" announcement. Playing a message through
to its end (EOF) marks it heard: `voicemailAdvanceFromEOF()` calls
`MarkHeard()` before opening the next message. A heard message is not deleted,
it is retained on disk and becomes reviewable in the saved phase.

**Saved-message review phase.** At session entry, before any new message is
marked heard, the daemon snapshots the IDs of every already-heard message into
a saved-review queue. When the new-message phase runs out, if that queue is
non-empty the session crosses into saved review: it announces the saved count
("You have N saved messages") and plays the heard messages in turn. If `*98` is
dialed when there are no unheard messages but saved ones exist, the session
enters saved review directly, playing the controls prompt on that path since
the new-message phase never ran. A mailbox with no messages of either kind
plays "You have no messages" and returns to dial tone.

**DTMF controls.** Keypad digits during playback route to `VoicemailKey()`:

- `7` deletes the current message (both files) and advances. Works in both
  phases.
- `9` in the new-message phase marks the current message heard and advances.
  In saved review the message is already heard, so `9` simply advances.
- `#` skips to the next message. In the new-message phase it leaves the heard
  flag untouched, so a skipped message stays unheard for a later session.
- `*` replays the current message from the start.

When a new-phase key exhausts the unheard messages and saved messages exist,
`7`, `9`, and `#` cross into saved review rather than ending the session. When
no message of either kind remains, the session plays "End of messages" and
returns to dial tone. Hanging up at any point ends the session immediately.

The net message lifecycle: a message arrives unheard; playing it to the end or
pressing `9` marks it heard; a heard message is kept and is reviewable through
the saved phase; `7` deletes it outright; doing nothing keeps it. There is no
implicit deletion from playback, only the FIFO eviction once the stored-message
cap is exceeded.

## Server internals

The server owns the per-line voicemail settings: their storage, the web UI to
edit them, and pushing them down to the phone. It never touches message audio.

### Per-line settings model

Voicemail config is per line and lives inside the existing `lines.settings`
JSONB column. There is no voicemail table and no schema migration; new fields
can be added to the struct without a DB change. The server structs are in
`server/internal/line/settings.go`:

```go
type Settings struct {
    VoiceStyle string    `json:"voice_style,omitempty"`
    SilentMode bool      `json:"silent_mode,omitempty"`
    AutoUpdate bool      `json:"auto_update,omitempty"`
    Voicemail  Voicemail `json:"voicemail"`
}

type Voicemail struct {
    Enabled            bool   `json:"enabled"`
    RingTimeoutSeconds int    `json:"ring_timeout_seconds"`
    MaxMessageSeconds  int    `json:"max_message_seconds"`
    MaxStoredMessages  int    `json:"max_stored_messages"`
    RetrievalCode      string `json:"retrieval_code"`
}
```

The inner `Voicemail` fields deliberately omit `omitempty` so a stored row
carries every field literally; a later read can tell "Enabled was explicitly
false" apart from "field absent". Time fields are integer seconds in storage
and on the wire. `line.DefaultVoicemail()` is what a new line starts with:
`Enabled: true`, ring timeout 20 s, max message 90 s, max stored 50, retrieval
code `*98`. `CreateLine` seeds the `settings` column with
`DefaultSettings().Normalize()` so the non-zero `enabled` default survives the
DB round trip.

`Voicemail.Normalize()` substitutes the default for any field that is zero or
out of range. It runs on every read and every write, so corrupt on-disk data
can never reach the daemon. `Settings.Merge` / `Voicemail.Merge` layer a
DB-loaded patch over the defaults: `Enabled` is overwritten unconditionally
(a bool has no unset sentinel), the other fields only when non-zero, so a
missing field keeps its default.

### Validation ranges

Bounds are package constants in `line/settings.go`, shared by the HTTP handler,
`Normalize()`, and tests:

| Field | Min | Max | Out-of-range heals to |
|-------|-----|-----|-----------------------|
| `ring_timeout_seconds` | 5 | 60 | 20 |
| `max_message_seconds` | 15 | 180 | 90 |
| `max_stored_messages` | 5 | 200 | 50 |

The retrieval code must match `^[0-9*#]{2,6}$` (2 to 6 characters, only digits,
`*`, and `#`) and must contain at least one `*` or `#`. A purely numeric code
is rejected so it cannot shadow a real 7-digit dial. An invalid code heals to
`*98`.

### Endpoints

Both endpoints are POST, registered on the authenticated mux in
`web/handler.go`, and ownership-checked with `requireLineOwnership` (an auth
failure returns 404, not 403).

- `POST /phones/{number}/voicemail` (`handlePhoneVoicemailPost`) takes the full
  form of all five settings: the `enabled` checkbox, the three integer fields
  (`ring_timeout_seconds`, `max_message_seconds`, `max_stored_messages`), and
  `retrieval_code`. The integers are validated with `parseClampedInt` (400 with
  a friendly "must be an integer between MIN and MAX" message on a bad value)
  and the code with `IsValidRetrievalCode` (400 on a malformed code), both
  before any DB write. On success it builds the new `Voicemail`, runs
  `Normalize()`, persists, and pushes to the device.
- `POST /phones/{number}/voicemail-toggle`
  (`handlePhoneVoicemailTogglePost`) flips `Voicemail.Enabled` only and takes no
  body fields. The other four fields are preserved through `Normalize()`, which
  backfills defaults if the row predates voicemail. It is a separate path so a
  checkbox round trip does not have to resubmit the timing and code fields.

Both swap the `voicemail-section` partial back for HTMX requests
(`am-voicemail-section` in the answering-machine theme) or send a 303 redirect
to `/phones/{number}` for a plain form post.

### Web UI

The voicemail UI lives entirely on the phone detail page,
`/phones/{number}`. The `/phones` list view does not carry it. It is rendered
by the `voicemail-section` partial (intercom and dialup themes) or
`am-voicemail-section` partial (answering-machine theme) in
`web/templates/phone-detail.html`:

- An enabled checkbox that `hx-post`s to `/voicemail-toggle` and swaps
  `#voicemail-section`.
- An advanced `<details>` block holding the four other fields. The whole form
  `hx-post`s to `/voicemail`. When voicemail is disabled the field block is
  dimmed and the inputs are `disabled`.
- An unheard-count badge ("N unheard" chip, or "MSG N" LED in the
  answering-machine theme) that renders only when voicemail is enabled and the
  unheard count is greater than zero.

The unheard count is not stored in the DB. It comes from
`hub.LineVoicemailUnheard(number)`, the sum of per-handset counts the hub last
received in `voicemail_state` messages from devices. It is in memory only and
resets when devices reconnect.

## Signaling

Per-line voicemail settings reach the daemon over the signaling WebSocket as
part of a line-settings update. The wire types are in
`internal/signal/protocol.go`:

```go
type Voicemail struct {
    Enabled            bool   `json:"enabled"`
    RingTimeoutSeconds int    `json:"ring_timeout_seconds"`
    MaxMessageSeconds  int    `json:"max_message_seconds"`
    MaxStoredMessages  int    `json:"max_stored_messages"`
    RetrievalCode      string `json:"retrieval_code"`
}

type LineSettings struct {
    VoiceStyle string     `json:"voice_style,omitempty"`
    SilentMode bool       `json:"silent_mode,omitempty"`
    AutoUpdate bool       `json:"auto_update,omitempty"`
    Voicemail  *Voicemail `json:"voicemail,omitempty"`
}
```

On the server side the wire copy is `signaling.LineSettings` /
`signaling.Voicemail` in `server/internal/signaling/protocol.go`, a deliberate
duplicate of `line.Settings` kept separate so the `internal/line` dependency
stays isolated. `LineSettings.Voicemail` is a pointer there: a nil pointer
means a settings push from a pre-voicemail server, while a non-nil block with
`enabled:false` means voicemail is explicitly disabled, so an old daemon sees
no surprise field. `VoicemailFromLine()` in `signaling/linestore_adapter.go` is
the single projection point from `line.Voicemail` to the wire type.

The server sends a `line_settings` message (`TypeLineSettings`) on two
triggers:

- **Settings change.** A successful POST to `/phones/{number}/voicemail` or
  `/voicemail-toggle` persists the settings, then `pushLineSettings` sends a
  `line_settings` message to the device registered as that number. A
  disconnected device is not an error: the push is skipped silently, and the
  phone receives the current settings on its next registration. Any other send
  failure is logged at warn level.
- **Device registration.** When a phone registers, `Relay.OnRegistered` loads
  the line's effective settings and pushes them, so a phone boots with current
  config. This is how a settings edit made while the phone was offline is
  caught up. There is no server-side retry queue.

The wire format uses integer seconds; the daemon converts them to
`time.Duration` for its own `config.Voicemail`. Not every setting takes effect
at the same time:

- `Enabled`, `RingTimeout`, and `RetrievalCode` are read live, per ring or per
  dial, so a change applies on the next inbound call with no restart.
- `MaxStoredMessages` and `MaxMessageDuration` are baked into the
  `voicemail.Store` when it is opened at boot, so a change to either takes
  effect on the next daemon restart.

In the other direction the daemon reports its unheard-message count to the
server with a `voicemail_state` message (`TypeVoicemailState`) carrying
`VoicemailUnheardCount`. The count field is always serialized, including an
explicit `0`, so the server can tell "zero unheard" apart from "not reported".

## Service code reference

Service codes are dialed on the phone. The mapping is defined in
`internal/phone/controller.go`; the retrieval code is configurable and the rest
are fixed.

| Code | Action |
|------|--------|
| `*96` | Audition the active outgoing greeting through the earpiece. Enters `VOICEMAIL_PLAY_GREETING`, then returns to dial tone. |
| `*97` | Record a custom outgoing greeting. Enters `VOICEMAIL_RECORD_GREETING`. |
| `*98` | Retrieve stored messages. Enters `VOICEMAIL_PLAYBACK`. This code is the configurable `RetrievalCode`; `*98` is the default. |
| `*99` | Delete the custom greeting and revert to the default. Returns to dial tone. |

`*96`, `*97`, and `*99` are fixed; only `*98` (the `RetrievalCode`) is
configurable. All four are gated on voicemail being enabled for the line.

During message playback (`VOICEMAIL_PLAYBACK`), DTMF digits control the
session. The controller routes them to `VoicemailKey(digit)`:

| Key | Action |
|-----|--------|
| `7` | Delete the current message (both files), then advance. Plays "Message deleted". Works in the new-message and saved-review phases. |
| `9` | New-message phase: mark the current message heard and advance, playing "Message saved". Saved-review phase: just advance, since the message is already heard. |
| `#` | Skip to the next message. In the new-message phase the heard flag is left untouched, so the message stays unheard for a later session. |
| `*` | Replay the current message from the start. |

When the new-message phase is exhausted and saved messages exist, `7`, `9`, and
`#` cross into the saved-review phase rather than ending the session. When no
message of either kind remains, the session plays "End of messages" and returns
to dial tone.

## Limits and defaults

Every configurable bound, defined in `internal/config/config.go`
(`defaultVoicemail()`) except the greeting cap, which is fixed in
`internal/voicemail/store.go`.

| Setting | Config field | Default | Notes |
|---------|--------------|---------|-------|
| Voicemail enabled | `Enabled` | `true` | Read live; off at boot means the store is never opened |
| Ring timeout before auto-answer | `RingTimeout` | 20 s | Read live; `0` disables auto-answer |
| Max message duration | `MaxMessageDuration` | 90 s | Baked into the store at boot |
| Max stored messages | `MaxStoredMessages` | 50 | Baked into the store at boot; FIFO eviction past the cap |
| Retrieval code | `RetrievalCode` | `*98` | Read live |
| Max greeting duration | `greetingMaxDuration` | 60 s | Fixed in `store.go`, not configurable |

The config file is `/data/digits/config.json`. Settings pushed from the server
update this config; see Signaling above for which take effect immediately and
which need a restart.

These daemon-side values are the defaults. The server clamps user-entered
values to narrower ranges before they are pushed down (ring timeout 5 to 60 s,
max message 15 to 180 s, max stored 5 to 200); see Validation ranges under
Server internals.
