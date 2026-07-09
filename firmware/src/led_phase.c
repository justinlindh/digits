#include "led_phase.h"

#include "phase.h"

// Idle LED patterns per phase. These are the ambient on-hook indicators the
// user sees; docs/user-guide.md documents the paired-breathing and
// unpaired-flash meanings. Keep this the only place the mapping lives: both the
// boot path and the FSM idle path call through here.
led_mode_t phase_idle_led_mode(uint8_t phase) {
    switch (phase) {
    case PHASE_PAIRED:
        return LED_MODE_BREATHING;
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
