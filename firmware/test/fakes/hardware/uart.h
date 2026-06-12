#ifndef DIGITS_TEST_HARDWARE_UART_H
#define DIGITS_TEST_HARDWARE_UART_H

// Host stand-in for <hardware/uart.h>. The RX path is backed by a byte queue
// in the fake engine so uart_proto_recv() line-framing can be tested: a test
// pushes raw bytes with fake_uart_rx_push(), then calls uart_proto_recv(). The
// TX path is captured into a byte buffer so tests can assert on the lines the
// firmware emits (e.g. exactly one DIAL:<digits> on a completed dial).

#include <stddef.h>
#include <stdbool.h>
#include <stdint.h>

#include "_sdk_shim.h"

typedef struct uart_inst uart_inst_t;
extern uart_inst_t *const uart0;

#define UART_PARITY_NONE 0

static inline void uart_init(uart_inst_t *u, uint32_t baud) { (void)u; (void)baud; }
static inline void uart_set_hw_flow(uart_inst_t *u, bool cts, bool rts) {
    (void)u; (void)cts; (void)rts;
}
static inline void uart_set_format(uart_inst_t *u, int data, int stop, int parity) {
    (void)u; (void)data; (void)stop; (void)parity;
}
static inline void uart_set_fifo_enabled(uart_inst_t *u, bool en) { (void)u; (void)en; }

static inline void uart_puts(uart_inst_t *u, const char *s) {
    (void)u;
    fake_uart_tx_write(s);
}
static inline void uart_putc(uart_inst_t *u, char c) {
    (void)u;
    char s[2] = {c, '\0'};
    fake_uart_tx_write(s);
}

static inline bool uart_is_readable(uart_inst_t *u) {
    (void)u;
    return fake_uart_rx_readable() != 0;
}
static inline char uart_getc(uart_inst_t *u) {
    (void)u;
    return fake_uart_rx_getc();
}

#endif  // DIGITS_TEST_HARDWARE_UART_H
