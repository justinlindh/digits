#include "led.h"

#include "board.h"
#include "hardware/gpio.h"
#include "pico/time.h"

#define LED_BLINK_INTERVAL_US 500000
#define LED_SLOW_BLINK_INTERVAL_US 2000000

static led_mode_t s_mode = LED_MODE_OFF;
static bool s_led_on = false;
static absolute_time_t s_last_toggle;

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
        s_last_toggle = get_absolute_time();
    }
}

void led_update(void) {
    int64_t interval_us;
    switch (s_mode) {
    case LED_MODE_BLINK:
        interval_us = LED_BLINK_INTERVAL_US;
        break;
    case LED_MODE_SLOW_BLINK:
        interval_us = LED_SLOW_BLINK_INTERVAL_US;
        break;
    default:
        return;
    }

    absolute_time_t now = get_absolute_time();
    if (absolute_time_diff_us(s_last_toggle, now) >= interval_us) {
        s_led_on = !s_led_on;
        gpio_put(board->led_pin, s_led_on ? 1 : 0);
        s_last_toggle = now;
    }
}
