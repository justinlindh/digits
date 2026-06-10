#include <stdbool.h>
#include <stdio.h>

#include "hardware/gpio.h"
#include "pico/stdlib.h"

#include "board.h"
#include "hook.h"
#include "keypad.h"
#include "led.h"
#include "phase.h"
#include "phone_fsm.h"
#include "ringer.h"
#include "tone.h"
#include "uart_proto.h"

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

    switch (phase_read()) {
    case PHASE_PAIRED:
        led_set_mode(LED_MODE_BREATHING);
        break;
    case PHASE_UNPAIRED:
        led_set_mode(LED_MODE_SLOW_PULSE);
        break;
    case PHASE_SETUP:
        led_set_mode(LED_MODE_DOUBLE_PULSE);
        break;
    case PHASE_RECOVERY:
        led_set_mode(LED_MODE_FAST_BLINK);
        break;
    default:
        break;
    }

    tone_init();
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

    while (true) {
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
