# Party Line (Three-Way Calling)

How a host on an active two-party call pulls a third person in, how the audio flows, and what the server does (and doesn't do) with it.

## The gesture

Historical reference: residential Three-Way Calling on Bell-era 5ESS and DMS-100 switches, mid-1990s.

During an active call, the host briefly presses the hook switch (100-600 ms on-hook pulse) and releases it. The firmware recognises this pulse as a `HOOK:FLASH` event, distinct from a real hangup (>600 ms on-hook). Anything shorter than 100 ms is suppressed as contact bounce.

From there:

| State | What the user hears | How to advance |
|-------|---------------------|----------------|
| `CONNECTED` | The other party | Flash to start add |
| `ADD_DIALTONE` | Second dial tone | Dial the third party |
| `ADD_DIALING` | DTMF tones | Digits collected; firmware emits `DIAL:<number>` on completion |
| `ADD_CALLING` | Ringback | Wait for answer, or flash to abort |
| `ADD_PRIVATE` | The third party only (held party hears silence) | Flash to merge, or hang up to collapse |
| `CONFERENCE_MERGED` | Everyone | All three connected; only the host's hang-up collapses the conference |
| `ADD_INTERCEPT` | SIT intercept tone | Third party unreachable / busy / hung up; flash to return to held party |

Semantics follow 1990s residential TWC exactly:

- **Three parties, hard cap.** No four-way. Host-initiated only (the two added parties cannot flash-add a fourth).
- **Silent hold.** The held party hears comfort noise only (Opus DTX generates SID frames from zero mic input; the receiver renders a low background noise floor, mimicking POTS line hiss).
- **No join beep.** The three just start hearing each other after merge.
- **Host hangup collapses.** When the host hooks on, everyone disconnects. Non-host hangup ends the conference but leaves the remaining pair in a normal 2-party call.

## Media topology: full mesh, end-to-end encrypted

Each participant holds one WebRTC peer connection to each other participant. For three parties that is three DTLS-SRTP legs, each keyed independently:

```text
      A ←→ B
      ↕    ↕
        C
```

The server never sees media. Audio for each leg is encrypted end-to-end with the same DTLS-SRTP stack used for regular 2-party calls. There is no SFU, no MCU, no relay in the media path. Conference audio mixing happens locally on each device: the handset's speaker receives the sum of all inbound decoded PCM streams, clipped to int16.

This preserves the project's baseline privacy property (server cannot eavesdrop) without introducing a new key-distribution scheme.

### Per-peer encode and mute

Each `PeerManager` owns its own Opus encoder and its own Opus decoder. Encoding is parallelised across peers via `SendPCMFrameToAll` when the capture pipeline produces a new 20 ms mic frame. Per-peer outbound mute is achieved by substituting a zero-filled buffer before encode; with Opus DTX enabled the encoder emits SID frames, which the receiver renders as comfort noise.

Silent hold is expressed as `SetOutboundMuted(true)` on the specific peer being held (the B leg). Audio to the added third party (C) keeps flowing normally during the private conversation phase.

## State machine

```text
CONNECTED
    │ HOOK:FLASH
    ▼
ADD_DIALTONE ── flash ──► CONNECTED (abort)
    │ digit
    ▼
ADD_DIALING ── flash ──► CONNECTED (abort)
    │ DIAL:<n>
    ▼
ADD_CALLING ── flash / busy / timeout ──► ADD_INTERCEPT ── flash ──► CONNECTED
    │ answer
    ▼
ADD_PRIVATE ── remote hangup ──► ADD_INTERCEPT
    │ HOOK:FLASH
    ▼
CONFERENCE_MERGED ── host HOOK:ON ──► IDLE (conference collapses)
                  ── member HOOK:ON ──► remaining pair keeps talking, conference ends
                  ── server ConferenceEnd ──► REMOTE_HANGUP (plays reorder until hookup)
```

All `ADD_*` states plus `CONFERENCE_MERGED` are treated as "conference flow" for hangup semantics: `HOOK:ON` from any of them triggers full teardown (mesh peers, 2-party peer, pipeline, server notification).

## Server role: signalling and membership, not media

The server tracks conference lifecycle in an in-memory `ConferenceTracker` and persists to Postgres:

- `conferences` row per conference (id, host_phone, originating_call_id, state, created_at, ended_at, end_reason)
- `conference_members` rows per participant (role is `host` or `added`, join/leave timestamps)
- `calls.originating_conference_id` on the continuation 2-party call created when a member drops mid-conference

The relay adds five new message types:

| Type | Direction | Purpose |
|------|-----------|---------|
| `conference_merge` | client → server | Host requests merge of the two active 2-party calls into a conference |
| `conference_member` | server → all clients | Authoritative membership snapshot after merge |
| `conference_connect` | server → added members | Tells B and C which of them should initiate the B↔C peer negotiation (deterministic tiebreak: lower phone number is initiator) |
| `conference_leave` | server → remaining | One member has dropped |
| `conference_end` | server → remaining | Conference is over; tear everything down |

SDP and ICE messages exchanged between conference members carry a `conf_id` field. The relay short-circuits its `CanCall` authorization check when both endpoints are members of the same active conference — this is the transitive authorization that lets the three parties signal peer-to-peer even if they otherwise couldn't call each other directly.

### Migration

Schema changes live in `server/internal/db/db.go`:

- v15 adds `conferences` + `conference_members` tables and `calls.originating_conference_id`.
- v16 adds `calls.end_reason` so the call-history query can filter out pre-merge 2-party rows that were absorbed into a conference.

## Call history

A 3-way call produces either one or two entries in `/calls`:

- **Clean merge + collapse:** one conference entry, spanning `created_at` to `ended_at`, with all three participants listed.
- **Member drop mid-conference:** one conference entry + one 2-party continuation call. The continuation row has `originating_conference_id` pointing at the ended conference; the UI renders it with a small "from 3-way" badge.

The intermediate 2-party rows (A↔B and A↔C that were ended at merge time) are hidden via `WHERE end_reason IS NULL OR end_reason != 'merged_to_conference'`. They are still in the DB for audit but never surface in the history UI.

Both the "intercom" and "dialup" webapp themes render the conference chip (`.chip--conf`) in their respective palettes.

## Firmware: hook-flash detection

`firmware/src/hook.c` implements the pulse detector. The physical mapping on the Pico H GPIO is:

- `raw == true` → handset lifted (off-hook, GPIO pulled up)
- `raw == false` → handset cradled (on-hook, GPIO grounded by switch)

A flash is a brief on-hook pulse during an off-hook call: off-hook → on-hook (100-600 ms) → off-hook. On detecting a transition back to off-hook, the firmware checks whether the preceding on-hook duration lies in the flash window. If so, it emits `HOOK:FLASH` instead of the usual `HOOK:OFF`/`HOOK:ON` sequence.

The minimum firmware version that supports this is `fw/v1.5.0`. Older firmware never emits `HOOK:FLASH`; `digitsd` compares the reported version against `hookFlashMinFirmware` at startup and silently drops any `HOOK:FLASH` events from pre-v1.5.0 Picos.

## Failure modes

All handled and tested:

- **Third party busy / unreachable / ring timeout** → SIT intercept tone; flash returns to held party without creating a conference.
- **Third party hangs up during private** → SIT intercept tone; flash returns to held party.
- **Held party (B) hangs up during add-dial** → silent drop; the host's ADD flow continues, but any subsequent merge request will reject because B is no longer in an active call. The host ends up in a regular 2-party call with C.
- **Host hangs up during ADD_\* states** → both A↔B and A↔C 2-party calls are ended on the server via `AllPeersOf(from)` in `handleHangup`. Mesh peers torn down locally; `HangupCall()` sends a hangup to the active peer; the server's loop over `AllPeersOf` catches the other one.
- **Non-host hangs up during conference** → server emits `ConferenceLeave` to others, creates a 2-party continuation calls row for the survivors, sends `ConferenceEnd` to collapse the conference entity.
- **Merge fails (server rejects)** → `TypeConferenceRejected` returns the host to `CONNECTED` with the held party; intercept tone plays; the third party leg is torn down.
- **WebSocket disconnect (power cut, Wi-Fi drop) during conference** → `Relay.OnDisconnect` detects active-conference membership, calls `endConference("disconnect")`, persists `state=ended, end_reason=disconnect`, notifies remaining members with `ConferenceEnd`. Without this, the conference would otherwise stay active forever and leave all members permanently busy.

## Code organisation

| Layer | Files |
|-------|-------|
| Firmware | `firmware/src/hook.c` (flash detection), `firmware/src/phone_fsm.c` (event dispatch) |
| Pi daemon: audio | `pi/digitsd/internal/audio/mixer.go` (N-source mixing), `pi/digitsd/internal/audio/pipeline.go` (`SetMuted`) |
| Pi daemon: media | `pi/digitsd/internal/webrtc/peer.go` (per-peer encoder/decoder/mute), `pi/digitsd/internal/webrtc/mesh.go` (`MeshManager`, `Adopt`, `SendPCMFrameToAll`) |
| Pi daemon: control | `pi/digitsd/internal/phone/controller.go` (FSM, conference handlers), `pi/digitsd/internal/phone/serial.go` (UART parsing) |
| Pi daemon: integration | `pi/digitsd/cmd/digitsd/main.go` (callbacks, signal dispatch, `MigrateToMesh`, `currentPeer`) |
| Server: data | `server/internal/calls/conference.go` (`ConferenceTracker`), `server/internal/calls/tracker.go` (persistence, history) |
| Server: signalling | `server/internal/signaling/conference.go` (handlers), `server/internal/signaling/relay.go` (`handleHangup`, `OnDisconnect`) |
| Webapp | `server/internal/web/templates/calls.html`, `server/internal/web/static/digits.css`, `dialup.css` |
