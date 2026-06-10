#include "keypad.h"

#include "board.h"
#include "hardware/gpio.h"
#include "pico/stdlib.h"

// All supported boards have 4 keypad rows; only the column count varies.
#define KEYPAD_NUM_ROWS 4

#define KEYPAD_SETTLE_US 5
#define KEYPAD_DEBOUNCE_MS 80

// s_last_key is the key currently believed to be held (the last one accepted),
// or '\0' once a release has been confirmed. s_change_time marks the last
// debounced state change (accept or confirmed release); a new transition is
// honored only after KEYPAD_DEBOUNCE_MS has elapsed since it, which rejects
// contact bounce on a single key without measuring distinct presses against an
// unrelated earlier key's accept time.
static char s_last_key = '\0';
static absolute_time_t s_change_time;

void keypad_init(void) {
    for (int i = 0; i < KEYPAD_NUM_ROWS; ++i) {
        gpio_init(board->keypad_rows[i]);
        gpio_set_dir(board->keypad_rows[i], GPIO_OUT);
        gpio_put(board->keypad_rows[i], 1);
    }

    for (uint i = 0; i < board->keypad_num_cols; ++i) {
        gpio_init(board->keypad_cols[i]);
        gpio_set_dir(board->keypad_cols[i], GPIO_IN);
        gpio_pull_up(board->keypad_cols[i]);
    }

    s_last_key = '\0';
    s_change_time = get_absolute_time();
}

char keypad_scan_raw(void) {
    char pressed = '\0';
    const uint num_cols = board->keypad_num_cols;

    for (int row = 0; row < KEYPAD_NUM_ROWS; ++row) {
        gpio_put(board->keypad_rows[row], 0);
        sleep_us(KEYPAD_SETTLE_US);

        for (uint col = 0; col < num_cols; ++col) {
            if (gpio_get(board->keypad_cols[col]) == 0) {
                pressed = board->keychars[row * num_cols + col];
            }
        }

        gpio_put(board->keypad_rows[row], 1);
    }

    return pressed;
}

char keypad_scan(void) {
    char pressed = keypad_scan_raw();
    absolute_time_t now = get_absolute_time();

    // Holding the same key: no new event, and no auto-repeat.
    if (pressed == s_last_key) {
        return '\0';
    }

    bool debounce_elapsed =
        absolute_time_diff_us(s_change_time, now) >= (KEYPAD_DEBOUNCE_MS * 1000);

    if (pressed == '\0') {
        // Release edge. Confirm it only after the debounce window so brief
        // mid-press contact chatter (key momentarily reads open) does not arm
        // acceptance of a bounce re-close as a fresh press. Once confirmed,
        // s_change_time is reset so the next distinct key is debounced against
        // this release, not against the previous key's accept time.
        if (debounce_elapsed) {
            s_last_key = '\0';
            s_change_time = now;
        }
        return '\0';
    }

    if (s_last_key == '\0') {
        // No key was held (release already confirmed): this is a genuine new
        // press. Accept immediately. It cannot be bounce of a prior key because
        // a confirmed release intervened, so fast distinct-digit dialing is not
        // throttled. Same-key bounce never reaches here: a brief mid-press open
        // does not confirm the release (it re-closes before the window), so
        // s_last_key stays the held key and the re-close is a held no-op above.
        s_last_key = pressed;
        s_change_time = now;
        return pressed;
    }

    // A different key appeared while one was still believed held (no confirmed
    // release between them, e.g. rollover or a fast slide across two contacts).
    // Treat it as a real transition once the debounce window since the last
    // accept has elapsed, so genuine adjacent presses are not lost, while
    // electrical cross-talk within the window is rejected.
    if (debounce_elapsed) {
        s_last_key = pressed;
        s_change_time = now;
        return pressed;
    }

    return '\0';
}
