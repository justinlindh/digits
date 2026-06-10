#include "phase.h"

// Fake phase store for host tests. The real phase.c reads/writes a flash
// sector; here it is an in-memory byte so the FSM's phase-driven LED idle
// patterns and STATE:SET commands can be exercised.

static uint8_t s_phase = PHASE_PAIRED;

uint8_t phase_read(void) { return s_phase; }

void phase_write(uint8_t value) { s_phase = value; }

void fake_phase_set(uint8_t value) { s_phase = value; }
