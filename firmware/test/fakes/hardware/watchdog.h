#ifndef DIGITS_TEST_HARDWARE_WATCHDOG_H
#define DIGITS_TEST_HARDWARE_WATCHDOG_H

// Host stand-in for <hardware/watchdog.h>. The reboot command paths that call
// this are not exercised by the host tests, so it is a no-op.

#include <stdbool.h>
#include <stdint.h>

static inline void watchdog_enable(uint32_t delay_ms, bool pause_on_debug) {
    (void)delay_ms;
    (void)pause_on_debug;
}

static inline void watchdog_disable(void) {
}

#endif  // DIGITS_TEST_HARDWARE_WATCHDOG_H
