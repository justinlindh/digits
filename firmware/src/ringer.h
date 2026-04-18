#ifndef RINGER_H
#define RINGER_H

#include <stdbool.h>

// Ringer H-bridge control pins, driven in anti-phase at 20Hz. IN1 moved
// between hardware revisions; IN2 is GP15 on both. Select via HARDWARE_REV
// (see firmware/CMakeLists.txt).
#ifndef HARDWARE_REV
#error "HARDWARE_REV not defined; set -DHARDWARE_REV=1 or =2 at configure time"
#endif

#if HARDWARE_REV == 1
// v1: ElectroCookie protoboard, L298N H-bridge driving a step-up transformer.
#define RINGER_PIN_IN1 11  // GP11, L298N IN1
#define RINGER_PIN_IN2 15  // GP15, L298N IN2
#elif HARDWARE_REV == 2
// v2: carrier PCB, DRV8871 H-bridge driving the primary of an off-board
// 120V<->12V 10W transformer used in reverse as a 1:10 step-up; its
// secondary drives the Western Electric bell coils externally.
#define RINGER_PIN_IN1 19  // GP19, DRV8871 IN1 (U3.30 -> U2.3)
#define RINGER_PIN_IN2 15  // GP15, DRV8871 IN2 (U3.18 -> U2.2)
#else
#error "Unsupported HARDWARE_REV; must be 1 or 2"
#endif

void ringer_init(void);
void ringer_start(void);
void ringer_stop(void);
bool ringer_is_active(void);
void ringer_update(void);

#endif  // RINGER_H
