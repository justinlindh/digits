// Behavioral tests for phone_fsm.c: the state transition table and the
// buf_appendf saturating accumulator.
//
// phone_fsm.c is included directly (not linked separately) so the tests can
// reach the static buf_appendf accumulator and the static s_state, while still
// exercising the public phone_fsm_update() against fake hook/keypad/uart/led
// modules. The fakes let a test drive a hook event or a Pi command and assert
// the resulting state and LED mode.

#include "test_harness.h"

#include "fake_env.h"
#include "fakes/fake_modules.h"

// Provide the version macros the production source references; the real build
// gets these from CMake compile definitions.
#ifndef FIRMWARE_VERSION
#define FIRMWARE_VERSION "test"
#endif
#ifndef FIRMWARE_COMMIT
#define FIRMWARE_COMMIT "test"
#endif

#include "phone_fsm.c"

// --- buf_appendf accumulator -------------------------------------------------

static void test_buf_appendf_basic_concat(void) {
    char buf[32];
    size_t pos = 0;
    buf_appendf(buf, sizeof(buf), &pos, "AB");
    buf_appendf(buf, sizeof(buf), &pos, "%d", 12);
    buf_appendf(buf, sizeof(buf), &pos, "C");
    CHECK_STREQ(buf, "AB12C");
    CHECK_EQ((int)pos, 5);
}

static void test_buf_appendf_saturates_without_underflow(void) {
    char buf[8];
    size_t pos = 0;
    // First write nearly fills the 8-byte buffer (7 chars + NUL).
    buf_appendf(buf, sizeof(buf), &pos, "1234567");
    CHECK_EQ((int)pos, 7);
    CHECK_STREQ(buf, "1234567");

    // A second write would overflow: pos must pin at buf_size-1 and the buffer
    // content must stay valid (no past-the-end write, no size_t underflow).
    buf_appendf(buf, sizeof(buf), &pos, "89ABCDEF");
    CHECK_EQ((int)pos, 7);
    CHECK_STREQ(buf, "1234567");

    // Once full, further appends are no-ops and never underflow buf_size - pos.
    buf_appendf(buf, sizeof(buf), &pos, "more");
    CHECK_EQ((int)pos, 7);
    CHECK_STREQ(buf, "1234567");
}

static void test_buf_appendf_exact_truncation_boundary(void) {
    char buf[6];
    size_t pos = 0;
    // "ABCDE" is exactly 5 chars: fits with the trailing NUL in a 6-byte buf.
    buf_appendf(buf, sizeof(buf), &pos, "ABCDE");
    CHECK_EQ((int)pos, 5);
    CHECK_STREQ(buf, "ABCDE");

    // "XYZ" would overflow: pin at 5, content unchanged.
    buf_appendf(buf, sizeof(buf), &pos, "XYZ");
    CHECK_EQ((int)pos, 5);
    CHECK_STREQ(buf, "ABCDE");
}

static void test_buf_appendf_zero_size_is_noop(void) {
    char buf[1] = {'!'};
    size_t pos = 0;
    buf_appendf(buf, 0, &pos, "anything");
    CHECK_EQ((int)pos, 0);
    CHECK_EQ(buf[0], '!');
}

// --- FSM transition table ----------------------------------------------------

#define HOOK_PIN 20

// Reset the whole world to a clean IDLE FSM with the handset on-hook.
static void fsm_reset_idle(void) {
    fake_env_reset();
    fake_uart_rx_reset();
    fake_uart_tx_reset();
    fake_board_use_v2();
    fake_phase_set(PHASE_PAIRED);

    // Keypad columns idle HIGH (internal pull-ups on real hardware). The fake
    // GPIO array resets to all-low, which would read as every key pressed, so
    // release them before init.
    fake_gpio_set_level(6, true);
    fake_gpio_set_level(7, true);
    fake_gpio_set_level(8, true);

    fake_gpio_set_level(HOOK_PIN, false);  // on-hook
    hook_init();
    // hook_init does not reset the inverted/flash/force gates; clear them so
    // FSM tests are isolated from the hook suite's mode changes.
    hook_set_inverted(false);
    hook_set_flash_enabled(false);
    hook_clear_force();
    hook_get_event();
    led_init();
    s_state = PHONE_STATE_IDLE;
    s_keytest_mode = false;
    clear_dialing_buffer();
    set_state(PHONE_STATE_IDLE);
    hook_get_event();
}

// Drive the hook pin and run poll+update for `ms` of virtual time in 10ms
// ticks, mirroring the real main loop (hook_poll then phone_fsm_update).
static void run_for_ms(int ms) {
    for (int t = 0; t < ms; t += 10) {
        fake_clock_advance_ms(10);
        hook_poll();
        phone_fsm_update();
    }
}

static void set_hook(bool off_hook) {
    fake_gpio_set_level(HOOK_PIN, off_hook);
}

// Push a Pi command line into the UART so phone_fsm_update() consumes it.
static void send_cmd(const char *cmd) {
    fake_uart_rx_push(cmd, (unsigned)strlen(cmd));
    fake_uart_rx_push("\n", 1);
}

static void test_fsm_offhook_to_dialtone(void) {
    fsm_reset_idle();
    CHECK_EQ(phone_fsm_get_state(), PHONE_STATE_IDLE);

    set_hook(true);   // lift handset
    run_for_ms(100);  // past 50ms debounce
    CHECK_EQ(phone_fsm_get_state(), PHONE_STATE_DIAL_TONE);
    CHECK_EQ(fake_led_mode(), LED_MODE_ON);
}

static void test_fsm_dialtone_to_dialing_on_key(void) {
    fsm_reset_idle();
    set_hook(true);
    run_for_ms(100);
    CHECK_EQ(phone_fsm_get_state(), PHONE_STATE_DIAL_TONE);

    // Press a digit: dial tone advances to dialing.
    fake_gpio_set_level(7, false);  // col1 => '0' (bottom row)
    run_for_ms(120);                // past 80ms keypad debounce
    CHECK_EQ(phone_fsm_get_state(), PHONE_STATE_DIALING);
}

static void test_fsm_dialing_timeout_to_busy(void) {
    fsm_reset_idle();
    set_hook(true);
    run_for_ms(100);
    CHECK_EQ(phone_fsm_get_state(), PHONE_STATE_DIAL_TONE);

    // Enter DIALING with a partial number (fewer than DIAL_DIGITS_REQUIRED).
    // process_key on the first digit transitions DIAL_TONE -> DIALING.
    process_key('5');
    process_key('5');
    process_key('5');
    CHECK_EQ(phone_fsm_get_state(), PHONE_STATE_DIALING);
    CHECK_EQ(s_digits_len, 3);
    CHECK(!s_dial_sent);

    // Hold off-hook in DIALING for DIAL_TIMEOUT_MS (15s) with no completing
    // digits. The off-hook timeout fires TIMEOUT:DIAL_TONE and lands BUSY.
    run_for_ms(15100);
    CHECK_EQ(phone_fsm_get_state(), PHONE_STATE_BUSY);
    CHECK_EQ(fake_uart_tx_count_lines_with_prefix("TIMEOUT:DIAL_TONE"), 1);
}

static void test_fsm_full_dial_emits_single_dial_line(void) {
    fsm_reset_idle();
    set_hook(true);
    run_for_ms(100);
    CHECK_EQ(phone_fsm_get_state(), PHONE_STATE_DIAL_TONE);

    fake_uart_tx_reset();

    // Drive seven distinct digits. The first call transitions DIAL_TONE ->
    // DIALING; the accumulator fills to DIAL_DIGITS_REQUIRED on the seventh.
    const char *digits = "1234567";
    for (int i = 0; digits[i] != '\0'; ++i) {
        process_key(digits[i]);
    }
    CHECK_EQ(phone_fsm_get_state(), PHONE_STATE_DIALING);
    CHECK_EQ(s_digits_len, DIAL_DIGITS_REQUIRED);
    CHECK(s_dial_sent);

    // Exactly one DIAL:<digits> line is emitted (the s_dial_sent latch prevents
    // a duplicate), and it carries the full accumulated number.
    CHECK_EQ(fake_uart_tx_count_lines_with_prefix("DIAL:1234567"), 1);
    CHECK_EQ(fake_uart_tx_count_lines_with_prefix("DIAL:"), 1);

    // An eighth keypress does not re-fire DIAL (latch holds, buffer is full).
    process_key('8');
    CHECK_EQ(fake_uart_tx_count_lines_with_prefix("DIAL:"), 1);
    CHECK_EQ(s_digits_len, DIAL_DIGITS_REQUIRED);
}

static void test_fsm_hangup_returns_to_idle(void) {
    fsm_reset_idle();
    set_hook(true);
    run_for_ms(100);
    CHECK_EQ(phone_fsm_get_state(), PHONE_STATE_DIAL_TONE);

    set_hook(false);  // cradle
    run_for_ms(100);
    CHECK_EQ(phone_fsm_get_state(), PHONE_STATE_IDLE);
    CHECK_EQ(fake_led_mode(), LED_MODE_OFF);
}

static void test_fsm_ring_then_offhook_connects(void) {
    fsm_reset_idle();

    // Pi commands an incoming ring while on-hook.
    send_cmd("RING:START");
    run_for_ms(20);
    CHECK_EQ(phone_fsm_get_state(), PHONE_STATE_RINGING);
    CHECK_EQ(fake_led_mode(), LED_MODE_BLINK);

    // Answer by lifting the handset: RINGING -> CONNECTED.
    set_hook(true);
    run_for_ms(100);
    CHECK_EQ(phone_fsm_get_state(), PHONE_STATE_CONNECTED);
    CHECK_EQ(fake_led_mode(), LED_MODE_BREATHING);
}

static void test_fsm_ring_stop_from_pi(void) {
    fsm_reset_idle();
    send_cmd("RING:START");
    run_for_ms(20);
    CHECK_EQ(phone_fsm_get_state(), PHONE_STATE_RINGING);

    send_cmd("RING:STOP");
    run_for_ms(20);
    CHECK_EQ(phone_fsm_get_state(), PHONE_STATE_IDLE);
}

static void test_fsm_dialtone_timeout_to_busy(void) {
    fsm_reset_idle();
    set_hook(true);
    run_for_ms(100);
    CHECK_EQ(phone_fsm_get_state(), PHONE_STATE_DIAL_TONE);

    // No keys for DIAL_TONE_TIMEOUT_MS (15s): dial tone falls to BUSY.
    run_for_ms(15100);
    CHECK_EQ(phone_fsm_get_state(), PHONE_STATE_BUSY);
}

static void test_fsm_reset_command_returns_idle(void) {
    fsm_reset_idle();
    set_hook(true);
    run_for_ms(100);
    CHECK_EQ(phone_fsm_get_state(), PHONE_STATE_DIAL_TONE);

    // RESET forces IDLE. Hold the handset off-hook would re-trigger dial tone,
    // so cradle first to observe a stable IDLE.
    set_hook(false);
    send_cmd("RESET");
    run_for_ms(100);
    CHECK_EQ(phone_fsm_get_state(), PHONE_STATE_IDLE);
}

static void test_fsm_idle_led_follows_phase(void) {
    fsm_reset_idle();

    // In IDLE the LED idle pattern is chosen by the persisted phase.
    fake_phase_set(PHASE_UNPAIRED);
    send_cmd("STATE:SET:UNPAIRED");
    run_for_ms(20);
    CHECK_EQ(fake_led_mode(), LED_MODE_SLOW_PULSE);

    fake_phase_set(PHASE_SETUP);
    send_cmd("STATE:SET:SETUP");
    run_for_ms(20);
    CHECK_EQ(fake_led_mode(), LED_MODE_DOUBLE_PULSE);
}

static void test_fsm_state_name_mapping(void) {
    CHECK_STREQ(phone_fsm_state_name(PHONE_STATE_IDLE), "IDLE");
    CHECK_STREQ(phone_fsm_state_name(PHONE_STATE_DIAL_TONE), "DIAL_TONE");
    CHECK_STREQ(phone_fsm_state_name(PHONE_STATE_DIALING), "DIALING");
    CHECK_STREQ(phone_fsm_state_name(PHONE_STATE_RINGING), "RINGING");
    CHECK_STREQ(phone_fsm_state_name(PHONE_STATE_CONNECTED), "CONNECTED");
    CHECK_STREQ(phone_fsm_state_name(PHONE_STATE_BUSY), "BUSY");
    CHECK_STREQ(phone_fsm_state_name((phone_state_t)99), "UNKNOWN");
}

static const test_case_t k_fsm_tests[] = {
    TEST_CASE(test_buf_appendf_basic_concat),
    TEST_CASE(test_buf_appendf_saturates_without_underflow),
    TEST_CASE(test_buf_appendf_exact_truncation_boundary),
    TEST_CASE(test_buf_appendf_zero_size_is_noop),
    TEST_CASE(test_fsm_offhook_to_dialtone),
    TEST_CASE(test_fsm_dialtone_to_dialing_on_key),
    TEST_CASE(test_fsm_dialing_timeout_to_busy),
    TEST_CASE(test_fsm_full_dial_emits_single_dial_line),
    TEST_CASE(test_fsm_hangup_returns_to_idle),
    TEST_CASE(test_fsm_ring_then_offhook_connects),
    TEST_CASE(test_fsm_ring_stop_from_pi),
    TEST_CASE(test_fsm_dialtone_timeout_to_busy),
    TEST_CASE(test_fsm_reset_command_returns_idle),
    TEST_CASE(test_fsm_idle_led_follows_phase),
    TEST_CASE(test_fsm_state_name_mapping),
};

DEFINE_SUITE(fsm)
