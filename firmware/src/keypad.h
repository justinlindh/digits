#ifndef DIGITS_KEYPAD_H
#define DIGITS_KEYPAD_H

#include <stdint.h>

// Row GPIOs (active-low scan outputs)
#define KEYPAD_ROW0 2
#define KEYPAD_ROW1 3
#define KEYPAD_ROW2 4
#define KEYPAD_ROW3 5

// Column GPIOs (inputs with pull-up)
#define KEYPAD_COL0 6
#define KEYPAD_COL1 7
#define KEYPAD_COL2 8
#define KEYPAD_COL3 9

void keypad_init(void);

// Returns pressed key ('0'-'9', '*', '#', 'A'-'D') or '\0' if no new key press.
char keypad_scan(void);

#endif  // DIGITS_KEYPAD_H
