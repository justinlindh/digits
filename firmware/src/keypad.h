#ifndef DIGITS_KEYPAD_H
#define DIGITS_KEYPAD_H

#include <stdint.h>

#ifndef HARDWARE_REV
#error "HARDWARE_REV not defined; set -DHARDWARE_REV=1 or =2 at configure time"
#endif

// V1 ElectroCookie prototype: 4x4 matrix on GP2..GP9
//   rows = scan outputs (active low when selected)
//   cols = inputs with pull-up (read low when key pressed in selected row)
// V2 carrier PCB: 4x3 telephone matrix on GP21..GP27 per schematic.
//   rows: GP27 (ROW0), GP26 (ROW1), GP21 (ROW2), GP25 (ROW3)
//   cols: GP24 (COL0), GP23 (COL1), GP22 (COL2)
#if HARDWARE_REV == 1
#define KEYPAD_NUM_ROWS 4
#define KEYPAD_NUM_COLS 4
#define KEYPAD_ROW0 2
#define KEYPAD_ROW1 3
#define KEYPAD_ROW2 4
#define KEYPAD_ROW3 5
#define KEYPAD_COL0 6
#define KEYPAD_COL1 7
#define KEYPAD_COL2 8
#define KEYPAD_COL3 9
#elif HARDWARE_REV == 2
#define KEYPAD_NUM_ROWS 4
#define KEYPAD_NUM_COLS 3
#define KEYPAD_ROW0 27
#define KEYPAD_ROW1 26
#define KEYPAD_ROW2 21
#define KEYPAD_ROW3 25
#define KEYPAD_COL0 24
#define KEYPAD_COL1 23
#define KEYPAD_COL2 22
#else
#error "Unsupported HARDWARE_REV; must be 1 or 2"
#endif

void keypad_init(void);

// Returns pressed key ('0'-'9', '*', '#', and on V1 also 'A'-'D')
// or '\0' if no new key press.
char keypad_scan(void);

#endif  // DIGITS_KEYPAD_H
