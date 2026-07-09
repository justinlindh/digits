#ifndef DIGITS_LED_PHASE_H
#define DIGITS_LED_PHASE_H

#include <stdint.h>

#include "led.h"

// Resolve the idle (on-hook) LED pattern for a persistent phase byte. This is
// the single source of truth shared by the boot path (main.c) and the FSM idle
// path (phone_fsm.c) so the two cannot drift. An unrecognized phase, including
// an unprogrammed 0xFF flash byte, maps to LED_MODE_OFF.
led_mode_t phase_idle_led_mode(uint8_t phase);

#endif  // DIGITS_LED_PHASE_H
