#ifndef DIGITS_HOOK_H
#define DIGITS_HOOK_H

#include <stdbool.h>
#include "pico/stdlib.h"

#define HOOK_PIN 10  // GP10

void hook_init(void);
void hook_poll(void);       // Call from main loop
bool hook_is_off_hook(void);
bool hook_get_event(bool *off_hook);

// Debug: software override of hook state.
// hook_force_off_hook(true)  = pretend handset is lifted (off-hook)
// hook_force_off_hook(false) = pretend handset is on cradle (on-hook)
// hook_clear_force()         = return to reading physical pin
void hook_force_off_hook(bool off_hook);
void hook_clear_force(void);
bool hook_is_forced(void);

#endif  // DIGITS_HOOK_H
