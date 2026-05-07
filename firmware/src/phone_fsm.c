#include "phone_fsm.h"

#include <stdarg.h>
#include <stdbool.h>
#include <stdio.h>
#include <string.h>

#include "board.h"
#include "hook.h"
#include "keypad.h"
#include "led.h"
#include "phase.h"
#include "ringer.h"
#include "tone.h"
#include "uart_proto.h"

// fsm_led_set resolves the LED pattern from (phase, fsm_state). When the Pi
// has locked the LED (e.g. CONNECTING during WiFi verify), FSM changes are
// skipped. When the FSM requests OFF (idle/on-hook), the phase determines
// the idle pattern so each mode gets the right ambient indicator.
static void fsm_led_set(led_mode_t mode) {
    if (led_is_locked()) {
        return;
    }
    if (mode == LED_MODE_OFF) {
        switch (phase_read()) {
        case PHASE_UNPAIRED: led_set_mode(LED_MODE_SLOW_PULSE); return;
        case PHASE_SETUP:    led_set_mode(LED_MODE_DOUBLE_PULSE); return;
        case PHASE_RECOVERY: led_set_mode(LED_MODE_FAST_BLINK); return;
        default: break;
        }
    }
    led_set_mode(mode);
}

#include "hardware/watchdog.h"
#include "pico/bootrom.h"
#include "pico/stdlib.h"
#include "pico/time.h"

#define DIAL_DIGITS_REQUIRED 7
#define DIAL_TIMEOUT_MS 15000        // 15s between digits before partial dial → off-hook timeout
#define DIAL_TONE_TIMEOUT_MS 15000   // 15s of dial tone with no keys → off-hook timeout (Bellcore GR-506-CORE)

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

// Saturating snprintf accumulator. Appends a formatted string into buf at
// position *pos and updates *pos. If the buffer is already full, returns
// without writing. If the formatted output would overflow, truncates and
// pins *pos at buf_size - 1 so subsequent calls become no-ops without
// underflowing the size_t arithmetic that a naive `n += snprintf(buf + n,
// sizeof(buf) - n, ...)` pattern is prone to.
static void buf_appendf(char *buf, size_t buf_size, size_t *pos,
                        const char *fmt, ...) {
    if (buf_size == 0 || *pos >= buf_size) {
        return;
    }
    va_list ap;
    va_start(ap, fmt);
    int written = vsnprintf(buf + *pos, buf_size - *pos, fmt, ap);
    va_end(ap);
    if (written <= 0) {
        return;
    }
    if ((size_t)written >= buf_size - *pos) {
        *pos = buf_size - 1;
    } else {
        *pos += (size_t)written;
    }
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
            fsm_led_set(LED_MODE_OFF);
            tone_stop();
            ringer_stop();
            clear_dialing_buffer();
            break;

        case PHONE_STATE_DIAL_TONE:
            fsm_led_set(LED_MODE_ON);
            ringer_stop();
            clear_dialing_buffer();
            tone_play(TONE_DIAL);
            s_dial_tone_start_ms = now_ms();
            break;

        case PHONE_STATE_DIALING:
            fsm_led_set(LED_MODE_ON);
            ringer_stop();
            tone_stop();
            s_dialing_start_ms = now_ms();
            break;

        case PHONE_STATE_RINGING:
            tone_stop();
            fsm_led_set(LED_MODE_BLINK);
            ringer_start();
            uart_proto_send("RING:ACK");
            break;

        case PHONE_STATE_CONNECTED:
            ringer_stop();
            tone_stop();
            fsm_led_set(LED_MODE_BREATHING);
            break;

        case PHONE_STATE_BUSY:
            ringer_stop();
            fsm_led_set(LED_MODE_ON);
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
        hook_get_event();
        set_state(PHONE_STATE_RINGING);
    } else if (strcmp(cmd, "RING:TEST") == 0) {
        // Direct hardware test — bypass FSM entirely.
        // Drives ringer + LED regardless of hook state.
        ringer_start();
        fsm_led_set(LED_MODE_BLINK);
        uart_proto_send("RING:TEST:ACK");
    } else if (strcmp(cmd, "RING:STOP") == 0) {
        if (s_state == PHONE_STATE_RINGING) {
            set_state(PHONE_STATE_IDLE);
            uart_proto_send("RING:DONE");
        } else {
            ringer_stop();
            fsm_led_set(LED_MODE_OFF);
            uart_proto_send("RING:DONE");
        }
    } else if (strcmp(cmd, "LED:ON") == 0) {
        led_set_mode(LED_MODE_ON);
    } else if (strcmp(cmd, "LED:OFF") == 0) {
        led_set_mode(LED_MODE_OFF);
    } else if (strcmp(cmd, "LED:BLINK") == 0) {
        led_set_mode(LED_MODE_BLINK);
    } else if (strcmp(cmd, "LED:FAST_BLINK") == 0) {
        led_set_mode(LED_MODE_FAST_BLINK);
    } else if (strcmp(cmd, "LED:DOUBLE_PULSE") == 0) {
        led_set_mode(LED_MODE_DOUBLE_PULSE);
    } else if (strcmp(cmd, "LED:CONNECTING") == 0) {
        led_set_mode(LED_MODE_CONNECTING);
    } else if (strcmp(cmd, "LED:BREATHING") == 0) {
        led_set_mode(LED_MODE_BREATHING);
    } else if (strcmp(cmd, "LED:SLOW_PULSE") == 0) {
        led_set_mode(LED_MODE_SLOW_PULSE);
    } else if (strcmp(cmd, "LED:LOCK") == 0) {
        led_set_locked(true);
    } else if (strcmp(cmd, "LED:UNLOCK") == 0) {
        led_set_locked(false);
    } else if (strcmp(cmd, "TONE:DIAL") == 0) {
        tone_play(TONE_DIAL);
    } else if (strcmp(cmd, "TONE:RINGBACK") == 0) {
        tone_play(TONE_RINGBACK);
    } else if (strcmp(cmd, "TONE:STOP") == 0) {
        tone_stop();
    } else if (strcmp(cmd, "CALL:CONNECTED") == 0) {
        // Caller-side: Pi sends this after the WebRTC peer answers.
        // RINGING is accepted defensively; callees normally self-transition
        // to CONNECTED via hook-off detection.
        if (s_state == PHONE_STATE_DIALING || s_state == PHONE_STATE_RINGING) {
            set_state(PHONE_STATE_CONNECTED);
            uart_proto_send("CALL:CONNECTED:ACK");
        } else {
            uart_proto_send("CALL:CONNECTED:IGNORED");
        }
    } else if (strcmp(cmd, "PING") == 0) {
        uart_proto_send("PONG");
    } else if (strcmp(cmd, "BOARD?") == 0) {
        char buf[64];
        snprintf(buf, sizeof(buf), "BOARD:%s:0x%02X", board->name, (unsigned int)board_read_rev_byte());
        uart_proto_send(buf);
    } else if (strncmp(cmd, "CONFIG:PCB_REV=", 15) == 0) {
        // Swaps the active board profile pointer only. Modules read pins
        // live from `board->...` on every access, so subsequent reads target
        // the new profile's pins. Those pins were never gpio_init'd,
        // gpio_set_dir'd, or gpio_pull_up'd: only the original profile's
        // pins were configured at boot. The override is therefore reliable
        // only across a firmware reboot, which the Pi-side flow already
        // guarantees: digitsd sends this on a mismatch, then flash-pico.sh
        // writes the rev byte and the next reset picks the right profile
        // from the start.
        const char* name = cmd + 15;
        char buf[64];
        if (board_set_profile(name)) {
            snprintf(buf, sizeof(buf), "CONFIG:PCB_REV=%s:OK", name);
        } else {
            snprintf(buf, sizeof(buf), "CONFIG:PCB_REV=%s:UNKNOWN", name);
        }
        uart_proto_send(buf);
    } else if (strcmp(cmd, "VERSION") == 0) {
        char resp[64];
        snprintf(resp, sizeof(resp), "VERSION:%s:%s", FIRMWARE_VERSION, FIRMWARE_COMMIT);
        uart_proto_send(resp);
    } else if (strcmp(cmd, "RESET") == 0) {
        s_keytest_mode = false;
        set_state(PHONE_STATE_IDLE);
        uart_proto_send("RST:OK");
    } else if (strcmp(cmd, "DIAL:RESET") == 0) {
        // Drop accumulated digits without changing state. digitsd uses this
        // after a service-code completion or confirmer cancel: the user
        // typed e.g. "*#73887#" while off-hook, the firmware accumulated
        // "73887" in its dialing buffer, and after the service code matches
        // we don't want the next 2 dialed digits to surprise-fire DIAL on
        // a stale prefix.
        clear_dialing_buffer();
        uart_proto_send("DIAL:RESET:OK");
    } else if (strcmp(cmd, "STATE:SET:PAIRED") == 0) {
        phase_write(PHASE_PAIRED);
        uart_proto_send("STATE:SET:OK");
    } else if (strcmp(cmd, "STATE:SET:UNPAIRED") == 0) {
        phase_write(PHASE_UNPAIRED);
        uart_proto_send("STATE:SET:OK");
    } else if (strcmp(cmd, "STATE:SET:SETUP") == 0) {
        phase_write(PHASE_SETUP);
        uart_proto_send("STATE:SET:OK");
    } else if (strcmp(cmd, "STATE:SET:RECOVERY") == 0) {
        phase_write(PHASE_RECOVERY);
        uart_proto_send("STATE:SET:OK");
    } else if (strcmp(cmd, "REBOOT") == 0 || strcmp(cmd, "REBOOT:BOOTSEL") == 0) {
        // Reboot the chip into BOOTSEL mode (USB MSD + PICOBOOT). The chip
        // resets and stays in bootrom waiting for a USB host connection
        // that on V2 will never come, which means SWD has unlimited time
        // to grab the cores and reflash. Required for headless OTA: a
        // plain watchdog reset puts the chip back in firmware within ~10 ms
        // (way faster than openocd's ~200 ms init), and the chip's bootrom
        // window is too narrow to race.
        //
        // To exit BOOTSEL: openocd `reset run` after programming, or a
        // power cycle.
        uart_proto_send("REBOOT:OK");
        // Give the UART TX FIFO time to flush before we kill the CPU.
        sleep_ms(50);
        // disable_interface_mask = 0 (enable both UF2 and PICOBOOT),
        // usb_activity_gpio_pin_mask = 0 (no LED indicator).
        reset_usb_boot(0, 0);
        while (true) {
            tight_loop_contents();
        }
    } else if (strcmp(cmd, "REBOOT:WATCHDOG") == 0) {
        // Soft watchdog reset back into firmware (no flash window). Useful
        // when you want to reset firmware state without dropping into
        // bootrom. flash-pico.sh does NOT use this path.
        uart_proto_send("REBOOT:OK");
        sleep_ms(50);
        watchdog_enable(1, 1);
        while (true) {
            tight_loop_contents();
        }
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
    } else if (strcmp(cmd, "HOOK:FLASH:ON") == 0) {
        hook_set_flash_enabled(true);
        uart_proto_send("HOOK:FLASH:ON");
    } else if (strcmp(cmd, "HOOK:FLASH:OFF") == 0) {
        hook_set_flash_enabled(false);
        uart_proto_send("HOOK:FLASH:OFF");
    } else if (strcmp(cmd, "KEYTEST") == 0) {
        s_keytest_mode = true;
        set_state(PHONE_STATE_IDLE);
        uart_proto_send("MODE:KEYTEST");
    } else if (strcmp(cmd, "KEYTEST:OFF") == 0) {
        s_keytest_mode = false;
        uart_proto_send("MODE:NORMAL");
    } else if (strcmp(cmd, "KEYDUMP") == 0) {
        // Raw GPIO state dump for all keypad pins. Pin assignments and column
        // count come from the active board profile (V1 = 4 cols, V2 = 3).
        char buf[160];
        const uint num_cols = board->keypad_num_cols;
        // Read row pins (outputs: show what we're driving)
        size_t n = 0;
        buf_appendf(buf, sizeof(buf), &n, "ROWS:");
        for (int r = 0; r < 4; ++r) {
            buf_appendf(buf, sizeof(buf), &n, " R%d/GP%u=%d",
                        r, board->keypad_rows[r], gpio_get(board->keypad_rows[r]));
        }
        uart_proto_send(buf);
        printf("%s\n", buf);
        // Read col pins (inputs: show what we're sensing)
        n = 0;
        buf_appendf(buf, sizeof(buf), &n, "COLS:");
        for (uint c = 0; c < num_cols; ++c) {
            buf_appendf(buf, sizeof(buf), &n, " C%u/GP%u=%d",
                        c, board->keypad_cols[c], gpio_get(board->keypad_cols[c]));
        }
        uart_proto_send(buf);
        printf("%s\n", buf);
        // Now drive each row LOW and read columns
        for (int row = 0; row < 4; ++row) {
            gpio_put(board->keypad_rows[row], 0);
            sleep_us(50);
            n = 0;
            buf_appendf(buf, sizeof(buf), &n, "SCAN R%d/GP%u=LOW:",
                        row, board->keypad_rows[row]);
            for (uint c = 0; c < num_cols; ++c) {
                buf_appendf(buf, sizeof(buf), &n, " C%u=%d",
                            c, gpio_get(board->keypad_cols[c]));
            }
            uart_proto_send(buf);
            printf("%s\n", buf);
            gpio_put(board->keypad_rows[row], 1);
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

    if (s_state != PHONE_STATE_DIALING && s_state != PHONE_STATE_CONNECTED) {
        return;
    }

    tone_play_dtmf(key);

    char key_msg[8];
    snprintf(key_msg, sizeof(key_msg), "KEY:%c", key);
    uart_proto_send(key_msg);

    if (s_state == PHONE_STATE_DIALING) {
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
            printf("[KEY] RAW %s\n", key_msg);
            stdio_flush();
        }
        led_update();
        return;
    }

    // Normal FSM operation
    hook_event_t hook_ev = hook_get_event();
    if (hook_ev != HOOK_EVENT_NONE) {
        switch (hook_ev) {
            case HOOK_EVENT_OFF:
                printf("HOOK:OFF\n");
                stdio_flush();
                uart_proto_send("HOOK:OFF");
                break;
            case HOOK_EVENT_ON:
                printf("HOOK:ON\n");
                stdio_flush();
                uart_proto_send("HOOK:ON");
                set_state(PHONE_STATE_IDLE);
                break;
            case HOOK_EVENT_FLASH:
                printf("HOOK:FLASH\n");
                stdio_flush();
                uart_proto_send("HOOK:FLASH");
                break;
            default:
                break;
        }
    }

    // Skip raw-state-driven transitions while a flash window is open: the
    // hook is transiently on-hook during the press phase but we don't know yet
    // whether this is a real hangup (-> IDLE) or a flash (stays in CONNECTED).
    // The flash window resolution path emits HOOK_EVENT_ON or HOOK_EVENT_FLASH,
    // which the event switch above handles.
    if (!hook_is_flash_pending()) {
        if (!hook_is_off_hook() && s_state != PHONE_STATE_IDLE &&
            s_state != PHONE_STATE_RINGING) {
            set_state(PHONE_STATE_IDLE);
        }

        if (hook_is_off_hook() && s_state == PHONE_STATE_IDLE) {
            set_state(PHONE_STATE_DIAL_TONE);
        }
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
