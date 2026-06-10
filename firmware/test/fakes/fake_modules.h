#ifndef DIGITS_TEST_FAKE_MODULES_H
#define DIGITS_TEST_FAKE_MODULES_H

// Inspection and control hooks exposed by the fake module implementations
// (fake_board.c, fake_led.c, fake_phase.c) for use by tests.

#include <stdint.h>

#include "led.h"

void fake_board_use_v1(void);
void fake_board_use_v2(void);

led_mode_t fake_led_mode(void);

void fake_phase_set(uint8_t value);

#endif  // DIGITS_TEST_FAKE_MODULES_H
