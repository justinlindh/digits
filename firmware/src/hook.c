#include "hook.h"

#include "hardware/gpio.h"
#include "pico/time.h"

// Polled debounce: read the pin every update cycle and only
// register a state change after the pin has been stable for
// DEBOUNCE_MS consecutive milliseconds.
#define DEBOUNCE_MS 50

// Flash detection: an on→off→on sequence where the "off" duration is within
// [FLASH_MIN_MS, FLASH_MAX_MS] is treated as a hook-flash, not a hangup.
// Shorter durations are bounce (ignored). Longer durations commit to HOOK_EVENT_OFF.
#define FLASH_MIN_MS 100
#define FLASH_MAX_MS 600

static bool s_off_hook = false;       // Committed (debounced) physical state
static bool s_raw_last = false;       // Last raw physical reading
static absolute_time_t s_stable_since;

// Flash-detection window state.
// When the debouncer first commits to "off-hook", we don't emit HOOK_EVENT_OFF
// immediately. Instead we set s_flash_pending and record the transition time.
// If the hook returns to on-hook within FLASH_MAX_MS we emit HOOK_EVENT_FLASH
// (or suppress it if under FLASH_MIN_MS). If it stays off past FLASH_MAX_MS
// we emit HOOK_EVENT_OFF.
static bool s_flash_pending = false;
static absolute_time_t s_flash_start;

static hook_event_t s_event = HOOK_EVENT_NONE;

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

            if (raw) {
                // Debounced transition to off-hook. Don't emit HOOK_EVENT_OFF yet;
                // start the flash-detection window instead.
                s_flash_pending = true;
                s_flash_start = get_absolute_time();
            } else {
                // Debounced transition to on-hook.
                if (s_flash_pending) {
                    // The hook returned to on-hook while the flash window is open.
                    int64_t off_ms = absolute_time_diff_us(s_flash_start,
                                                           get_absolute_time()) / 1000;
                    s_flash_pending = false;
                    if (off_ms >= FLASH_MIN_MS && off_ms <= FLASH_MAX_MS) {
                        s_event = HOOK_EVENT_FLASH;
                    }
                    // Shorter than FLASH_MIN_MS: bounce, suppress.
                } else {
                    // Normal hangup from a committed off-hook state.
                    s_event = HOOK_EVENT_ON;
                }
            }
        }
    }

    // While a flash window is open, check whether the timeout has expired
    // without the hook returning to on-hook (commits to a real off-hook event).
    if (s_flash_pending) {
        int64_t off_ms = absolute_time_diff_us(s_flash_start,
                                               get_absolute_time()) / 1000;
        if (off_ms > FLASH_MAX_MS) {
            s_flash_pending = false;
            s_event = HOOK_EVENT_OFF;
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
