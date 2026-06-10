#include "fake_env.h"

// State for the virtual clock and the GPIO array. A single translation unit
// owns this so the fake SDK shims (gpio_get, get_absolute_time, ...) and the
// tests share one consistent view.

static uint64_t s_now_us = 0;

// s_input_level is what gpio_get() returns: the level present on the pin,
// whether the firmware drove it (output) or an external source set it (input).
static bool s_input_level[FAKE_GPIO_COUNT];

void fake_env_reset(void) {
    s_now_us = 0;
    for (unsigned i = 0; i < FAKE_GPIO_COUNT; ++i) {
        s_input_level[i] = false;
    }
}

void fake_clock_advance_us(int64_t us) {
    if (us < 0) {
        us = 0;
    }
    s_now_us += (uint64_t)us;
}

void fake_clock_advance_ms(int64_t ms) {
    fake_clock_advance_us(ms * 1000);
}

uint64_t fake_clock_now_us(void) {
    return s_now_us;
}

void fake_gpio_set_level(unsigned pin, bool high) {
    if (pin < FAKE_GPIO_COUNT) {
        s_input_level[pin] = high;
    }
}

bool fake_gpio_get_level(unsigned pin) {
    if (pin < FAKE_GPIO_COUNT) {
        return s_input_level[pin];
    }
    return false;
}

// --- SDK shim backing functions ----------------------------------------------
// These are declared by the fake SDK headers and called by production code.

#include "_sdk_shim.h"

uint64_t fake_sdk_time_us(void) {
    return s_now_us;
}

int fake_sdk_gpio_get(unsigned pin) {
    return fake_gpio_get_level(pin) ? 1 : 0;
}

void fake_sdk_gpio_put(unsigned pin, int value) {
    fake_gpio_set_level(pin, value != 0);
}
