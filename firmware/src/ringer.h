#ifndef RINGER_H
#define RINGER_H

#include <stdbool.h>

// L298N H-bridge control pins — driven in anti-phase at 20Hz
// to produce AC square wave through step-up transformer → bell coil
#define RINGER_PIN_IN1 11  // GP11 — L298N IN1
#define RINGER_PIN_IN2 15  // GP15 — L298N IN2

void ringer_init(void);
void ringer_start(void);
void ringer_stop(void);
bool ringer_is_active(void);
void ringer_update(void);

#endif  // RINGER_H
