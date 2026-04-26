#include "board.h"

#include <string.h>

// V1 ElectroCookie prototype: 4x4 matrix (12 phone keys plus A-D column).
static const char keychars_v1[16] = {
    '1', '2', '3', 'A',
    '4', '5', '6', 'B',
    '7', '8', '9', 'C',
    '*', '0', '#', 'D',
};

// V2 carrier 4x3 telephone matrix (no A-D column).
static const char keychars_v2[12] = {
    '1', '2', '3',
    '4', '5', '6',
    '7', '8', '9',
    '*', '0', '#',
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

static const board_profile_t profile_v2 = {
    .name = "v2",
    .rev_byte = '2',
    .uart_tx_pin = 28,
    .uart_rx_pin = 29,
    .hook_pin = 20,
    .led_pin = 16,
    .keypad_rows = {27, 26, 21, 25},
    .keypad_cols = {24, 23, 22, 0},
    .keypad_num_cols = 3,
    .keychars = keychars_v2,
    .ringer_in1_pin = 19,
    .ringer_in2_pin = 15,
};

static const board_profile_t* const all_profiles[] = {&profile_v1, &profile_v2};
#define NUM_PROFILES (sizeof(all_profiles) / sizeof(all_profiles[0]))

const board_profile_t* board = &profile_v2;

uint8_t board_read_rev_byte(void) {
    return *(const volatile uint8_t*)BOARD_REV_FLASH_ADDR;
}

void board_init(void) {
    uint8_t rev = board_read_rev_byte();
    for (size_t i = 0; i < NUM_PROFILES; i++) {
        if (all_profiles[i]->rev_byte == rev) {
            board = all_profiles[i];
            return;
        }
    }
    board = &profile_v2;
}

bool board_set_profile(const char* name) {
    for (size_t i = 0; i < NUM_PROFILES; i++) {
        if (strcmp(all_profiles[i]->name, name) == 0) {
            board = all_profiles[i];
            return true;
        }
    }
    return false;
}
