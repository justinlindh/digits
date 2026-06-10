#include "_sdk_shim.h"

#include <stdbool.h>
#include <string.h>

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

// --- TX capture --------------------------------------------------------------
// uart_proto_send writes the message then a '\n', so the TX buffer holds
// newline-terminated lines. A linear buffer (not a ring) keeps line scanning
// simple; tests stay well under capacity.

#define FAKE_UART_TX_CAP 2048
static char s_tx[FAKE_UART_TX_CAP];
static unsigned s_tx_len = 0;

void fake_uart_tx_reset(void) {
    s_tx_len = 0;
    s_tx[0] = '\0';
}

void fake_uart_tx_write(const char *s) {
    if (s == NULL) {
        return;
    }
    for (; *s != '\0'; ++s) {
        if (s_tx_len >= FAKE_UART_TX_CAP - 1) {
            return;  // buffer full: drop (tests stay well under capacity)
        }
        s_tx[s_tx_len++] = *s;
    }
    s_tx[s_tx_len] = '\0';
}

int fake_uart_tx_count_lines_with_prefix(const char *prefix) {
    if (prefix == NULL) {
        return 0;
    }
    size_t plen = strlen(prefix);
    int count = 0;
    unsigned i = 0;
    while (i < s_tx_len) {
        // Identify the current line span [i, j) up to the next newline.
        unsigned j = i;
        while (j < s_tx_len && s_tx[j] != '\n') {
            ++j;
        }
        // Only count completed (newline-terminated) lines.
        bool terminated = (j < s_tx_len);
        if (terminated && (size_t)(j - i) >= plen &&
            strncmp(&s_tx[i], prefix, plen) == 0) {
            ++count;
        }
        i = j + 1;  // skip the newline
    }
    return count;
}
