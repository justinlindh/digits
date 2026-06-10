#include "_sdk_shim.h"

#include "hardware/uart.h"

// Backing store for the fake UART RX byte queue plus the uart0 symbol the
// firmware names. A ring buffer large enough to hold several protocol lines
// (PROTO_MAX_LINE is 128) plus overflow probes.

struct uart_inst { int _id; };
static struct uart_inst s_uart0 = { 0 };
uart_inst_t *const uart0 = &s_uart0;

#define FAKE_UART_RX_CAP 1024
static char s_rx[FAKE_UART_RX_CAP];
static unsigned s_head = 0;  // next read
static unsigned s_tail = 0;  // next write
static unsigned s_count = 0;

void fake_uart_rx_reset(void) {
    s_head = 0;
    s_tail = 0;
    s_count = 0;
}

void fake_uart_rx_push(const char *bytes, unsigned len) {
    for (unsigned i = 0; i < len; ++i) {
        if (s_count >= FAKE_UART_RX_CAP) {
            return;  // queue full: drop (tests stay well under capacity)
        }
        s_rx[s_tail] = bytes[i];
        s_tail = (s_tail + 1) % FAKE_UART_RX_CAP;
        s_count++;
    }
}

int fake_uart_rx_readable(void) {
    return s_count > 0 ? 1 : 0;
}

char fake_uart_rx_getc(void) {
    if (s_count == 0) {
        return '\0';
    }
    char c = s_rx[s_head];
    s_head = (s_head + 1) % FAKE_UART_RX_CAP;
    s_count--;
    return c;
}
