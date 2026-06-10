#ifndef DIGITS_TEST_SDK_SHIM_H
#define DIGITS_TEST_SDK_SHIM_H

// Backing functions shared by the fake SDK headers. Kept separate from
// fake_env.h so the public test API stays small.

#include <stdint.h>

uint64_t fake_sdk_time_us(void);
int fake_sdk_gpio_get(unsigned pin);
void fake_sdk_gpio_put(unsigned pin, int value);

// Captures bytes the firmware feeds to the fake UART RX so uart_proto_recv()
// can frame them into lines. Tests push raw bytes via fake_uart_rx_push().
void fake_uart_rx_push(const char *bytes, unsigned len);
void fake_uart_rx_reset(void);
int fake_uart_rx_readable(void);
char fake_uart_rx_getc(void);

#endif  // DIGITS_TEST_SDK_SHIM_H
