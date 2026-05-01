#ifndef LED_H
#define LED_H

#include <stdint.h>

typedef enum {
    LED_MODE_OFF = 0,
    LED_MODE_ON,
    LED_MODE_BLINK,
    // LED_MODE_SLOW_BLINK is a longer-period blink (~2s) used for advisory
    // indications such as a waiting voicemail message. Visually distinct
    // from LED_MODE_BLINK so a glance can tell ringing from message-waiting.
    LED_MODE_SLOW_BLINK,
} led_mode_t;

void led_init(void);
void led_set_mode(led_mode_t mode);
void led_update(void);

#endif  // LED_H
