#include "keypad.h"

#include "board.h"
#include "hardware/gpio.h"
#include "pico/stdlib.h"

// All supported boards have 4 keypad rows; only the column count varies.
#define KEYPAD_NUM_ROWS 4

#define KEYPAD_SETTLE_US 5
#define KEYPAD_DEBOUNCE_MS 80

static char s_last_key = '\0';
static absolute_time_t s_last_press_time;

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
    s_last_press_time = get_absolute_time();
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
    if (pressed != '\0' && pressed != s_last_key) {
        if (absolute_time_diff_us(s_last_press_time, now) >= (KEYPAD_DEBOUNCE_MS * 1000)) {
            s_last_key = pressed;
            s_last_press_time = now;
            return pressed;
        }
    } else if (pressed == '\0') {
        s_last_key = '\0';
    }

    return '\0';
}
