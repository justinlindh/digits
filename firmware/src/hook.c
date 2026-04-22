#include "hook.h"

#include "hardware/gpio.h"
#include "pico/time.h"

// Polled debounce: read the pin every update cycle and only
// register a state change after the pin has been stable for
// DEBOUNCE_MS consecutive milliseconds.
#define DEBOUNCE_MS 50

// Flash detection: a brief on-hook pulse during an off-hook call, where the
// on-hook duration is within [FLASH_MIN_MS, FLASH_MAX_MS], is treated as a
// hook-flash rather than a hangup. Shorter durations are contact bounce
// (ignored). Longer durations commit to HOOK_EVENT_ON (real hangup).
#define FLASH_MIN_MS 100
#define FLASH_MAX_MS 600

static bool s_off_hook = false;       // Committed (debounced) physical state
static bool s_raw_last = false;       // Last raw physical reading
static absolute_time_t s_stable_since;

// Flash-detection window state.
// When the debouncer commits to "on-hook" during an active off-hook state, we
// don't emit HOOK_EVENT_ON immediately. Instead we set s_flash_pending and
// record the transition time. If the hook returns to off-hook within
// [FLASH_MIN_MS, FLASH_MAX_MS] we emit HOOK_EVENT_FLASH (or suppress as bounce
// below FLASH_MIN_MS). If the hook stays on past FLASH_MAX_MS we emit
// HOOK_EVENT_ON (real hangup).
static bool s_flash_pending = false;
static absolute_time_t s_flash_start;

static hook_event_t s_event = HOOK_EVENT_NONE;

// Software override mode/state.
static bool s_force_mode = false;
static bool s_forced_state = false;

// Invert mode: when true, LOW = off-hook (PCB carrier board tactile switch).
static bool s_inverted = false;

// Flash-detection gate. When false, no flash window is opened on transition to
// on-hook -- HOOK_EVENT_ON fires immediately so hangup is instantaneous. When
// true, the 100-600ms window is used to distinguish a flash from a hangup. The
// Pi daemon toggles this via HOOK:FLASH:ON / HOOK:FLASH:OFF, enabling it only
// while the phone is in an active call state.
static bool s_flash_enabled = false;

static bool read_physical_off_hook(void) {
    bool raw = (gpio_get(HOOK_PIN) != 0);
    return s_inverted ? !raw : raw;
}

void hook_init(void) {
    gpio_init(HOOK_PIN);
    gpio_set_dir(HOOK_PIN, GPIO_IN);
    gpio_pull_up(HOOK_PIN);

    // Read initial physical state.
    s_raw_last = read_physical_off_hook();
    s_off_hook = s_raw_last;
    s_stable_since = get_absolute_time();
    s_flash_pending = false;
    s_event = HOOK_EVENT_NONE;

    s_force_mode = false;
    s_forced_state = false;
}

void hook_poll(void) {
    // Ignore physical updates while override mode is active.
    if (s_force_mode) {
        return;
    }

    bool raw = read_physical_off_hook();

    if (raw != s_raw_last) {
        // Pin changed — reset stability timer.
        s_raw_last = raw;
        s_stable_since = get_absolute_time();
        return;
    }

    // Pin is same as last read. Check if it has been stable long enough to
    // be a committed debounced transition.
    if (raw != s_off_hook) {
        int64_t stable_ms = absolute_time_diff_us(s_stable_since,
                                                  get_absolute_time()) / 1000;
        if (stable_ms >= DEBOUNCE_MS) {
            // Debounced transition detected.
            s_off_hook = raw;

            // Hook-switch mapping (after s_inverted normalization):
            //   raw == true  => handset LIFTED  (off-hook)
            //   raw == false => handset CRADLED (on-hook)
            //
            // A hook-flash is a brief on-hook pulse DURING an active off-hook call:
            //   off-hook (true) -> on-hook (false, 100-600 ms) -> off-hook (true)
            //
            // Detection strategy:
            //   1. Debounced transition to on-hook (raw == false) while we were
            //      previously off-hook: don't emit HOOK_EVENT_ON yet. Open a
            //      flash-detection window and record the time (s_flash_start).
            //      If the handset returns to off-hook within
            //      [FLASH_MIN_MS, FLASH_MAX_MS] we emit HOOK_EVENT_FLASH. If it
            //      stays on-hook past FLASH_MAX_MS we emit HOOK_EVENT_ON
            //      (checked in the timeout block below).
            //   2. Debounced transition to off-hook (raw == true) while the
            //      window is open: measure the on-hook duration. Emit FLASH if
            //      in range, or suppress as contact bounce if under
            //      FLASH_MIN_MS. If the window is not open the user is simply
            //      lifting the handset from the cradle: emit HOOK_EVENT_OFF.
            if (!raw) {
                if (s_flash_enabled) {
                    // Transition to on-hook while flash-capable: open the
                    // flash-detection window instead of emitting HOOK_EVENT_ON
                    // immediately (see comment above).
                    s_flash_pending = true;
                    s_flash_start = get_absolute_time();
                } else {
                    // Flash disabled: hangup is instantaneous.
                    s_event = HOOK_EVENT_ON;
                }
            } else {
                // Transition to off-hook.
                if (s_flash_pending) {
                    // Off-hook arrived while flash window is open: measure
                    // the on-hook duration.
                    int64_t on_ms = absolute_time_diff_us(s_flash_start,
                                                          get_absolute_time()) / 1000;
                    s_flash_pending = false;
                    if (on_ms >= FLASH_MIN_MS && on_ms <= FLASH_MAX_MS) {
                        s_event = HOOK_EVENT_FLASH;
                    }
                    // Under FLASH_MIN_MS: contact bounce, suppress.
                } else {
                    // Normal pickup from committed on-hook state.
                    s_event = HOOK_EVENT_OFF;
                }
            }
        }
    }

    // While a flash window is open, check whether the timeout has expired
    // without the hook returning to off-hook (commits to a real hangup).
    if (s_flash_pending) {
        int64_t on_ms = absolute_time_diff_us(s_flash_start,
                                              get_absolute_time()) / 1000;
        if (on_ms > FLASH_MAX_MS) {
            s_flash_pending = false;
            s_event = HOOK_EVENT_ON;
        }
    }
}

bool hook_is_off_hook(void) {
    if (s_force_mode) {
        return s_forced_state;
    }
    return s_off_hook;
}

hook_event_t hook_get_event(void) {
    hook_event_t ev = s_event;
    s_event = HOOK_EVENT_NONE;
    return ev;
}

void hook_force_off_hook(bool off_hook) {
    bool prev_effective = hook_is_off_hook();

    s_force_mode = true;
    s_forced_state = off_hook;
    s_flash_pending = false;

    // In force mode, events should reflect forced state.
    s_event = HOOK_EVENT_NONE;
    if (off_hook != prev_effective) {
        s_event = off_hook ? HOOK_EVENT_OFF : HOOK_EVENT_ON;
    }
}

void hook_clear_force(void) {
    bool prev_effective = hook_is_off_hook();
    bool physical = read_physical_off_hook();

    s_force_mode = false;
    s_flash_pending = false;

    // Re-sync debounce state with physical pin.
    s_raw_last = physical;
    s_off_hook = physical;
    s_stable_since = get_absolute_time();

    // Clear any stale forced event and emit transition if effective state changed.
    s_event = HOOK_EVENT_NONE;
    if (physical != prev_effective) {
        s_event = physical ? HOOK_EVENT_OFF : HOOK_EVENT_ON;
    }
}

bool hook_is_forced(void) {
    return s_force_mode;
}

void hook_set_inverted(bool inverted) {
    if (inverted == s_inverted) {
        return;
    }
    s_inverted = inverted;

    // Re-sync debounce state with the new interpretation.
    bool physical = read_physical_off_hook();
    s_raw_last = physical;
    s_off_hook = physical;
    s_stable_since = get_absolute_time();
    s_flash_pending = false;
    s_event = HOOK_EVENT_NONE;
}

bool hook_is_inverted(void) {
    return s_inverted;
}

void hook_set_flash_enabled(bool enabled) {
    if (enabled == s_flash_enabled) {
        return;
    }
    s_flash_enabled = enabled;
    if (!enabled && s_flash_pending) {
        // Close an open flash window: commit to on-hook so the user doesn't
        // sit in a stale pending state after flash is disabled mid-cycle.
        s_flash_pending = false;
        s_event = HOOK_EVENT_ON;
    }
}

bool hook_is_flash_enabled(void) {
    return s_flash_enabled;
}

bool hook_is_flash_pending(void) {
    return s_flash_pending;
}
