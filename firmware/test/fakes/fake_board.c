#include "board.h"

#include <string.h>

// Fake board profile for host tests. Mirrors the real V2 carrier-board pin map
// and the 4x3 telephone keypad so keypad_scan() resolves the same characters
// the firmware ships. Tests that need V1 (4x4) can call fake_board_use_v1().

static const char keychars_v2[12] = {
    '1', '2', '3',
    '4', '5', '6',
    '7', '8', '9',
    '*', '0', '#',
};

static const char keychars_v1[16] = {
    '1', '2', '3', 'A',
    '4', '5', '6', 'B',
    '7', '8', '9', 'C',
    '*', '0', '#', 'D',
};

static const board_profile_t profile_v2 = {
    .name = "v2",
    .rev_byte = '2',
    .uart_tx_pin = 28,
    .uart_rx_pin = 29,
    .hook_pin = 20,
    .led_pin = 16,
    .keypad_rows = {2, 3, 4, 5},
    .keypad_cols = {6, 7, 8, 9},
    .keypad_num_cols = 3,
    .keychars = keychars_v2,
    .ringer_in1_pin = 19,
    .ringer_in2_pin = 15,
};

static const board_profile_t profile_v1 = {
    .name = "v1",
    .rev_byte = '1',
    .uart_tx_pin = 0,
    .uart_rx_pin = 1,
    .hook_pin = 10,
    .led_pin = 14,
    .keypad_rows = {2, 3, 4, 5},
    .keypad_cols = {6, 7, 8, 9},
    .keypad_num_cols = 4,
    .keychars = keychars_v1,
    .ringer_in1_pin = 11,
    .ringer_in2_pin = 15,
};

const board_profile_t *board = &profile_v2;

void fake_board_use_v1(void) { board = &profile_v1; }
void fake_board_use_v2(void) { board = &profile_v2; }

void board_init(void) { board = &profile_v2; }

uint8_t board_read_rev_byte(void) { return board->rev_byte; }

bool board_set_profile(const char *name) {
    if (name == NULL) {
        return false;
    }
    if (strcmp(name, "v1") == 0) {
        board = &profile_v1;
        return true;
    }
    if (strcmp(name, "v2") == 0) {
        board = &profile_v2;
        return true;
    }
    return false;
}
