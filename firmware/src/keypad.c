#include "keypad.h"

#include "hardware/gpio.h"
#include "pico/stdlib.h"

#define KEYPAD_SETTLE_US 5
#define KEYPAD_DEBOUNCE_MS 80

static const uint8_t row_pins[KEYPAD_NUM_ROWS] = {
    KEYPAD_ROW0,
    KEYPAD_ROW1,
    KEYPAD_ROW2,
    KEYPAD_ROW3,
};

static const uint8_t col_pins[KEYPAD_NUM_COLS] = {
    KEYPAD_COL0,
    KEYPAD_COL1,
    KEYPAD_COL2,
#if KEYPAD_NUM_COLS == 4
    KEYPAD_COL3,
#endif
};

#if HARDWARE_REV == 1
// V1 prototype 4x4 matrix
static const char key_map[KEYPAD_NUM_ROWS][KEYPAD_NUM_COLS] = {
    {'1', '2', '3', 'A'},
    {'4', '5', '6', 'B'},
    {'7', '8', '9', 'C'},
    {'*', '0', '#', 'D'},
};
#elif HARDWARE_REV == 2
// V2 carrier 4x3 telephone matrix (no A-D)
static const char key_map[KEYPAD_NUM_ROWS][KEYPAD_NUM_COLS] = {
    {'1', '2', '3'},
    {'4', '5', '6'},
    {'7', '8', '9'},
    {'*', '0', '#'},
};
#endif

static char s_last_key = '\0';
static absolute_time_t s_last_press_time;

void keypad_init(void) {
    for (int i = 0; i < KEYPAD_NUM_ROWS; ++i) {
        gpio_init(row_pins[i]);
        gpio_set_dir(row_pins[i], GPIO_OUT);
        gpio_put(row_pins[i], 1);
    }

    for (int i = 0; i < KEYPAD_NUM_COLS; ++i) {
        gpio_init(col_pins[i]);
        gpio_set_dir(col_pins[i], GPIO_IN);
        gpio_pull_up(col_pins[i]);
    }

    s_last_key = '\0';
    s_last_press_time = get_absolute_time();
}

char keypad_scan(void) {
    char pressed = '\0';

    for (int row = 0; row < KEYPAD_NUM_ROWS; ++row) {
        gpio_put(row_pins[row], 0);
        sleep_us(KEYPAD_SETTLE_US);

        for (int col = 0; col < KEYPAD_NUM_COLS; ++col) {
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
