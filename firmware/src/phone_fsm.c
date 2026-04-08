#include "phone_fsm.h"

#include <stdbool.h>
#include <stdio.h>
#include <string.h>

#include "hook.h"
#include "keypad.h"
#include "led.h"
#include "ringer.h"
#include "tone.h"
#include "uart_proto.h"

#include "pico/stdlib.h"
#include "pico/time.h"

#define DIAL_DIGITS_REQUIRED 7
#define DIAL_TIMEOUT_MS 30000  // 30s for bench testing (restore to 5000 for real phone)
#define DIAL_TONE_TIMEOUT_MS 30000  // 30s of dial tone before off-hook timeout → busy

static phone_state_t s_state = PHONE_STATE_IDLE;
static char s_digits[DIAL_DIGITS_REQUIRED + 1];
static uint8_t s_digits_len = 0;
static uint32_t s_dialing_start_ms = 0;
static uint32_t s_dial_tone_start_ms = 0;
static bool s_dial_sent = false;
static bool s_keytest_mode = false;

static uint32_t now_ms(void) {
    return to_ms_since_boot(get_absolute_time());
}

static void clear_dialing_buffer(void) {
    memset(s_digits, 0, sizeof(s_digits));
    s_digits_len = 0;
    s_dial_sent = false;
    s_dialing_start_ms = now_ms();
}

const char *phone_fsm_state_name(phone_state_t state) {
    switch (state) {
        case PHONE_STATE_IDLE:
            return "IDLE";
        case PHONE_STATE_DIAL_TONE:
            return "DIAL_TONE";
        case PHONE_STATE_DIALING:
            return "DIALING";
        case PHONE_STATE_RINGING:
            return "RINGING";
        case PHONE_STATE_CONNECTED:
            return "CONNECTED";
        case PHONE_STATE_BUSY:
            return "BUSY";
        default:
            return "UNKNOWN";
    }
}

static void set_state(phone_state_t next) {
    if (s_state == next) {
        return;
    }

    s_state = next;

    switch (s_state) {
        case PHONE_STATE_IDLE:
            led_set_mode(LED_MODE_OFF);
            tone_stop();
            ringer_stop();
            clear_dialing_buffer();
            break;

        case PHONE_STATE_DIAL_TONE:
            led_set_mode(LED_MODE_ON);
            ringer_stop();
            clear_dialing_buffer();
            tone_play(TONE_DIAL);
            s_dial_tone_start_ms = now_ms();
            break;

        case PHONE_STATE_DIALING:
            led_set_mode(LED_MODE_ON);  // Stay lit while off-hook
            ringer_stop();
            tone_stop();
            s_dialing_start_ms = now_ms();
            break;

        case PHONE_STATE_RINGING:
            tone_stop();
            led_set_mode(LED_MODE_BLINK);
            ringer_start();
            uart_proto_send("RING:ACK");
            break;

        case PHONE_STATE_CONNECTED:
            ringer_stop();
            tone_stop();
            led_set_mode(LED_MODE_ON);
            break;

        case PHONE_STATE_BUSY:
            ringer_stop();
            led_set_mode(LED_MODE_ON);  // Still off-hook during busy
            tone_play(TONE_BUSY);
            break;

        default:
            break;
    }

    printf("FSM:%s\n", phone_fsm_state_name(s_state));
    stdio_flush();
}

static void process_pi_command(const char *cmd) {
    if (cmd == NULL || cmd[0] == '\0') {
        return;
    }

    if (strcmp(cmd, "RING:START") == 0) {
        // Flush any pending hook event (e.g. from HOOK:FORCE:ON sent just before)
        // to prevent the on-hook event from immediately reverting us to IDLE.
        bool dummy;
        hook_get_event(&dummy);
        set_state(PHONE_STATE_RINGING);
    } else if (strcmp(cmd, "RING:TEST") == 0) {
        // Direct hardware test — bypass FSM entirely.
        // Drives ringer + LED regardless of hook state.
        ringer_start();
        led_set_mode(LED_MODE_BLINK);
        uart_proto_send("RING:TEST:ACK");
    } else if (strcmp(cmd, "RING:STOP") == 0) {
        if (s_state == PHONE_STATE_RINGING) {
            set_state(PHONE_STATE_IDLE);
            uart_proto_send("RING:DONE");
        } else {
            ringer_stop();
            led_set_mode(LED_MODE_OFF);  // Clean up LED from RING:TEST
            uart_proto_send("RING:DONE");
        }
    } else if (strcmp(cmd, "LED:ON") == 0) {
        led_set_mode(LED_MODE_ON);
    } else if (strcmp(cmd, "LED:OFF") == 0) {
        led_set_mode(LED_MODE_OFF);
    } else if (strcmp(cmd, "LED:BLINK") == 0) {
        led_set_mode(LED_MODE_BLINK);
    } else if (strcmp(cmd, "TONE:DIAL") == 0) {
        tone_play(TONE_DIAL);
    } else if (strcmp(cmd, "TONE:RINGBACK") == 0) {
        tone_play(TONE_RINGBACK);
    } else if (strcmp(cmd, "TONE:STOP") == 0) {
        tone_stop();
    } else if (strcmp(cmd, "PING") == 0) {
        uart_proto_send("PONG");
    } else if (strcmp(cmd, "VERSION") == 0) {
        char resp[64];
        snprintf(resp, sizeof(resp), "VERSION:%s:%s", FIRMWARE_VERSION, FIRMWARE_COMMIT);
        uart_proto_send(resp);
    } else if (strcmp(cmd, "RESET") == 0) {
        s_keytest_mode = false;
        set_state(PHONE_STATE_IDLE);
        uart_proto_send("RST:OK");
    } else if (strcmp(cmd, "STATE?") == 0) {
        char buf[32];
        snprintf(buf, sizeof(buf), "STATE:%s", phone_fsm_state_name(s_state));
        uart_proto_send(buf);
        if (hook_is_forced()) {
            uart_proto_send(hook_is_off_hook() ? "HOOK:FORCED:OFF_HOOK" : "HOOK:FORCED:ON_HOOK");
        } else {
            uart_proto_send(hook_is_off_hook() ? "HOOK:OFF_HOOK" : "HOOK:ON_HOOK");
        }
        if (s_keytest_mode) {
            uart_proto_send("MODE:KEYTEST");
        }
    } else if (strcmp(cmd, "HOOK:FORCE:OFF") == 0) {
        hook_force_off_hook(true);
        uart_proto_send("HOOK:FORCED:OFF_HOOK");
    } else if (strcmp(cmd, "HOOK:FORCE:ON") == 0) {
        hook_force_off_hook(false);
        uart_proto_send("HOOK:FORCED:ON_HOOK");
    } else if (strcmp(cmd, "HOOK:RELEASE") == 0) {
        hook_clear_force();
        uart_proto_send("HOOK:RELEASED");
    } else if (strcmp(cmd, "HOOK:INVERT:ON") == 0) {
        hook_set_inverted(true);
        uart_proto_send("HOOK:INVERT:ON");
    } else if (strcmp(cmd, "HOOK:INVERT:OFF") == 0) {
        hook_set_inverted(false);
        uart_proto_send("HOOK:INVERT:OFF");
    } else if (strcmp(cmd, "KEYTEST") == 0) {
        s_keytest_mode = true;
        set_state(PHONE_STATE_IDLE);
        uart_proto_send("MODE:KEYTEST");
    } else if (strcmp(cmd, "KEYTEST:OFF") == 0) {
        s_keytest_mode = false;
        uart_proto_send("MODE:NORMAL");
    } else if (strcmp(cmd, "KEYDUMP") == 0) {
        // Raw GPIO state dump for all keypad pins
        char buf[128];
        // Read row pins (outputs — show what we're driving)
        snprintf(buf, sizeof(buf), "ROWS: R0/GP2=%d R1/GP3=%d R2/GP4=%d R3/GP5=%d",
                 gpio_get(2), gpio_get(3), gpio_get(4), gpio_get(5));
        uart_proto_send(buf);
        printf("%s\n", buf);
        // Read col pins (inputs — show what we're sensing)
        snprintf(buf, sizeof(buf), "COLS: C0/GP6=%d C1/GP7=%d C2/GP8=%d C3/GP9=%d",
                 gpio_get(6), gpio_get(7), gpio_get(8), gpio_get(9));
        uart_proto_send(buf);
        printf("%s\n", buf);
        // Now drive each row LOW and read columns
        for (int row = 0; row < 4; ++row) {
            uint8_t rpin = (row < 2) ? (2 + row) : (2 + row);  // GP2-GP5
            gpio_put(rpin, 0);
            sleep_us(50);  // generous settle
            snprintf(buf, sizeof(buf), "SCAN R%d/GP%d=LOW: C0=%d C1=%d C2=%d C3=%d",
                     row, rpin,
                     gpio_get(6), gpio_get(7), gpio_get(8), gpio_get(9));
            uart_proto_send(buf);
            printf("%s\n", buf);
            gpio_put(rpin, 1);
            sleep_us(50);
        }
        stdio_flush();
    }
}

static void process_key(char key) {
    if (key == '\0') {
        return;
    }

    if (s_state == PHONE_STATE_DIAL_TONE) {
        set_state(PHONE_STATE_DIALING);
    }

    if (s_state != PHONE_STATE_DIALING) {
        return;
    }

    tone_play_dtmf(key);

    char key_msg[8];
    snprintf(key_msg, sizeof(key_msg), "KEY:%c", key);
    uart_proto_send(key_msg);

    if (key >= '0' && key <= '9' && s_digits_len < DIAL_DIGITS_REQUIRED) {
        s_digits[s_digits_len++] = key;
        s_digits[s_digits_len] = '\0';
    }

    if (s_digits_len == DIAL_DIGITS_REQUIRED && !s_dial_sent) {
        char dial_msg[16];
        snprintf(dial_msg, sizeof(dial_msg), "DIAL:%s", s_digits);
        uart_proto_send(dial_msg);
        tone_play(TONE_RINGBACK);
        s_dial_sent = true;
    }
}

void phone_fsm_init(void) {
    s_state = PHONE_STATE_IDLE;
    clear_dialing_buffer();
    set_state(PHONE_STATE_IDLE);
}

void phone_fsm_update(void) {
    // Always process UART commands regardless of mode
    const char *line = NULL;
    while ((line = uart_proto_recv()) != NULL) {
        process_pi_command(line);
    }

    // Keytest mode: bypass FSM entirely, just report raw keypresses
    if (s_keytest_mode) {
        char key = keypad_scan();
        if (key != '\0') {
            char key_msg[8];
            snprintf(key_msg, sizeof(key_msg), "KEY:%c", key);
            uart_proto_send(key_msg);
            printf("🔢 RAW %s\n", key_msg);
            stdio_flush();
        }
        led_update();
        return;
    }

    // Normal FSM operation
    bool off_hook_event = false;
    if (hook_get_event(&off_hook_event)) {
        printf("HOOK:%s\n", off_hook_event ? "OFF" : "ON");
        stdio_flush();
        uart_proto_send(off_hook_event ? "HOOK:OFF" : "HOOK:ON");
        if (!off_hook_event) {
            set_state(PHONE_STATE_IDLE);
        }
    }

    if (!hook_is_off_hook() && s_state != PHONE_STATE_IDLE &&
        s_state != PHONE_STATE_RINGING) {
        set_state(PHONE_STATE_IDLE);
    }

    if (hook_is_off_hook() && s_state == PHONE_STATE_IDLE) {
        set_state(PHONE_STATE_DIAL_TONE);
    }

    if (s_state == PHONE_STATE_RINGING && hook_is_off_hook()) {
        set_state(PHONE_STATE_CONNECTED);
    }

    // Off-hook timeout: dial tone → busy if no keys pressed
    if (s_state == PHONE_STATE_DIAL_TONE &&
        (now_ms() - s_dial_tone_start_ms) >= DIAL_TONE_TIMEOUT_MS) {
        uart_proto_send("TIMEOUT:DIAL_TONE");
        set_state(PHONE_STATE_BUSY);
    }

    process_key(keypad_scan());

    if (s_state == PHONE_STATE_DIALING && !s_dial_sent && s_digits_len > 0) {
        if ((now_ms() - s_dialing_start_ms) >= DIAL_TIMEOUT_MS) {
            set_state(PHONE_STATE_BUSY);
        }
    }

    led_update();
    tone_update();
    ringer_update();
}

void phone_fsm_reset(void) {
    s_keytest_mode = false;
    set_state(PHONE_STATE_IDLE);
}

void phone_fsm_set_keytest(bool enable) {
    s_keytest_mode = enable;
    if (enable) {
        set_state(PHONE_STATE_IDLE);
    }
}

bool phone_fsm_is_keytest(void) {
    return s_keytest_mode;
}

phone_state_t phone_fsm_get_state(void) {
    return s_state;
}
