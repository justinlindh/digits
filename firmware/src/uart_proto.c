#include "uart_proto.h"

#include <stdbool.h>
#include <stdio.h>
#include <string.h>

#include "hardware/gpio.h"

#include "board.h"

static char inject_buf[PROTO_MAX_LINE];
static bool inject_pending = false;

void uart_proto_init(void) {
    uart_init(PROTO_UART_ID, PROTO_UART_BAUD);
    gpio_set_function(board->uart_tx_pin, GPIO_FUNC_UART);
    gpio_set_function(board->uart_rx_pin, GPIO_FUNC_UART);

    uart_set_hw_flow(PROTO_UART_ID, false, false);
    uart_set_format(PROTO_UART_ID, 8, 1, UART_PARITY_NONE);
    uart_set_fifo_enabled(PROTO_UART_ID, true);
}

void uart_proto_send(const char *msg) {
    if (msg == NULL) {
        return;
    }

    uart_puts(PROTO_UART_ID, msg);
    uart_putc(PROTO_UART_ID, '\n');

    printf("UART TX: %s\n", msg);
}

void uart_proto_inject(const char *cmd) {
    if (cmd == NULL || inject_pending) return;
    strncpy(inject_buf, cmd, PROTO_MAX_LINE - 1);
    inject_buf[PROTO_MAX_LINE - 1] = '\0';
    inject_pending = true;
}

const char *uart_proto_recv(void) {
    // Return injected command first (from USB console)
    if (inject_pending) {
        inject_pending = false;
        return inject_buf;
    }

    static char line[PROTO_MAX_LINE];
    static size_t idx = 0;
    static bool overflowed = false;

    while (uart_is_readable(PROTO_UART_ID)) {
        char c = (char)uart_getc(PROTO_UART_ID);

        if (c == '\r' || c == '\n') {
            if (overflowed) {
                idx = 0;
                overflowed = false;
                continue;
            }

            if (idx == 0) {
                continue;
            }

            line[idx] = '\0';
            idx = 0;
            return line;
        }

        if (overflowed) {
            continue;
        }

        if (idx < (PROTO_MAX_LINE - 1)) {
            line[idx++] = c;
        } else {
            overflowed = true;
        }
    }

    return NULL;
}
