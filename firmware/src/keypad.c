#include "keypad.h"

#include "hardware/gpio.h"
#include "pico/stdlib.h"

#define KEYPAD_ROWS 4
#define KEYPAD_COLS 4
#define KEYPAD_SETTLE_US 5
#define KEYPAD_DEBOUNCE_MS 80

static const uint8_t row_pins[KEYPAD_ROWS] = {
    KEYPAD_ROW0,
    KEYPAD_ROW1,
    KEYPAD_ROW2,
    KEYPAD_ROW3,
};

static const uint8_t col_pins[KEYPAD_COLS] = {
    KEYPAD_COL0,
    KEYPAD_COL1,
    KEYPAD_COL2,
    KEYPAD_COL3,
};

static const char key_map[KEYPAD_ROWS][KEYPAD_COLS] = {
    {'1', '2', '3', 'A'},
    {'4', '5', '6', 'B'},
    {'7', '8', '9', 'C'},
    {'*', '0', '#', 'D'},
};

static char s_last_key = '\0';
static absolute_time_t s_last_press_time;

void keypad_init(void) {
    for (int i = 0; i < KEYPAD_ROWS; ++i) {
        gpio_init(row_pins[i]);
        gpio_set_dir(row_pins[i], GPIO_OUT);
        gpio_put(row_pins[i], 1);
    }

    for (int i = 0; i < KEYPAD_COLS; ++i) {
        gpio_init(col_pins[i]);
        gpio_set_dir(col_pins[i], GPIO_IN);
        gpio_pull_up(col_pins[i]);
    }

    s_last_key = '\0';
    s_last_press_time = get_absolute_time();
}

char keypad_scan(void) {
    char pressed = '\0';

    for (int row = 0; row < KEYPAD_ROWS; ++row) {
        gpio_put(row_pins[row], 0);
        sleep_us(KEYPAD_SETTLE_US);

        for (int col = 0; col < KEYPAD_COLS; ++col) {
            if (gpio_get(col_pins[col]) == 0) {
                pressed = key_map[row][col];
            }
        }

        gpio_put(row_pins[row], 1);
    }

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
