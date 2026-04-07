#include "hook.h"

#include "hardware/gpio.h"
#include "pico/time.h"

// Polled debounce: read the pin every update cycle and only
// register a state change after the pin has been stable for
// DEBOUNCE_MS consecutive milliseconds.
#define DEBOUNCE_MS 50

static bool s_off_hook = false;       // Committed (debounced) physical state
static bool s_raw_last = false;       // Last raw physical reading
static absolute_time_t s_stable_since;
static bool s_event_pending = false;
static bool s_event_off_hook = false;

// Software override mode/state.
static bool s_force_mode = false;
static bool s_forced_state = false;

// Invert mode: when true, LOW = off-hook (PCB carrier board tactile switch).
static bool s_inverted = false;

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
    s_event_pending = false;

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

    // Pin is same as last read — check if stable long enough.
    if (raw != s_off_hook) {
        int64_t stable_ms = absolute_time_diff_us(s_stable_since,
                                                  get_absolute_time()) / 1000;
        if (stable_ms >= DEBOUNCE_MS) {
            s_off_hook = raw;
            s_event_off_hook = raw;
            s_event_pending = true;
        }
    }
}

bool hook_is_off_hook(void) {
    if (s_force_mode) {
        return s_forced_state;
    }
    return s_off_hook;
}

bool hook_get_event(bool *off_hook) {
    if (!s_event_pending) {
        return false;
    }

    if (off_hook != NULL) {
        *off_hook = s_event_off_hook;
    }
    s_event_pending = false;
    return true;
}

void hook_force_off_hook(bool off_hook) {
    bool prev_effective = hook_is_off_hook();

    s_force_mode = true;
    s_forced_state = off_hook;

    // In force mode, events should reflect forced state.
    s_event_pending = false;
    if (off_hook != prev_effective) {
        s_event_off_hook = off_hook;
        s_event_pending = true;
    }
}

void hook_clear_force(void) {
    bool prev_effective = hook_is_off_hook();
    bool physical = read_physical_off_hook();

    s_force_mode = false;

    // Re-sync debounce state with physical pin.
    s_raw_last = physical;
    s_off_hook = physical;
    s_stable_since = get_absolute_time();

    // Clear any stale forced event and emit transition if effective state changed.
    s_event_pending = false;
    if (physical != prev_effective) {
        s_event_off_hook = physical;
        s_event_pending = true;
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
    s_event_pending = false;
}

bool hook_is_inverted(void) {
    return s_inverted;
}
