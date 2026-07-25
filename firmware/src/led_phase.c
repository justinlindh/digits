#include "led_phase.h"

#include "phase.h"

// Idle LED patterns per phase. These are the ambient on-hook indicators the
// user sees, matching the normal-operation table in docs/user-guide.md: a
// paired idle phone is dark, and breathing is reserved for an active call
// (phone_fsm.c drives that from CONNECTED). Keep this the only place the
// mapping lives so the boot and FSM paths cannot drift again.
led_mode_t phase_idle_led_mode(uint8_t phase) {
    switch (phase) {
    case PHASE_PAIRED:
        return LED_MODE_OFF;
    case PHASE_UNPAIRED:
        return LED_MODE_SLOW_PULSE;
    case PHASE_SETUP:
        return LED_MODE_DOUBLE_PULSE;
    case PHASE_RECOVERY:
        return LED_MODE_FAST_BLINK;
    default:
        return LED_MODE_OFF;
    }
}

// Boot-settle LED patterns per phase, from the user guide's boot table: a
// paired phone breathes from power-on until digitsd's first STATE:SET resync
// hands the LED to the idle mapping above. Every other phase shows the same
// pattern in both tables.
led_mode_t phase_boot_led_mode(uint8_t phase) {
    if (phase == PHASE_PAIRED) {
        return LED_MODE_BREATHING;
    }
    return phase_idle_led_mode(phase);
}
