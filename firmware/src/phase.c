#include "phase.h"

#include <string.h>

#include "hardware/flash.h"
#include "hardware/sync.h"
#include "pico/stdlib.h"

uint8_t phase_read(void) {
    return *(volatile uint8_t *)PHASE_FLASH_ADDR;
}

void phase_write(uint8_t value) {
    if (phase_read() == value) {
        return;
    }

    uint8_t buf[FLASH_PAGE_SIZE];
    memset(buf, 0xFF, sizeof(buf));
    buf[0] = value;

    uint32_t ints = save_and_disable_interrupts();
    flash_range_erase(PHASE_FLASH_OFFSET, FLASH_SECTOR_SIZE);
    flash_range_program(PHASE_FLASH_OFFSET, buf, FLASH_PAGE_SIZE);
    restore_interrupts(ints);
}
