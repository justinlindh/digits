#include <stdbool.h>
#include <stdio.h>

#include "hardware/gpio.h"
#include "hardware/watchdog.h"
#include "pico/stdlib.h"

#include "board.h"
#include "hook.h"
#include "keypad.h"
#include "led.h"
#include "led_phase.h"
#include "phase.h"
#include "phone_fsm.h"
#include "ringer.h"
#include "uart_proto.h"

// Hardware watchdog timeout. The main loop feeds the watchdog every ~10ms
// iteration; if the firmware hangs, the chip resets within this window so the
// Pi can recover the phone without a physical power cycle. Sizing: the longest
// blocking work reachable from the loop is a phase_write flash sector erase
// plus page program (phase.c), which the W25Q16JV datasheet bounds at ~400ms
// worst case with interrupts disabled. The RP2040 watchdog counts in hardware
// independent of the CPU, so that erase must fit inside a single feed interval;
// 1000ms leaves roughly 2.5x margin over it. Everything else in the loop (USB
// console poll, hook poll, keypad scan, LED/ringer updates) is non-blocking or
// bounded by microsecond sleeps.
#define WATCHDOG_TIMEOUT_MS 1000

// Drive both candidate UART_TX pins high so the Pi sees a clean idle line
// during the boot window. We don't know which is the real one until
// board_init() reads the rev byte from flash, so cover both. The unused
// pin is released after board_init().
static void uart_tx_idle_high(void) {
    gpio_init(0);
    gpio_set_dir(0, GPIO_OUT);
    gpio_put(0, 1);
    gpio_init(28);
    gpio_set_dir(28, GPIO_OUT);
    gpio_put(28, 1);
}

static void release_unused_uart_tx_pin(void) {
    if (board->uart_tx_pin != 0)  gpio_deinit(0);
    if (board->uart_tx_pin != 28) gpio_deinit(28);
}

// USB console line buffer
static char usb_line[64];
static size_t usb_idx = 0;

static void usb_console_poll(void) {
    while (true) {
        int c = getchar_timeout_us(0);
        if (c == PICO_ERROR_TIMEOUT) {
            break;
        }
        if (c == '\r' || c == '\n') {
            putchar('\n');
            if (usb_idx > 0) {
                usb_line[usb_idx] = '\0';
                printf("USB CMD: %s\n", usb_line);
                stdio_flush();
                uart_proto_inject(usb_line);
                usb_idx = 0;
            }
            printf("> ");
            stdio_flush();
        } else if (c == 127 || c == '\b') {
            // Backspace
            if (usb_idx > 0) {
                usb_idx--;
                printf("\b \b");
                stdio_flush();
            }
        } else if (usb_idx < sizeof(usb_line) - 1) {
            usb_line[usb_idx++] = (char)c;
            putchar(c);
            stdio_flush();
        }
    }
}

int main(void) {
    uart_tx_idle_high();

    board_init();

    release_unused_uart_tx_pin();

    stdio_init_all();

    hook_init();
    keypad_init();
    led_init();

    led_set_mode(phase_idle_led_mode(phase_read()));

    ringer_init();
    uart_proto_init();
    phone_fsm_init();

    bool banner_printed = false;

    uart_proto_send("STATUS:READY");

    // Scan keypad once at boot. If * is held, persist RECOVERY phase to
    // flash so the Pi can detect it after it finishes booting (the Pi
    // boots 15-30s after the Pico). The phase byte survives the boot
    // gap; digitsd queries it with PHASE? after POST.
    if (keypad_scan_raw() == '*') {
        phase_write(PHASE_RECOVERY);
        led_set_mode(LED_MODE_FAST_BLINK);
        uart_proto_send("BOOT:PANIC");
    }

    // Arm the hardware watchdog now that init (including any star-at-boot flash
    // write above) is done. pause_on_debug=true so a halted SWD session during
    // OTA reflash does not spuriously reset the chip.
    watchdog_enable(WATCHDOG_TIMEOUT_MS, true);

    while (true) {
        watchdog_update();

        if (!banner_printed && stdio_usb_connected()) {
            printf("\n===========================\n");
            printf(" Digits Firmware %s (%s)\n", FIRMWARE_VERSION, FIRMWARE_COMMIT);
            printf("===========================\n");
            printf("All subsystems initialized.\n");
            printf("Type commands: RING:START RING:STOP RING:TEST LED:ON LED:OFF LED:BLINK PING\n");
            printf("Debug: RESET STATE? HOOK:FORCE:OFF HOOK:FORCE:ON HOOK:RELEASE KEYTEST KEYTEST:OFF\n");
            printf("RING:TEST bypasses FSM+hook: rings regardless of hook state.\n");
            printf("> ");
            stdio_flush();
            banner_printed = true;
        }

        usb_console_poll();
        hook_poll();
        phone_fsm_update();
        sleep_ms(10);
    }

    return 0;
}
