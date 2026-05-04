#include "led.h"

#include "board.h"
#include "hardware/gpio.h"
#include "pico/time.h"

#define LED_BLINK_INTERVAL_US 500000

// DOUBLE_PULSE: 150ms on, 150ms off, 150ms on, 1550ms off (2s cycle)
#define LED_DP_ON_US   150000
#define LED_DP_GAP_US  150000
#define LED_DP_PAUSE_US 1550000

// HEARTBEAT: 100ms on, 2900ms off (3s cycle)
#define LED_HB_ON_US    100000
#define LED_HB_OFF_US  2900000

static led_mode_t s_mode = LED_MODE_OFF;
static bool s_led_on = false;
static absolute_time_t s_last_toggle;
static int s_phase = 0;

void led_init(void) {
    gpio_init(board->led_pin);
    gpio_set_dir(board->led_pin, GPIO_OUT);

    s_mode = LED_MODE_OFF;
    s_led_on = false;
    gpio_put(board->led_pin, 0);
    s_last_toggle = get_absolute_time();
}

void led_set_mode(led_mode_t mode) {
    s_mode = mode;

    if (mode == LED_MODE_OFF) {
        s_led_on = false;
        gpio_put(board->led_pin, 0);
    } else if (mode == LED_MODE_ON) {
        s_led_on = true;
        gpio_put(board->led_pin, 1);
    } else {
        s_led_on = true;
        s_phase = 0;
        gpio_put(board->led_pin, 1);
        s_last_toggle = get_absolute_time();
    }
}

void led_update(void) {
    absolute_time_t now = get_absolute_time();
    int64_t elapsed = absolute_time_diff_us(s_last_toggle, now);

    if (s_mode == LED_MODE_BLINK) {
        if (elapsed >= LED_BLINK_INTERVAL_US) {
            s_led_on = !s_led_on;
            gpio_put(board->led_pin, s_led_on ? 1 : 0);
            s_last_toggle = now;
        }
        return;
    }

    if (s_mode == LED_MODE_DOUBLE_PULSE) {
        // Phases: 0=first-on, 1=gap, 2=second-on, 3=long-pause
        int64_t threshold;
        switch (s_phase) {
            case 0: threshold = LED_DP_ON_US;    break;
            case 1: threshold = LED_DP_GAP_US;   break;
            case 2: threshold = LED_DP_ON_US;    break;
            case 3: threshold = LED_DP_PAUSE_US; break;
            default: threshold = LED_DP_PAUSE_US; break;
        }
        if (elapsed >= threshold) {
            s_phase = (s_phase + 1) % 4;
            s_led_on = (s_phase == 0 || s_phase == 2);
            gpio_put(board->led_pin, s_led_on ? 1 : 0);
            s_last_toggle = now;
        }
        return;
    }

    if (s_mode == LED_MODE_HEARTBEAT) {
        // Phases: 0=on, 1=off
        int64_t threshold = (s_phase == 0) ? LED_HB_ON_US : LED_HB_OFF_US;
        if (elapsed >= threshold) {
            s_phase = (s_phase + 1) % 2;
            s_led_on = (s_phase == 0);
            gpio_put(board->led_pin, s_led_on ? 1 : 0);
            s_last_toggle = now;
        }
        return;
    }
}
