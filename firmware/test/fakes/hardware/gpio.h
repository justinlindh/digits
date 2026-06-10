#ifndef DIGITS_TEST_HARDWARE_GPIO_H
#define DIGITS_TEST_HARDWARE_GPIO_H

// Host stand-in for <hardware/gpio.h>. The gpio_* entry points live in the
// stdlib fake; gpio_set_function is UART-pin plumbing the tests do not need.

#include "pico/stdlib.h"

#define GPIO_FUNC_UART 2

static inline void gpio_set_function(uint pin, int fn) { (void)pin; (void)fn; }

#endif  // DIGITS_TEST_HARDWARE_GPIO_H
