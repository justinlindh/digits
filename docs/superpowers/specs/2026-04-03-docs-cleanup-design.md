# Docs Cleanup for Public Release

## Goal

Clean up `docs/` for public repo release on GitHub. Primary audience: makers/hackers
who want to build their own Digits phone. Secondary: developers who want to contribute
or fork.

## Principles

- Restructure around audience needs (build, understand, host)
- Delete obsolete planning docs and internal dev notes
- Consolidate overlapping architecture docs into one
- Slim mission/vision docs that duplicate digits.family content
- Update all docs for current terminology (households/devices/lines, not phones/contacts/directory)
- Preserve existing voice where it's good; refine where it's not

## Delete

| File | Reason |
|------|--------|
| `pi-os-audit.md` | Recommendations already implemented in `tools/build-image.sh` |
| `easter-eggs-backlog.md` | Internal brainstorming, not public-facing |
| `electrocookie-layout.txt` | No longer accurate |
| `debugging/2026-03-23-webrtc-audio-debugging.md` | Session log; fix is in the code |
| `architecture/voip-call-path.md` | Largely obsolete; useful bits consolidated into `overview.md` |
| `architecture/networking-nat-traversal.md` | Consolidated into `overview.md` |
| `diagrams/cross-household-linking.md` | Consolidated into `overview.md` |
| `diagrams/electrocookie-*.png` (2 files) | Match deleted layout file |
| `diagrams/phone-fsm.puml` | Redundant with `.dot` source |
| `diagrams/phone-fsm-graphviz.png` | Redundant with `phone-fsm.png` |
| `diagrams/phone-fsm.svg` | Redundant with `phone-fsm.png` |
| `diagrams/img/04-call-permission.png` + `.svg` | Describes unimplemented feature |
| `diagrams/img/05-revocation-flow.png` + `.svg` | Describes unimplemented feature |

## New Directory Structure

```
docs/
├── README.md                          # NEW: navigation hub
├── mission.md                         # Slimmed, links to digits.family
├── why-digits.md                      # Slimmed, links to digits.family
├── build/
│   ├── components.md                  # Updated BOM
│   ├── wiring.md                      # Verified electrical spec
│   ├── datasheets.md                  # Light edit
│   ├── hardware-kill-switch.md        # Light edit
│   └── teardown/                      # Moved from docs/teardown/ + docs/photos/
│       ├── notes.md
│       └── photos/                    # 14 Sangyn 2500 JPGs + README
├── architecture/
│   ├── overview.md                    # NEW: consolidated architecture doc
│   └── uart-protocol.md              # Updated protocol spec
├── hosting/
│   └── self-hosting.md               # Updated deployment guide
└── diagrams/
    ├── phone-fsm.dot                  # FSM source (Graphviz)
    ├── phone-fsm.png                  # FSM rendered
    └── img/
        ├── 01-data-model.png + .svg
        ├── 02-linking-flow.png + .svg
        └── 03-system-overview.png + .svg
```

## New Files

### `docs/README.md`

Short navigation page. Four sections:

- **Build one** -- links to `build/components.md`, `build/wiring.md`
- **How it works** -- links to `architecture/overview.md`, `architecture/uart-protocol.md`
- **Run your own server** -- links to `hosting/self-hosting.md`
- **Why Digits?** -- links to digits.family/why and digits.family/how-it-works

### `docs/architecture/overview.md`

Consolidated from three old docs. Sections:

1. **System overview** -- component roles (Pico, Pi, Codec Zero, signaling server), reference `diagrams/img/03-system-overview.png`
2. **Data model** -- households, devices, lines, household links; reference `diagrams/img/01-data-model.png` and `02-linking-flow.png`
3. **Call path** -- WebRTC + Opus + SRTP, why chosen over SIP/RTP, Pion on Pi; outgoing/incoming/hangup sequence
4. **NAT traversal** -- ICE + STUN + TURN fallback strategy, coturn self-hosted, TURN-over-TLS on 443
5. **Current status** -- what works (LAN calls, E2E encrypted audio, 75-90ms latency), what's next (TURN integration in digitsd, reconnect behavior)

Source material:
- `voip-call-path.md` Section 3 (architecture rationale, component diagram, sequence diagram)
- `networking-nat-traversal.md` (STUN/TURN strategy, coturn, cost analysis)
- `cross-household-linking.md` (data model, linking flow, system overview diagram)
- Current `pi/digitsd/` code (verified implementation state)

## Edits to Existing Files

### `mission.md` + `why-digits.md`

Slim to 1-2 paragraphs each with a link to the full version on digits.family.
Keep the core thesis. Remove anything the website covers better.

### `build/components.md`

- Update procurement status (Codec Zero should be "on hand")
- Remove WM8960 references if present (replaced by Codec Zero DA7212)
- Update any old terminology

### `build/wiring.md`

- Verify GPIO pins and ALSA device names against current code
- Update old terminology
- Already marked "verified 2026-03-22" so likely minimal changes

### `build/datasheets.md`

- Light edit for terminology
- Verify component list matches current BOM

### `build/hardware-kill-switch.md`

- Light edit for terminology
- Circuit design unchanged

### `architecture/uart-protocol.md`

- Verify command/event list against current firmware source
- Update FSM state names if changed
- Update old terminology

### `hosting/self-hosting.md`

- Verify Docker Compose config and env vars against `server/docker/`
- Update any stale instructions
- Check that service names match current code (signald, admind)

### `build/teardown/` (moved)

- Move `docs/teardown/notes.md` to `docs/build/teardown/notes.md`
- Move `docs/photos/` to `docs/build/teardown/photos/`
- Update any internal links in `photos/README.md`
- No content changes

## Main README.md

Add a documentation section linking to:

```markdown
## Documentation

- [Why Digits?](https://digits.family/why) -- the problem and vision
- [How it works](https://digits.family/how-it-works) -- overview
- [Architecture](docs/architecture/overview.md) -- technical deep-dive
- [Build one](docs/build/components.md) -- BOM and hardware guide
- [Wiring](docs/build/wiring.md) -- full electrical spec
- [Self-hosting](docs/hosting/self-hosting.md) -- run your own server
```

## Implementation Notes

- The data model refactor (households/devices/lines replacing phones/contacts/directory) is
  landed and stable. All docs should use the new terminology.
- The cross-household-linking doc describes Phases 2-3 (contact invites, sync) that were
  never implemented. The consolidated overview should only document what's built, plus a
  brief "what's next" section for forward-looking items.
- voip-call-path.md Phases A-C are complete. Phase D is partial (busy/no-answer works,
  TURN not wired up in client). Phase E is partial (pairing works, no systemd service).
  The overview should reflect actual state.
- Server-side TURN credential generation is implemented but digitsd doesn't request ICE
  servers yet. Document this as a known gap in "what's next".
