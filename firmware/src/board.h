#ifndef DIGITS_BOARD_H
#define DIGITS_BOARD_H

#include <stdint.h>
#include "pico/stdlib.h"

// Flash address (XIP-mapped) where the Pi writes a 1-byte ASCII rev marker.
// Last 4 KB sector of 2 MB flash. Reading is a plain XIP load.
//
// This address is also hard-coded in two non-firmware sites:
//   pi/image/rootfs-overlay/usr/local/bin/flash-pico.sh (writes the byte)
//   pi/digitsd/cmd/digitsd/main.go                       (cross-references it)
// Update all three together if the offset ever moves.
#define BOARD_REV_FLASH_OFFSET 0x1FF000u
#define BOARD_REV_FLASH_ADDR   (XIP_BASE + BOARD_REV_FLASH_OFFSET)

typedef struct {
    const char* name;          // e.g. "v1", "v2"
    uint8_t rev_byte;          // ASCII rev character expected in the flash marker

    // UART (Pi to Pico)
    uint uart_tx_pin;
    uint uart_rx_pin;

    // Hookswitch
    uint hook_pin;

    // Status LED
    uint led_pin;

    // Keypad matrix
    uint keypad_rows[4];
    uint keypad_cols[4];
    uint keypad_num_cols;      // V1=4, V2=3
    // Flat (row, col) -> ASCII character lookup, length
    // 4 * keypad_num_cols. Indexed as keychars[row * keypad_num_cols + col].
    const char* keychars;

    // Ringer (DRV8871 H-bridge inputs)
    uint ringer_in1_pin;
    uint ringer_in2_pin;
} board_profile_t;

extern const board_profile_t* board;

// Reads the rev byte at BOARD_REV_FLASH_ADDR via XIP, picks matching profile,
// sets `board`. Falls back to V2 if the byte is unprogrammed (0xFF) or
// doesn't match any known profile.
void board_init(void);

// Override the active profile by name. Returns true if the name matches a
// known profile. Modules that latched their pin numbers at init() time are
// NOT re-initialized. Intended for the CONFIG:PCB_REV=N UART command.
bool board_set_profile(const char* name);

// Reads the rev byte at BOARD_REV_FLASH_ADDR. Exposed for diagnostics.
uint8_t board_read_rev_byte(void);

#endif
