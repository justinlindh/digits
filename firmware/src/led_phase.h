#ifndef DIGITS_LED_PHASE_H
#define DIGITS_LED_PHASE_H

#include <stdint.h>

#include "led.h"

// Resolve the idle (on-hook) LED pattern for a persistent phase byte, per the
// user guide's normal-operation table (paired = dark). Used by the FSM idle
// path (phone_fsm.c). An unrecognized phase, including an unprogrammed 0xFF
// flash byte, maps to LED_MODE_OFF.
led_mode_t phase_idle_led_mode(uint8_t phase);

// Resolve the boot-settle LED pattern, per the user guide's boot table
// (paired = breathing until digitsd's first state resync). Used by main.c at
// power-on; delegates to phase_idle_led_mode for every non-paired phase.
led_mode_t phase_boot_led_mode(uint8_t phase);

#endif  // DIGITS_LED_PHASE_H
