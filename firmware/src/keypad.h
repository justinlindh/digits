#ifndef DIGITS_KEYPAD_H
#define DIGITS_KEYPAD_H

#include <stdint.h>

// Telephone matrix keypad. Pin assignments and column count come from the
// active board profile (see board.h). V1 is a 4x4 matrix (12 phone keys plus
// an extra A-D column). V2 is a standard 4x3 telephone matrix.
//   rows = scan outputs (active low when selected)
//   cols = inputs with pull-up (read low when key pressed in selected row)

void keypad_init(void);

// Returns pressed key ('0'-'9', '*', '#', and on V1 also 'A'-'D')
// or '\0' if no new key press.
char keypad_scan(void);

#endif  // DIGITS_KEYPAD_H
