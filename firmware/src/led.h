#ifndef LED_H
#define LED_H

#include <stdint.h>

#ifndef HARDWARE_REV
#error "HARDWARE_REV not defined; set -DHARDWARE_REV=1 or =2 at configure time"
#endif

// V1 ElectroCookie prototype: status LED on GP14.
// V2 carrier PCB: LED_OUT on GP16 (U3.27) routed through R1 to J6.1.
#if HARDWARE_REV == 1
#define LED_PIN 14
#elif HARDWARE_REV == 2
#define LED_PIN 16
#else
#error "Unsupported HARDWARE_REV; must be 1 or 2"
#endif

typedef enum {
    LED_MODE_OFF = 0,
    LED_MODE_ON,
    LED_MODE_BLINK,
} led_mode_t;

void led_init(void);
void led_set_mode(led_mode_t mode);
void led_update(void);

#endif  // LED_H
