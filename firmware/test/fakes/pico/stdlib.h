#ifndef DIGITS_TEST_PICO_STDLIB_H
#define DIGITS_TEST_PICO_STDLIB_H

// Minimal host stand-in for <pico/stdlib.h>. Provides the SDK base types and
// the handful of pico_stdlib entry points the firmware references, backed by
// the fake clock/GPIO engine.

#include <stdbool.h>
#include <stdint.h>

#include "_sdk_shim.h"

typedef unsigned int uint;

// The SDK models absolute_time_t as an opaque struct; we mirror that so the
// firmware's `absolute_time_t s_stable_since;` declarations compile and copy by
// value the same way.
typedef struct {
    uint64_t _us;
} absolute_time_t;

#define GPIO_OUT 1
#define GPIO_IN 0

static inline absolute_time_t get_absolute_time(void) {
    absolute_time_t t;
    t._us = fake_sdk_time_us();
    return t;
}

static inline int64_t absolute_time_diff_us(absolute_time_t from,
                                            absolute_time_t to) {
    return (int64_t)(to._us - from._us);
}

static inline uint32_t to_ms_since_boot(absolute_time_t t) {
    return (uint32_t)(t._us / 1000u);
}

static inline void gpio_init(uint pin) { (void)pin; }
static inline void gpio_deinit(uint pin) { (void)pin; }
static inline void gpio_set_dir(uint pin, int out) { (void)pin; (void)out; }
static inline void gpio_pull_up(uint pin) { (void)pin; }
static inline int gpio_get(uint pin) { return fake_sdk_gpio_get(pin); }
static inline void gpio_put(uint pin, int value) { fake_sdk_gpio_put(pin, value); }

// Time advance is driven explicitly by tests; sleeping is a no-op on host.
static inline void sleep_ms(uint32_t ms) { (void)ms; }
static inline void sleep_us(uint64_t us) { (void)us; }

static inline void stdio_init_all(void) {}
static inline void stdio_flush(void) {}
static inline void tight_loop_contents(void) {}

#endif  // DIGITS_TEST_PICO_STDLIB_H
