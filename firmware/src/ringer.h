#ifndef RINGER_H
#define RINGER_H

#include <stdbool.h>

// DRV8871 H-bridge control pins — driven in anti-phase at 20Hz.
// The DRV8871 OUT1/OUT2 drive the primary of an off-board 120V↔12V 10W
// transformer (used in reverse as a 1:10 step-up); its secondary drives
// the Western Electric bell coils externally.
#define RINGER_PIN_IN1 19  // GP19 — DRV8871 IN1 (U3.30 → U2.3)
#define RINGER_PIN_IN2 15  // GP15 — DRV8871 IN2 (U3.18 → U2.2)

void ringer_init(void);
void ringer_start(void);
void ringer_stop(void);
bool ringer_is_active(void);
void ringer_update(void);

#endif  // RINGER_H
