#include "led.h"

#include "board.h"
#include "hardware/gpio.h"
#include "pico/time.h"

#define BLINK_INTERVAL_US       500000
#define FAST_BLINK_INTERVAL_US  150000

// Double pulse: ON 100ms, OFF 100ms, ON 100ms, OFF 700ms
#define DP_ON_US    100000
#define DP_GAP_US   100000
#define DP_PAUSE_US 700000

// Connecting: ON 100ms, OFF 100ms, ON 100ms, OFF 400ms
#define CN_ON_US    100000
#define CN_GAP_US   100000
#define CN_PAUSE_US 400000

static led_mode_t s_mode = LED_MODE_OFF;
static bool s_led_on = false;
static bool s_locked = false;
static absolute_time_t s_last_toggle;
static uint8_t s_step;

void led_init(void) {
    gpio_init(board->led_pin);
    gpio_set_dir(board->led_pin, GPIO_OUT);

    s_mode = LED_MODE_OFF;
    s_led_on = false;
    s_locked = false;
    s_step = 0;
    gpio_put(board->led_pin, 0);
    s_last_toggle = get_absolute_time();
}

void led_set_locked(bool locked) {
    s_locked = locked;
}

bool led_is_locked(void) {
    return s_locked;
}

void led_set_mode(led_mode_t mode) {
    s_mode = mode;
    s_step = 0;

    if (mode == LED_MODE_OFF) {
        s_led_on = false;
        gpio_put(board->led_pin, 0);
    } else if (mode == LED_MODE_ON) {
        s_led_on = true;
        gpio_put(board->led_pin, 1);
    } else {
        s_last_toggle = get_absolute_time();
    }
}

static void set_led(bool on) {
    s_led_on = on;
    gpio_put(board->led_pin, on ? 1 : 0);
}

// Pattern step machine for multi-phase patterns.
// Double pulse: step 0=ON, 1=OFF(gap), 2=ON, 3=OFF(pause)
// Connecting:   step 0=ON, 1=OFF(gap), 2=ON, 3=OFF(pause)
static void update_pattern(uint32_t on_us, uint32_t gap_us, uint32_t pause_us) {
    absolute_time_t now = get_absolute_time();
    uint32_t elapsed = (uint32_t)absolute_time_diff_us(s_last_toggle, now);

    switch (s_step) {
    case 0:
        if (elapsed >= on_us) {
            set_led(false);
            s_step = 1;
            s_last_toggle = now;
        }
        break;
    case 1:
        if (elapsed >= gap_us) {
            set_led(true);
            s_step = 2;
            s_last_toggle = now;
        }
        break;
    case 2:
        if (elapsed >= on_us) {
            set_led(false);
            s_step = 3;
            s_last_toggle = now;
        }
        break;
    case 3:
        if (elapsed >= pause_us) {
            set_led(true);
            s_step = 0;
            s_last_toggle = now;
        }
        break;
    }
}

void led_update(void) {
    if (s_mode == LED_MODE_OFF || s_mode == LED_MODE_ON) {
        return;
    }

    absolute_time_t now = get_absolute_time();

    switch (s_mode) {
    case LED_MODE_BLINK:
        if (absolute_time_diff_us(s_last_toggle, now) >= BLINK_INTERVAL_US) {
            set_led(!s_led_on);
            s_last_toggle = now;
        }
        break;
    case LED_MODE_FAST_BLINK:
        if (absolute_time_diff_us(s_last_toggle, now) >= FAST_BLINK_INTERVAL_US) {
            set_led(!s_led_on);
            s_last_toggle = now;
        }
        break;
    case LED_MODE_DOUBLE_PULSE:
        update_pattern(DP_ON_US, DP_GAP_US, DP_PAUSE_US);
        break;
    case LED_MODE_CONNECTING:
        update_pattern(CN_ON_US, CN_GAP_US, CN_PAUSE_US);
        break;
    default:
        break;
    }
}
