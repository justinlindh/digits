#ifndef TONE_H
#define TONE_H

#include <stdint.h>

// Pico-side PWM tones are DEPRECATED — all tone generation moved to Pi-side
// (dtmf_uart.py). These stubs remain so the FSM compiles without changes.
// GP12 is unused. GP13 reassigned to LED.

typedef enum {
    TONE_NONE = 0,
    TONE_DIAL,
    TONE_RINGBACK,
    TONE_BUSY,
    TONE_DTMF,
} tone_type_t;

void tone_init(void);
void tone_play(tone_type_t type);
void tone_play_dtmf(char key);
void tone_stop(void);
void tone_update(void);

#endif  // TONE_H
