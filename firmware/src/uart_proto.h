#ifndef DIGITS_UART_PROTO_H
#define DIGITS_UART_PROTO_H

#include <stddef.h>

#include "hardware/uart.h"

#define PROTO_UART_ID uart0
#define PROTO_UART_BAUD 115200
#define PROTO_MAX_LINE 128

void uart_proto_init(void);
void uart_proto_send(const char *msg);
const char *uart_proto_recv(void);
void uart_proto_inject(const char *cmd);

#endif  // DIGITS_UART_PROTO_H
