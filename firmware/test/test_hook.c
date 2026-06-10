// Behavioral tests for hook.c flash-vs-hangup classification.
//
// The hookswitch reads a GPIO (board->hook_pin == 20 in the fake V2 profile).
// Non-inverted polarity: HIGH == off-hook (handset lifted), LOW == on-hook
// (cradled), via the internal pull-up. hook_poll() debounces over DEBOUNCE_MS
// (50ms) and, when flash detection is enabled, classifies a brief on-hook
// pulse in [FLASH_MIN_MS, FLASH_MAX_MS] == [100, 600]ms as a flash rather than
// a hangup.

#include "test_harness.h"

#include "fake_env.h"
#include "hook.h"

#define HOOK_PIN 20

// Drive the raw pin and let hook_poll() observe it for `ms` of virtual time,
// stepping in 10ms ticks like the real 10ms main loop.
static void hold_level_for_ms(bool high, int ms) {
    fake_gpio_set_level(HOOK_PIN, high);
    for (int t = 0; t < ms; t += 10) {
        fake_clock_advance_ms(10);
        hook_poll();
    }
}

// Reset the hook module's mode flags. hook_init() resets debounce/force state
// but not the inverted/flash gates, so tests clear them explicitly to stay
// isolated from one another's mode changes.
static void reset_hook_modes(void) {
    hook_set_inverted(false);
    hook_set_flash_enabled(false);
    hook_clear_force();
    hook_get_event();
}

// Bring the hook to a committed off-hook state from a clean init.
static void settle_off_hook(void) {
    fake_env_reset();
    fake_uart_rx_reset();
    // Pin high at init => off-hook latched immediately.
    fake_gpio_set_level(HOOK_PIN, true);
    hook_init();
    reset_hook_modes();
    // Drain any init event.
    hook_get_event();
}

static void test_hook_debounce_requires_stable_window(void) {
    fake_env_reset();
    fake_gpio_set_level(HOOK_PIN, false);  // on-hook at init
    hook_init();
    reset_hook_modes();
    hook_get_event();
    CHECK(!hook_is_off_hook());

    // Lift the handset but only let it settle for 30ms (< 50ms DEBOUNCE_MS):
    // the transition must NOT commit yet.
    hold_level_for_ms(true, 30);
    CHECK(!hook_is_off_hook());
    CHECK_EQ(hook_get_event(), HOOK_EVENT_NONE);

    // Continue holding past the 50ms window: now it commits to off-hook.
    hold_level_for_ms(true, 40);
    CHECK(hook_is_off_hook());
    CHECK_EQ(hook_get_event(), HOOK_EVENT_OFF);
}

static void test_hook_flash_disabled_is_instant_hangup(void) {
    settle_off_hook();
    hook_set_flash_enabled(false);

    // Cradle the handset; with flash disabled the on-hook commit fires
    // HOOK_EVENT_ON immediately (after debounce), no window.
    hold_level_for_ms(false, 70);
    CHECK(!hook_is_off_hook());
    CHECK_EQ(hook_get_event(), HOOK_EVENT_ON);
    CHECK(!hook_is_flash_pending());
}

static void test_hook_flash_in_window_emits_flash(void) {
    settle_off_hook();
    hook_set_flash_enabled(true);

    // Depress the hook (on-hook) long enough to debounce-commit. The commit
    // opens the flash window instead of emitting ON.
    hold_level_for_ms(false, 70);
    CHECK(hook_is_flash_pending());
    CHECK_EQ(hook_get_event(), HOOK_EVENT_NONE);

    // The flash window opened at the debounce-commit instant. We have already
    // burned 70ms of on-hook time. Keep on-hook until total on-duration lands
    // inside [100, 600]ms, then lift. 70 + 200 = 270ms: a valid flash.
    hold_level_for_ms(false, 200);
    // Lift back to off-hook and let it debounce-commit; the window measures
    // the on-hook duration and classifies it as a flash.
    hold_level_for_ms(true, 70);
    CHECK_EQ(hook_get_event(), HOOK_EVENT_FLASH);
    CHECK(!hook_is_flash_pending());
    CHECK(hook_is_off_hook());
}

static void test_hook_short_pulse_suppressed_as_bounce(void) {
    settle_off_hook();
    hook_set_flash_enabled(true);

    // Open the window with the minimum debounce hold of ~50-60ms, which is
    // under FLASH_MIN_MS (100ms), then immediately lift. The measured on-hook
    // duration is below 100ms => suppressed as contact bounce, no event.
    fake_gpio_set_level(HOOK_PIN, false);
    // Exactly 50ms to commit the on-hook transition and open the window.
    for (int t = 0; t < 60; t += 10) {
        fake_clock_advance_ms(10);
        hook_poll();
    }
    CHECK(hook_is_flash_pending());
    // Lift immediately. Total on-hook time ~60ms < FLASH_MIN_MS.
    hold_level_for_ms(true, 70);
    CHECK_EQ(hook_get_event(), HOOK_EVENT_NONE);
    CHECK(!hook_is_flash_pending());
    CHECK(hook_is_off_hook());
}

static void test_hook_long_pulse_times_out_to_hangup(void) {
    settle_off_hook();
    hook_set_flash_enabled(true);

    // Depress and hold on-hook well past FLASH_MAX_MS (600ms). The timeout
    // branch commits to a real hangup (HOOK_EVENT_ON) without waiting for a
    // lift.
    hold_level_for_ms(false, 70);  // open window
    CHECK(hook_is_flash_pending());
    hook_get_event();

    // Stay on-hook past 600ms total. 70ms already elapsed; add 600 more.
    hold_level_for_ms(false, 600);
    CHECK_EQ(hook_get_event(), HOOK_EVENT_ON);
    CHECK(!hook_is_flash_pending());
}

static void test_hook_flash_disabled_midwindow_commits_on(void) {
    settle_off_hook();
    hook_set_flash_enabled(true);

    hold_level_for_ms(false, 70);  // open flash window
    CHECK(hook_is_flash_pending());
    hook_get_event();

    // Pi disables flash mid-window. The open window must resolve to a hangup
    // so the caller does not sit in a stale pending state.
    hook_set_flash_enabled(false);
    CHECK(!hook_is_flash_pending());
    CHECK_EQ(hook_get_event(), HOOK_EVENT_ON);
}

static void test_hook_invert_resyncs_state(void) {
    // With inversion ON, LOW == off-hook. Start cradled (HIGH) in normal sense
    // then invert: the firmware must re-sync to reflect the new polarity.
    fake_env_reset();
    fake_gpio_set_level(HOOK_PIN, true);  // HIGH
    hook_init();
    reset_hook_modes();
    hook_get_event();
    CHECK(hook_is_off_hook());  // HIGH == off-hook (non-inverted)

    hook_set_inverted(true);
    // Now HIGH means on-hook. State must have re-synced to on-hook with no
    // spurious debounce delay, and any stale event cleared.
    CHECK(!hook_is_off_hook());
    CHECK(hook_is_inverted());
    CHECK_EQ(hook_get_event(), HOOK_EVENT_NONE);

    // A LOW reading now means off-hook under inversion.
    hold_level_for_ms(false, 70);
    CHECK(hook_is_off_hook());
}

static void test_hook_force_and_release_resync(void) {
    settle_off_hook();  // physical + committed off-hook

    // Force on-hook in software: effective state flips, event emitted.
    hook_force_off_hook(false);
    CHECK(hook_is_forced());
    CHECK(!hook_is_off_hook());
    CHECK_EQ(hook_get_event(), HOOK_EVENT_ON);

    // While forced, physical pin changes are ignored.
    hold_level_for_ms(false, 100);
    hook_force_off_hook(false);  // no change
    CHECK_EQ(hook_get_event(), HOOK_EVENT_NONE);

    // Release: re-sync to the physical pin. Pin is currently LOW (on-hook),
    // which matches the forced state, so no transition event.
    hook_clear_force();
    CHECK(!hook_is_forced());
    CHECK(!hook_is_off_hook());
    CHECK_EQ(hook_get_event(), HOOK_EVENT_NONE);

    // Now drive the physical pin high and release-resync via a fresh force
    // cycle: releasing while physical != effective emits the transition.
    hook_force_off_hook(false);  // force on-hook
    hook_get_event();
    fake_gpio_set_level(HOOK_PIN, true);  // physical now off-hook
    hook_clear_force();
    CHECK(hook_is_off_hook());
    CHECK_EQ(hook_get_event(), HOOK_EVENT_OFF);
}

static const test_case_t k_hook_tests[] = {
    TEST_CASE(test_hook_debounce_requires_stable_window),
    TEST_CASE(test_hook_flash_disabled_is_instant_hangup),
    TEST_CASE(test_hook_flash_in_window_emits_flash),
    TEST_CASE(test_hook_short_pulse_suppressed_as_bounce),
    TEST_CASE(test_hook_long_pulse_times_out_to_hangup),
    TEST_CASE(test_hook_flash_disabled_midwindow_commits_on),
    TEST_CASE(test_hook_invert_resyncs_state),
    TEST_CASE(test_hook_force_and_release_resync),
};

DEFINE_SUITE(hook)
