// Behavioral tests for keypad.c debounce.
//
// The matrix is scanned row by row: a row is driven LOW, then each column is
// read; a pressed key pulls its column LOW. keypad_scan() returns a key once,
// on a fresh distinct press, and only if KEYPAD_DEBOUNCE_MS (80ms) has elapsed
// since the previous accepted press. Releasing all keys (no column low) resets
// the last-key latch so the same key can be pressed again.
//
// The fake V2 profile uses rows GP{2,3,4,5} and cols GP{6,7,8}, num_cols=3,
// with the standard 4x3 telephone keychars. A pressed key (row, col) is
// modeled by holding that column LOW while the matching row is driven LOW.
// Because keypad_scan_raw() drives one row low at a time and restores it high,
// we model a held key as: column C is LOW. That reports a press in EVERY row
// that is scanned, so to pin down a specific key we instead drive the column
// low only while its row is selected. The fake can't see which row is active
// mid-scan, so we emulate a single held key by setting the column low; the
// last row scanned with that column low wins. To get deterministic keys we
// press keys whose column is unique per intended row by toggling the column
// around the scan. Simpler and faithful: hold a column low and accept that the
// reported char is the bottom-most row sharing that column. We use that mapping
// explicitly below.

#include "test_harness.h"

#include "fake_env.h"
#include "fakes/fake_modules.h"
#include "keypad.h"

#define COL0 6
#define COL1 7
#define COL2 8

// Release: no column held low.
static void release_keys(void) {
    fake_gpio_set_level(COL0, true);
    fake_gpio_set_level(COL1, true);
    fake_gpio_set_level(COL2, true);
}

// Hold exactly one column low. keypad_scan_raw drives each row low in turn and
// reads the columns; with a single column held low the press is reported for
// every row, so the final reported key is the LAST row's char in that column.
// For the V2 keychars that is row 3 (index 3): col0='*', col1='0', col2='#'.
static char held_key_for_col(int col) {
    if (col == COL0) return '*';
    if (col == COL1) return '0';
    if (col == COL2) return '#';
    return '\0';
}

static void hold_col(int col) {
    release_keys();
    fake_gpio_set_level(col, false);
}

static void test_keypad_distinct_key_accepted_once(void) {
    fake_env_reset();
    fake_board_use_v2();
    release_keys();
    keypad_init();

    // Advance well past the debounce window so the first press is eligible.
    fake_clock_advance_ms(200);

    hold_col(COL1);  // '0'
    char k = keypad_scan();
    CHECK_EQ(k, held_key_for_col(COL1));

    // Holding the same key down: subsequent scans return '\0' (no repeat).
    CHECK_EQ(keypad_scan(), '\0');
    fake_clock_advance_ms(500);
    CHECK_EQ(keypad_scan(), '\0');
}

static void test_keypad_same_key_after_release(void) {
    fake_env_reset();
    fake_board_use_v2();
    release_keys();
    keypad_init();
    fake_clock_advance_ms(200);

    hold_col(COL0);  // '*'
    CHECK_EQ(keypad_scan(), '*');

    // Release: latch clears.
    release_keys();
    CHECK_EQ(keypad_scan(), '\0');

    // Press the same key again after the debounce interval: accepted again.
    fake_clock_advance_ms(200);
    hold_col(COL0);
    CHECK_EQ(keypad_scan(), '*');
}

static void test_keypad_distinct_key_within_debounce_rejected(void) {
    fake_env_reset();
    fake_board_use_v2();
    release_keys();
    keypad_init();
    fake_clock_advance_ms(200);

    // Accept '0'.
    hold_col(COL1);
    CHECK_EQ(keypad_scan(), '0');

    // Without releasing, a DIFFERENT key appears only 40ms later (< 80ms
    // KEYPAD_DEBOUNCE_MS). The distinct-key branch requires the debounce
    // interval since the last accepted press, so this is rejected.
    fake_clock_advance_ms(40);
    hold_col(COL2);  // '#'
    CHECK_EQ(keypad_scan(), '\0');

    // After the full debounce interval elapses, the still-held distinct key
    // is accepted.
    fake_clock_advance_ms(80);
    CHECK_EQ(keypad_scan(), '#');
}

static void test_keypad_no_press_returns_null(void) {
    fake_env_reset();
    fake_board_use_v2();
    release_keys();
    keypad_init();
    fake_clock_advance_ms(200);

    for (int i = 0; i < 5; ++i) {
        CHECK_EQ(keypad_scan(), '\0');
        fake_clock_advance_ms(50);
    }
}

#define T(fn) {#fn, fn}
static const test_case_t k_keypad_tests[] = {
    T(test_keypad_distinct_key_accepted_once),
    T(test_keypad_same_key_after_release),
    T(test_keypad_distinct_key_within_debounce_rejected),
    T(test_keypad_no_press_returns_null),
};
#undef T

const test_case_t *keypad_tests(int *count);
const test_case_t *keypad_tests(int *count) {
    *count = (int)(sizeof(k_keypad_tests) / sizeof(k_keypad_tests[0]));
    return k_keypad_tests;
}
