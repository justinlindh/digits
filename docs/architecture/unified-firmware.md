# Unified firmware design

## Goal

Replace the build-time `HARDWARE_REV` split in the Pico firmware with a single binary that detects the board it is running on at boot and adapts at runtime. One ELF artifact per release. No more chance of flashing V1 firmware to V2 hardware (or vice versa).

## Motivation

Today the firmware is forked at compile time:

- `HARDWARE_REV=1`: V1 family (ElectroCookie prototype + V1 PCB).
- `HARDWARE_REV=2`: V2 carrier PCB.

Differences between the two builds are entirely GPIO pin assignments plus one boot2 stage selection. Every other line of firmware is identical.

The fork has caused several bugs already:

- Pin constants (HOOK_PIN, LED_PIN, KEYPAD_*, ringer pins) were hardcoded to V1 values; V2 PCB shipped with the firmware ignoring its actual wiring until each constant was made `HARDWARE_REV`-conditional one at a time.
- The CI release pipeline builds with default `HARDWARE_REV=1` and publishes a single ELF as `firmware-${VERSION}.elf`. Every release ships V1 firmware, the server hands the same URL to every Pi, and any V2 unit auto-flashing this OTA gets V1 firmware on V2 hardware (UART pin mismatch alone breaks all communication).
- The local build script reads `HARDWARE_REV` from environment only; passing `-DHARDWARE_REV=2` as a positional argument silently builds V1. This footgun has already cost a bench session chasing a phantom UART loopback bug.

A unified firmware closes all of these failure modes at once. The mechanism (JEDEC ID auto-detection plus a Pi-side override path) extends naturally to future board revisions without another fork.

## Non-goals

- Codec / mixer / audio path differences between V1 and V2: these live in the Pi SD image overlay (mixer state files, GPCLK0 service, codec sample-rate constraints), not in the Pico firmware. They stay where they are.
- digitsd OTA URL filtering: with one ELF per release, the existing "pick the .elf" code in the server is correct and needs no change.
- The `pcb_rev` marker file on the Pi: stays. It still drives image-side decisions (SWD pinout extraction, mixer state, codec config). The firmware no longer cares about it; the Pi-side image still does.

## Design

### Detection mechanism

Hybrid: JEDEC flash ID auto-detection at boot, with a Pi-side UART override available for future boards that share a flash chip with an existing rev.

JEDEC IDs of currently-deployed boards:

- W25Q080 (`0xEF4014`) on the Pi Pico module: V1 family.
- W25Q16JV (`0xEF4015`) on the V2 carrier: V2.

Auto-detection covers everything in the user's possession today. The override path is dormant code today, active when a future V3 ships with the same flash chip as V2 but a different pinout.

Unknown JEDEC IDs fall back to the most-recent profile (V2 today). This biases new hardware toward forward progress: a brand-new board that fails to match a known ID gets a V2 pinout by default, which is the safe assumption for V2-line successors.

### Boot2 unification

Standardize on `boot2_generic_03h` for all boards. It uses bare 0x03 single-bit reads, which work on every SPI flash chip we have or are likely to use. XIP throughput drops on V1 from approximately 24 MB/s to 5 MB/s. The firmware is roughly 80 KB total and is not bandwidth-bound (no large lookup tables, no streaming flash reads in hot paths). The slowdown is imperceptible.

### Pin map data structure

A single `board_profile_t` struct holds every per-rev value. All pin numbers and per-rev quirks live here. Modules read from a global `board` pointer instead of compile-time constants.

```c
// firmware/src/board.h

typedef struct {
    const char* name;          // "v1" / "v2"
    uint32_t jedec_id;         // expected JEDEC ID, 0 = wildcard fallback

    // UART (Pi to Pico)
    uint uart_tx_pin;
    uint uart_rx_pin;

    // Hookswitch
    uint hook_pin;

    // Status LED
    uint led_pin;

    // Keypad matrix
    uint keypad_rows[4];
    uint keypad_cols[4];
    uint keypad_num_cols;      // V1=4, V2=3

    // Ringer (DRV8871 H-bridge inputs)
    uint ringer_in1_pin;
    uint ringer_in2_pin;

    // Per-rev quirks
    bool needs_uart_tx_idle_workaround;
} board_profile_t;

extern const board_profile_t* board;

void board_init(void);  // reads JEDEC ID, installs profile pointer
```

Profiles defined as `static const` in `board.c`. New profile = new struct entry plus optional new JEDEC ID. No other firmware change.

### Init sequence

```c
int main(void) {
    // 1. Pre-bootstrap: drive both candidate UART_TX pins high so the Pi
    //    sees a clean idle line during the boot window. We don't know the
    //    profile yet so we cover both possibilities.
    gpio_init(0);  gpio_set_dir(0, GPIO_OUT);  gpio_put(0, 1);
    gpio_init(28); gpio_set_dir(28, GPIO_OUT); gpio_put(28, 1);

    // 2. Read JEDEC ID, install board profile.
    board_init();

    // 3. Release the pre-bootstrap pin we don't need.
    if (board->uart_tx_pin != 0)  gpio_deinit(0);
    if (board->uart_tx_pin != 28) gpio_deinit(28);

    // 4. Standard subsystem init (each reads from board->...).
    uart_proto_init();
    hook_init();
    led_init();
    keypad_init();
    ringer_init();
    phone_fsm_init();

    // 5. Main loop.
    while (true) { /* poll loop unchanged */ }
}
```

Step 1 is the only place raw GPIO numbers appear outside profile lookup. It is intentional: the JEDEC ID read in step 2 has not happened yet, so we cannot know which UART_TX pin to drive. Driving both costs nothing and protects the bootstrap window.

### Pi-side UART override

Firmware exposes a new UART command:

```
CONFIG:PCB_REV=N    -> ACK if N is a known profile, else NACK
                       Re-installs the profile and re-inits affected modules.
BOARD?              -> BOARD:<name>:<jedec_id_hex>
                       Reports the active profile.
```

digitsd reads its own `pcb_rev` at startup, queries `BOARD?`, and sends `CONFIG:PCB_REV=N` if the answers disagree. Today they always agree (auto-detection is perfect for V1 / V2). The override exists for V3+ boards that share flash with V2 but need different pinouts.

## CI and release plumbing

- `tools/build-firmware.sh`: drops the per-rev concept. Builds once, outputs `artifacts/firmware-${VERSION}.elf`.
- `firmware/.releaserc-full.cjs`: publishes one ELF and one SHA256 file per release.
- `firmware/CMakeLists.txt`: removes the `HARDWARE_REV` cache var, the per-rev boot2 branch, the `HARDWARE_REV=...` define. Boot2 hardcoded to `boot2_generic_03h`.
- `firmware/Makefile`: drops the `HARDWARE_REV` env var threading.
- `scripts/build.sh`: drops `HARDWARE_REV` env var handling and the `-DHARDWARE_REV=...` cmake arg.
- Top-level `Makefile`: removes `firmware-v1` / `firmware-v2` / `firmware-v1-local` / `firmware-v2-local` (added today as a stopgap). Goes back to single `firmware` and `firmware-local` targets.

Net change is mostly deletion. Fewer build paths, fewer chances to ship the wrong artifact.

## digitsd changes

- New `BOARD?` query at startup. Logs the firmware-reported board name. Cross-checks against the on-disk `pcb_rev`; warns on mismatch.
- New `CONFIG:PCB_REV=N` send when `BOARD?` and `pcb_rev` disagree (currently never happens; future-proofing only).
- OTA URL-picking code unchanged. Already picks "the .elf"; one ELF per release means it picks correctly.

## ERRATA and docs cleanup

V2 ERRATA items affected by this change:

| Item | Status after unified firmware |
|---|---|
| 2. UART_TX_PI pullup | Demoted from required to optional. Firmware-side `needs_uart_tx_idle_workaround` flag stays on for V2 profile. The hardware pullup becomes a "nice to have" so the line stays high even with no firmware running. |
| 3. boot2 chip mismatch | Closed. Unified firmware uses `boot2_generic_03h` which works on the existing W25Q16JV. No flash chip swap needed. |
| 9. Firmware HOOK_PIN GP10 vs GP20 | Closed. Subsumed into the unified firmware refactor. |

Items 1, 4, 6, 7, 8 still need their respective V2.1 or V3 PCB work.

`hardware/pcb/v2.1/CHANGES_FROM_V2.md`: drop the flash chip swap section, demote the UART pullup section. V2.1 KiCad scope shrinks to component-side flip, J6 polarity swap (J8 reassignment is already done in the V2.1 schematic).

`#313` (V2.1 KiCad work issue): update checklist to remove the flash chip swap and demote the UART pullup.

## Migration plan

Each step independently verifiable on hardware. Worst-case rollback is reverting one commit.

1. **Add `board.h` / `board.c` with both profiles, but keep `HARDWARE_REV` working.** New module that nothing depends on. JEDEC ID detection is implemented and exercised, but the existing constants still drive the build. Verify on bench: `BOARD?` returns "v1" on V1 PCB and "v2" on V2 carrier.

2. **Migrate one module at a time to read from `board->...`.** Start with `led.c`. Then `hook.c`, `keypad.c`, `ringer.c`, `uart_proto.c`. After each module: build, flash, verify both boards still work.

3. **Drop `boot2_w25q080`, standardize on `boot2_generic_03h`.** One-line CMakeLists change. The riskiest step on V1, since `boot2_generic_03h` running on the W25Q080 is the only new combination. Easy to roll back.

4. **Remove the `HARDWARE_REV` build-time override.** Delete the cache var, env var threading, `firmware-v1` / `firmware-v2` Make targets. The per-rev `#if` blocks in `hook.h`, `led.h`, `keypad.h`, `ringer.h`, `uart_proto.h` should be empty after step 2; remove the conditional shells.

5. **Update CI and release plumbing.** Single ELF artifact, single Make target. Cut a fresh firmware release.

6. **Update digitsd.** Add `BOARD?` query at startup, log result, cross-check against `pcb_rev`. Wire up the dormant `CONFIG:PCB_REV=N` UART command.

7. **ERRATA and docs cleanup.** Close V2 ERRATA items 3 and 9. Demote item 2 to optional. Update V2.1 CHANGES doc and #313 to reflect shrunk KiCad scope.

## Risks

- **`boot2_generic_03h` on the W25Q080 (V1 PCB)** is a combination not currently exercised in CI or on the bench. If it fails to set up XIP correctly the V1 chip enters the same reset loop V2 originally hit with the wrong boot2. Step 3 of the migration plan exists specifically to validate this on hardware before any further changes. Rollback: revert the CMakeLists change.
- **Unknown JEDEC ID on a brand-new board** falls through to the V2 profile. If a future board is wired V1-style but has a non-W25Q080 flash chip (unlikely given V1 is being retired), the auto-detect would silently mis-configure. Mitigation: digitsd logs the firmware-reported board name at startup and warns on mismatch with `pcb_rev`. The Pi-side override (`CONFIG:PCB_REV=N`) corrects without a firmware re-release.
- **JEDEC ID read timing.** The SDK exposes flash JEDEC ID through `flash_get_unique_id` and friends, but those run during XIP. Reading JEDEC ID at the very top of `main()` is supported by the SDK; if it isn't, the implementation drops to a manual SSI command. Easy to verify in step 1.
