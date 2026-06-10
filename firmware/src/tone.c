#include "tone.h"

// Pico-side PWM tones are DEPRECATED. All tone generation moved to Pi-side
// (dtmf_uart.py). These are no-op stubs so the FSM compiles without changes.

void tone_init(void) {}
void tone_play(tone_type_t type) { (void)type; }
void tone_play_dtmf(char key) { (void)key; }
void tone_stop(void) {}
void tone_update(void) {}
