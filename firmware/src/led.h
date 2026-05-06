#ifndef LED_H
#define LED_H

#include <stdbool.h>
#include <stdint.h>

typedef enum {
    LED_MODE_OFF = 0,
    LED_MODE_ON,
    LED_MODE_BLINK,
    LED_MODE_FAST_BLINK,
    LED_MODE_DOUBLE_PULSE,
    LED_MODE_CONNECTING,
} led_mode_t;

void led_init(void);
void led_set_mode(led_mode_t mode);
void led_update(void);
void led_set_locked(bool locked);
bool led_is_locked(void);

#endif  // LED_H
