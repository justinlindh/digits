#ifndef DIGITS_HOOK_H
#define DIGITS_HOOK_H

#include <stdbool.h>
#include "pico/stdlib.h"

// Hook transition events returned by hook_get_event().
typedef enum {
    HOOK_EVENT_NONE  = 0,
    HOOK_EVENT_OFF,    // Handset lifted (off-hook) -- confirmed after flash window expires
    HOOK_EVENT_ON,     // Handset cradled (on-hook) from a stable off-hook state
    HOOK_EVENT_FLASH,  // Brief on-off-on pulse: 100-600ms off duration
} hook_event_t;

void hook_init(void);
void hook_poll(void);       // Call from main loop
bool hook_is_off_hook(void);
hook_event_t hook_get_event(void);

// Invert the physical hook sense. Default is non-inverted: switch closed
// to GND when on-hook (HIGH = off-hook via internal pull-up). Use invert
// when the wired switch's polarity is reversed (NC contacts, or a switch
// that opens when the handset is cradled).
void hook_set_inverted(bool inverted);
bool hook_is_inverted(void);

// Debug: software override of hook state.
// hook_force_off_hook(true)  = pretend handset is lifted (off-hook)
// hook_force_off_hook(false) = pretend handset is on cradle (on-hook)
// hook_clear_force()         = return to reading physical pin
void hook_force_off_hook(bool off_hook);
void hook_clear_force(void);
bool hook_is_forced(void);

// Flash-detection gate. When disabled (default), any debounced transition to
// on-hook immediately emits HOOK_EVENT_ON -- there is no flash window and no
// hangup latency. When enabled, the firmware waits up to FLASH_MAX_MS after
// the on-hook commit to see if the hook returns to off-hook (emits
// HOOK_EVENT_FLASH). The Pi daemon only enables this while in an active call
// state, so hangup/pickup outside calls stay instantaneous.
void hook_set_flash_enabled(bool enabled);

// Returns true while a flash window is open (user has depressed the hookswitch
// but we haven't decided yet whether it's a flash or a hangup). During this
// window the raw off-hook state is transiently false; callers that make
// decisions based on raw hook state should skip them while this is true.
bool hook_is_flash_pending(void);

#endif  // DIGITS_HOOK_H
