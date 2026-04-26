#ifndef DIGITS_UART_PROTO_H
#define DIGITS_UART_PROTO_H

#include <stddef.h>

#include "hardware/uart.h"

#ifndef HARDWARE_REV
#error "HARDWARE_REV not defined; set -DHARDWARE_REV=1 or =2 at configure time"
#endif

#define PROTO_UART_ID uart0

#if HARDWARE_REV == 1
// V1/prototype on ElectroCookie + Pico H module: UART on GP0/GP1.
#define PROTO_UART_TX_PIN 0
#define PROTO_UART_RX_PIN 1
#elif HARDWARE_REV == 2
// V2 carrier: UART on GP28/GP29 per the schematic. GP28 (TX) -> Pi RX (J1.10),
// GP29 (RX) -> Pi TX (J1.8). Both are valid uart0 alt-function pins per
// RP2040 datasheet table 2-19.
#define PROTO_UART_TX_PIN 28
#define PROTO_UART_RX_PIN 29
#else
#error "Unsupported HARDWARE_REV; must be 1 or 2"
#endif

#define PROTO_UART_BAUD 115200
#define PROTO_MAX_LINE 128

void uart_proto_init(void);
void uart_proto_send(const char *msg);
const char *uart_proto_recv(void);
void uart_proto_inject(const char *cmd);

#endif  // DIGITS_UART_PROTO_H
