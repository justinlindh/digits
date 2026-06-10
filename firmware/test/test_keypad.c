// Behavioral tests for keypad.c debounce.
//
// The matrix is scanned row by row: a row is driven LOW, then each column is
// read; a pressed key pulls its column LOW. keypad_scan() returns a key once,
// on the press edge. A held key produces no repeat. A release is confirmed only
// after KEYPAD_DEBOUNCE_MS (80ms) of stable open, which rejects same-key
// contact bounce (a brief mid-press open that re-closes inside the window never
// confirms the release, so the re-close stays a held no-op). Once a release is
// confirmed the latch clears and a fresh press of any key is accepted
// immediately, so distinct-digit dialing is not throttled. A different key
// arriving while one is still believed held is accepted only after the debounce
// window since the last accept, rejecting electrical cross-talk.
//
// The fake V2 profile uses rows GP{2,3,4,5} and cols GP{6,7,8}, num_cols=3,
// with the standard 4x3 telephone keychars. We model a held key by holding one
// column LOW. keypad_scan_raw() drives each row low in turn and reads that
// column low for every row, so the reported char is the bottom-most row sharing
// the column (row 3): col0='*', col1='0', col2='#'. held_key_for_col() below
// encodes that mapping.

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

// Fresh keypad on the V2 profile with all keys released, then advance past the
// debounce window so the first press is eligible.
static void setup(void) {
    fake_env_reset();
    fake_board_use_v2();
    release_keys();
    keypad_init();
    fake_clock_advance_ms(200);
}

static void test_keypad_distinct_key_accepted_once(void) {
    setup();

    hold_col(COL1);  // '0'
    char k = keypad_scan();
    CHECK_EQ(k, held_key_for_col(COL1));

    // Holding the same key down: subsequent scans return '\0' (no repeat).
    CHECK_EQ(keypad_scan(), '\0');
    fake_clock_advance_ms(500);
    CHECK_EQ(keypad_scan(), '\0');
}

static void test_keypad_same_key_after_release(void) {
    setup();

    hold_col(COL0);  // '*'
    CHECK_EQ(keypad_scan(), '*');

    // Release. The release edge is only confirmed once the debounce window has
    // elapsed since the accept, so scanning immediately keeps the key latched.
    release_keys();
    CHECK_EQ(keypad_scan(), '\0');

    // Advance past the debounce window and scan again: now the release is
    // confirmed and the latch clears.
    fake_clock_advance_ms(100);
    CHECK_EQ(keypad_scan(), '\0');

    // Press the same key again: with the release confirmed it is a genuine new
    // press and is accepted.
    hold_col(COL0);
    CHECK_EQ(keypad_scan(), '*');
}

static void test_keypad_distinct_key_within_debounce_rejected(void) {
    setup();

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
    setup();

    for (int i = 0; i < 5; ++i) {
        CHECK_EQ(keypad_scan(), '\0');
        fake_clock_advance_ms(50);
    }
}

static const test_case_t k_keypad_tests[] = {
    TEST_CASE(test_keypad_distinct_key_accepted_once),
    TEST_CASE(test_keypad_same_key_after_release),
    TEST_CASE(test_keypad_distinct_key_within_debounce_rejected),
    TEST_CASE(test_keypad_no_press_returns_null),
};

DEFINE_SUITE(keypad)
