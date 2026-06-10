#include "led.h"

// Fake LED module for host tests. The real led.c drives PWM hardware; the FSM
// only cares that led_set_mode/led_is_locked behave, so this tracks state in
// memory and exposes it for assertions.

static led_mode_t s_mode = LED_MODE_OFF;
static bool s_locked = false;

void led_init(void) {
    s_mode = LED_MODE_OFF;
    s_locked = false;
}

// Matches the real led.c: led_set_mode itself does not honor the lock. Only
// fsm_led_set in phone_fsm.c gates on led_is_locked() before calling here.
void led_set_mode(led_mode_t mode) {
    s_mode = mode;
}

void led_update(void) {}

void led_set_locked(bool locked) { s_locked = locked; }
bool led_is_locked(void) { return s_locked; }

led_mode_t fake_led_mode(void) { return s_mode; }
