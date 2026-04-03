#include "ringer.h"

#include "hardware/gpio.h"
#include "pico/time.h"

// US ring cadence: 2s on, 4s off (6s cycle)
#define RINGER_CADENCE_ON_MS 2000
#define RINGER_CADENCE_CYCLE_MS 6000

// 20Hz AC: full period = 50ms, half-period = 25ms = 25000µs
#define RINGER_TOGGLE_INTERVAL_US 25000

static bool s_active = false;
static bool s_phase = false;  // alternates between two H-bridge states
static uint32_t s_start_ms = 0;
static absolute_time_t s_last_toggle;

static uint32_t now_ms(void) {
    return to_ms_since_boot(get_absolute_time());
}

// Set H-bridge outputs. When phase=true: IN1=HIGH, IN2=LOW (current flows one way).
// When phase=false: IN1=LOW, IN2=HIGH (current flows the other way).
// Both LOW = no current (coast/stop).
static void set_hbridge(bool phase) {
    gpio_put(RINGER_PIN_IN1, phase ? 1 : 0);
    gpio_put(RINGER_PIN_IN2, phase ? 0 : 1);
}

static void stop_hbridge(void) {
    gpio_put(RINGER_PIN_IN1, 0);
    gpio_put(RINGER_PIN_IN2, 0);
}

void ringer_init(void) {
    gpio_init(RINGER_PIN_IN1);
    gpio_set_dir(RINGER_PIN_IN1, GPIO_OUT);
    gpio_put(RINGER_PIN_IN1, 0);

    gpio_init(RINGER_PIN_IN2);
    gpio_set_dir(RINGER_PIN_IN2, GPIO_OUT);
    gpio_put(RINGER_PIN_IN2, 0);

    s_active = false;
    s_phase = false;
    s_start_ms = now_ms();
    s_last_toggle = get_absolute_time();
}

void ringer_start(void) {
    s_active = true;
    s_phase = false;
    stop_hbridge();
    s_start_ms = now_ms();
    s_last_toggle = get_absolute_time();
}

void ringer_stop(void) {
    s_active = false;
    s_phase = false;
    stop_hbridge();
}

bool ringer_is_active(void) {
    return s_active;
}

void ringer_update(void) {
    if (!s_active) {
        return;
    }

    uint32_t elapsed_ms = now_ms() - s_start_ms;
    bool on_window = (elapsed_ms % RINGER_CADENCE_CYCLE_MS) < RINGER_CADENCE_ON_MS;

    if (!on_window) {
        // Silent window — always stop H-bridge regardless of s_phase.
        // Previous code only stopped when s_phase was true, leaving the
        // bridge driven in one direction if the last 20Hz toggle ended
        // on phase=false.  That DC bias could saturate the bell coil
        // or trigger L298N thermal shutdown, killing subsequent rings.
        stop_hbridge();
        s_phase = false;
        return;
    }

    // Active ringing window — alternate H-bridge polarity at 20Hz
    absolute_time_t now_t = get_absolute_time();
    if (absolute_time_diff_us(s_last_toggle, now_t) >= RINGER_TOGGLE_INTERVAL_US) {
        s_phase = !s_phase;
        set_hbridge(s_phase);
        s_last_toggle = now_t;
    }
}
