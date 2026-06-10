#ifndef DIGITS_TEST_PICO_BOOTROM_H
#define DIGITS_TEST_PICO_BOOTROM_H

// Host stand-in for <pico/bootrom.h>. reset_usb_boot is only reached by the
// REBOOT command path, which the host tests do not drive.

#include <stdint.h>

static inline void reset_usb_boot(uint32_t gpio_mask, uint32_t disable_mask) {
    (void)gpio_mask;
    (void)disable_mask;
}

#endif  // DIGITS_TEST_PICO_BOOTROM_H
