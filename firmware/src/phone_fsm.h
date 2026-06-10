#ifndef DIGITS_PHONE_FSM_H
#define DIGITS_PHONE_FSM_H

#include <stdbool.h>
#include <stdint.h>

typedef enum {
    PHONE_STATE_IDLE = 0,
    PHONE_STATE_DIAL_TONE,
    PHONE_STATE_DIALING,
    PHONE_STATE_RINGING,
    PHONE_STATE_CONNECTED,
    PHONE_STATE_BUSY,
} phone_state_t;

void phone_fsm_init(void);
void phone_fsm_update(void);
phone_state_t phone_fsm_get_state(void);
const char *phone_fsm_state_name(phone_state_t state);

#endif  // DIGITS_PHONE_FSM_H
