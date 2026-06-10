#ifndef DIGITS_RINGER_H
#define DIGITS_RINGER_H

#include <stdbool.h>
#include <stdint.h>

// Ringer H-bridge control pins, driven in anti-phase at 20Hz. Pin
// assignments come from the active board profile (see board.h).
// V1: L298N H-bridge driving a step-up transformer (IN1=GP11, IN2=GP15).
// V2: DRV8871 H-bridge driving the primary of an off-board 120V<->12V
// 10W transformer used in reverse as a 1:10 step-up; its secondary
// drives the Western Electric bell coils externally (IN1=GP19, IN2=GP15).

void ringer_init(void);
void ringer_start(void);
void ringer_start_pattern(uint8_t pattern_id);
void ringer_stop(void);
void ringer_update(void);

#endif  // DIGITS_RINGER_H
