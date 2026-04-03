# Digits Easter Eggs — Implementation Backlog

Phase: Post-MVP (after core calling works end-to-end)
Source: Research spike 2026-03-22, full report in `~/.openclaw/workspace/reports/digits-easter-egg-research-spike-device-only-feature-ideas.md`

## Hard Constraints
- No external services — everything runs on-device (Pico + Pi Zero)
- Must work within hardware: keypad, earpiece/speaker, LED, mechanical bell
- Must not undermine core mission (screen-free voice for kids)
- Auto-timeout after 2-3 minutes, no addictive reward loops

## Priority 1 — Prototype First

| # | Name | Fun | Complexity | Activation | Description |
|---|------|-----|-----------|------------|-------------|
| 1 | Bell Orchestra | 9/10 | Medium | Long-press `#` on-hook → lift | Rhythm copy game — bell plays pattern, repeat via keypad taps. Gets faster. |
| 2 | Secret Operators | 8/10 | Simple | Dial `00#` | Silly character skits (pirate, robot, haunted). Pre-recorded audio clips. |
| 3 | Friendship Streak | 8/10 | Medium | Automatic | Make a real call daily → celebratory bell fanfare after N-day streak. |

## Priority 2 — Build When Ready

| # | Name | Fun | Complexity | Activation | Description |
|---|------|-----|-----------|------------|-------------|
| 4 | Dial-a-Drum Machine | 8.5/10 | Medium | Dial `*2328` | Each key = percussion sound. `#` loops. Keypad beat pad. |
| 5 | Phantom Ring | 8/10 | Simple | `#13#` on-hook | Phone rings itself 10-30s later with goofy reveal. Kid-friendly prank. |
| 6 | Cradle Konami | 9/10 | Hard | Hook taps + `1986` | Hidden physical cheat code unlocks daily audio scene. Playground legend material. |

## Priority 3 — Nice to Have

| # | Name | Fun | Complexity | Activation | Description |
|---|------|-----|-----------|------------|-------------|
| 7 | Phone Fortune Cookie | 7.5/10 | Simple | Dial `*386` | Random audio prompt nudging real calls ("ask a friend their favorite snack"). |
| 8 | Time Wizard | 7/10 | Simple-Med | Dial `*8463` | Bell strikes the hour, chirps for minutes. Clock without speech. |
| 9 | Spy Decoder | 7/10 | Medium | Dial `*779` | Coded tone puzzles, enter answer on keypad. |

## Two-Player Ideas (Requires Active Call)
- Rhythm battle: both callers get the same pattern, compete on accuracy
- Keypad speed challenge: race to enter a sequence
- These require call-state integration — defer until calling works end-to-end

## Notes
- All activation codes chosen to be unlikely to collide with real phone numbers
- Easter eggs only accessible from specific FSM states (idle/dial-tone) to avoid interference with calls
- The Cradle Konami is the hardest to implement but would become the thing kids talk about at school
