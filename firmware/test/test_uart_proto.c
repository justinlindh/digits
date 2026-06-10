// Behavioral tests for uart_proto.c RX line-framing and overflow recovery.
//
// uart_proto_recv() reads available bytes from the UART, accumulates them into
// a static line buffer (PROTO_MAX_LINE == 128), and returns a pointer to the
// completed line on CR or LF. Empty lines (bare terminators) are skipped. A
// line longer than the buffer sets an overflow flag: the remainder of that
// oversized line is discarded, and the NEXT terminator clears the flag and
// resyncs, so a too-long line cannot corrupt or truncate-merge into the line
// that follows it.

#include "test_harness.h"

#include "_sdk_shim.h"
#include "fake_env.h"
#include "uart_proto.h"

static void feed(const char *s) {
    fake_uart_rx_push(s, (unsigned)strlen(s));
}

static void test_uart_single_line(void) {
    fake_env_reset();
    fake_uart_rx_reset();
    uart_proto_init();

    feed("RING:START\n");
    const char *line = uart_proto_recv();
    CHECK(line != NULL);
    CHECK_STREQ(line, "RING:START");
    // No more bytes => NULL.
    CHECK(uart_proto_recv() == NULL);
}

static void test_uart_multiple_lines_one_buffer(void) {
    fake_env_reset();
    fake_uart_rx_reset();
    uart_proto_init();

    feed("PING\nSTATE?\nLED:ON\n");
    CHECK_STREQ(uart_proto_recv(), "PING");
    CHECK_STREQ(uart_proto_recv(), "STATE?");
    CHECK_STREQ(uart_proto_recv(), "LED:ON");
    CHECK(uart_proto_recv() == NULL);
}

static void test_uart_crlf_and_blank_lines_skipped(void) {
    fake_env_reset();
    fake_uart_rx_reset();
    uart_proto_init();

    // CRLF terminators and stray blank lines must not produce empty tokens.
    feed("PING\r\n\r\n\nPONG\r\n");
    CHECK_STREQ(uart_proto_recv(), "PING");
    CHECK_STREQ(uart_proto_recv(), "PONG");
    CHECK(uart_proto_recv() == NULL);
}

static void test_uart_partial_then_completed(void) {
    fake_env_reset();
    fake_uart_rx_reset();
    uart_proto_init();

    // A line can arrive in fragments across multiple recv() calls; recv()
    // returns NULL until the terminator shows up.
    feed("BOAR");
    CHECK(uart_proto_recv() == NULL);
    feed("D?");
    CHECK(uart_proto_recv() == NULL);
    feed("\n");
    CHECK_STREQ(uart_proto_recv(), "BOARD?");
}

static void test_uart_overflow_discarded_and_resyncs(void) {
    fake_env_reset();
    fake_uart_rx_reset();
    uart_proto_init();

    // Build a line longer than PROTO_MAX_LINE (128) with no terminator, then
    // terminate it, then send a clean command. The oversized line must be
    // dropped entirely and the following command must come through intact.
    char big[200];
    for (int i = 0; i < 200; ++i) {
        big[i] = 'X';
    }
    fake_uart_rx_push(big, 200);
    feed("\n");          // terminates the overflowed line: dropped + resync
    feed("PING\n");      // clean line after overflow

    // The overflowed line yields nothing; the next clean line frames normally.
    const char *line = uart_proto_recv();
    CHECK(line != NULL);
    CHECK_STREQ(line, "PING");
    CHECK(uart_proto_recv() == NULL);
}

static void test_uart_max_length_line_fits(void) {
    fake_env_reset();
    fake_uart_rx_reset();
    uart_proto_init();

    // Exactly PROTO_MAX_LINE-1 (127) payload bytes is the largest line that
    // fits without tripping overflow.
    char line127[PROTO_MAX_LINE];
    for (int i = 0; i < PROTO_MAX_LINE - 1; ++i) {
        line127[i] = 'A';
    }
    line127[PROTO_MAX_LINE - 1] = '\0';

    feed(line127);
    feed("\n");
    const char *got = uart_proto_recv();
    CHECK(got != NULL);
    CHECK_EQ((int)strlen(got), PROTO_MAX_LINE - 1);
    CHECK_STREQ(got, line127);
}

static void test_uart_inject_takes_priority(void) {
    fake_env_reset();
    fake_uart_rx_reset();
    uart_proto_init();

    // An injected console command is returned before UART bytes.
    feed("FROM_UART\n");
    uart_proto_inject("FROM_USB");
    CHECK_STREQ(uart_proto_recv(), "FROM_USB");
    CHECK_STREQ(uart_proto_recv(), "FROM_UART");
}

#define T(fn) {#fn, fn}
static const test_case_t k_uart_tests[] = {
    T(test_uart_single_line),
    T(test_uart_multiple_lines_one_buffer),
    T(test_uart_crlf_and_blank_lines_skipped),
    T(test_uart_partial_then_completed),
    T(test_uart_overflow_discarded_and_resyncs),
    T(test_uart_max_length_line_fits),
    T(test_uart_inject_takes_priority),
};
#undef T

const test_case_t *uart_tests(int *count);
const test_case_t *uart_tests(int *count) {
    *count = (int)(sizeof(k_uart_tests) / sizeof(k_uart_tests[0]));
    return k_uart_tests;
}
