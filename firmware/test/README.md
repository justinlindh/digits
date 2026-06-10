# Firmware host tests

Native unit tests for the pure-logic firmware modules. They run on the host
with a plain C compiler and do NOT require the Pico SDK or the ARM toolchain, so
they are fast to run locally and cheap to gate in CI.

## Run

From `firmware/`:

```
make test
```

Or directly with CMake:

```
cmake -S test -B test/build
cmake --build test/build
./test/build/fw_tests
```

The runner exits nonzero if any check fails.

## What is covered

- `hook.c`: 50ms debounce stability window, the flash-vs-hangup classifier over
  the [100, 600]ms window, sub-100ms bounce suppression, the past-600ms timeout
  to a real hangup, mid-window flash-disable commit, polarity inversion re-sync,
  and software force/release re-sync.
- `keypad.c`: distinct-key acceptance fires once per press, same-key repeat is
  suppressed until release, and a distinct key within the 80ms debounce window
  is rejected until the window elapses.
- `uart_proto.c`: RX line framing across fragments, CR/LF and blank-line
  handling, the 127-byte max line, and overflow discard-plus-resync so an
  oversized line cannot corrupt the following one.
- `phone_fsm.c`: the state transition table (idle/dial-tone/dialing/ringing/
  connected/busy paths driven by hook events and Pi commands) and the
  `buf_appendf` saturating accumulator (truncation boundary, no size_t
  underflow, zero-size no-op).

## How it works

Production sources are compiled unmodified against fake Pico SDK headers under
`fakes/` (`pico/...`, `hardware/...`). Those shims forward to a controllable
virtual clock and GPIO array (`fakes/fake_env.c`) plus a fake UART RX queue
(`fakes/fake_uart.c`). The hardware-bound modules the FSM depends on (board
profile, LED, phase) have in-memory fakes (`fakes/fake_board.c`,
`fakes/fake_led.c`, `fakes/fake_phase.c`) that expose state for assertions. The
real `ringer.c` and `tone.c` link directly.

`test_fsm.c` includes `phone_fsm.c` directly rather than linking it separately,
so the tests can reach the file-static `buf_appendf` and `s_state` while still
exercising the public `phone_fsm_update()` path.

The harness is a dependency-free header (`test_harness.h`): `CHECK`, `CHECK_EQ`,
and `CHECK_STREQ` macros plus a small runner in `test_main.c`.
