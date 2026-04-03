#include <stdbool.h>
#include <stdio.h>

#include "pico/stdlib.h"

#include "hook.h"
#include "keypad.h"
#include "led.h"
#include "phone_fsm.h"
#include "ringer.h"
#include "tone.h"
#include "uart_proto.h"

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
    stdio_init_all();

    hook_init();
    keypad_init();
    led_init();
    tone_init();
    ringer_init();
    uart_proto_init();
    phone_fsm_init();

    bool banner_printed = false;

    uart_proto_send("STATUS:READY");

    while (true) {
        if (!banner_printed && stdio_usb_connected()) {
            printf("\n===========================\n");
            printf(" Digits Firmware %s (%s)\n", FIRMWARE_VERSION, FIRMWARE_COMMIT);
            printf("===========================\n");
            printf("All subsystems initialized.\n");
            printf("Type commands: RING:START RING:STOP RING:TEST LED:ON LED:OFF LED:BLINK PING\n");
            printf("Debug: RESET STATE? HOOK:FORCE:OFF HOOK:FORCE:ON HOOK:RELEASE KEYTEST KEYTEST:OFF\n");
            printf("RING:TEST bypasses FSM+hook — rings regardless of hook state.\n");
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
