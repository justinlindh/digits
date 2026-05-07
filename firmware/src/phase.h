#ifndef PHASE_H
#define PHASE_H

#include <stdint.h>

#define PHASE_PAIRED    0x01
#define PHASE_UNPAIRED  0x02
#define PHASE_SETUP     0x03
#define PHASE_RECOVERY  0x04

// Flash sector holding the persistent phase byte. One sector below the board
// rev byte at 0x1FF000. Reading is a plain XIP volatile load.
#define PHASE_FLASH_OFFSET 0x1FE000u
#define PHASE_FLASH_ADDR   (XIP_BASE + PHASE_FLASH_OFFSET)

// Read the current phase byte from flash (XIP). Returns 0xFF if unprogrammed.
uint8_t phase_read(void);

// Write a new phase byte to flash. No-op if value matches current contents.
void phase_write(uint8_t value);

#endif  // PHASE_H
