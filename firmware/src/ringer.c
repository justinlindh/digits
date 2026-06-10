#include "ringer.h"

#include "board.h"
#include "hardware/gpio.h"
#include "pico/time.h"

// 20Hz AC: full period = 50ms, half-period = 25ms = 25000µs
#define RINGER_TOGGLE_INTERVAL_US 25000

static bool s_active = false;
static bool s_phase = false;  // alternates between two H-bridge states
static absolute_time_t s_last_toggle;

// Pattern tables: alternating on/off durations in ms, terminated by 0.
// Even indices (0, 2, ...) are on-segments; odd indices are off-segments.
static const uint16_t pattern_standard[]    = {2000, 4000, 0};
static const uint16_t pattern_distinctive[] = {400, 200, 400, 200, 800, 4000, 0};

#define PATTERN_COUNT 2
static const uint16_t *patterns[PATTERN_COUNT] = {
    pattern_standard,
    pattern_distinctive,
};

static const uint16_t *s_pattern = pattern_standard;
static uint8_t s_seg_idx = 0;
static uint32_t s_seg_start_ms = 0;

static uint32_t now_ms(void) {
    return to_ms_since_boot(get_absolute_time());
}

// Set H-bridge outputs. When phase=true: IN1=HIGH, IN2=LOW (current flows one way).
// When phase=false: IN1=LOW, IN2=HIGH (current flows the other way).
// Both LOW = no current (coast/stop).
static void set_hbridge(bool phase) {
    gpio_put(board->ringer_in1_pin, phase ? 1 : 0);
    gpio_put(board->ringer_in2_pin, phase ? 0 : 1);
}

static void stop_hbridge(void) {
    gpio_put(board->ringer_in1_pin, 0);
    gpio_put(board->ringer_in2_pin, 0);
}

void ringer_init(void) {
    gpio_init(board->ringer_in1_pin);
    gpio_set_dir(board->ringer_in1_pin, GPIO_OUT);
    gpio_put(board->ringer_in1_pin, 0);

    gpio_init(board->ringer_in2_pin);
    gpio_set_dir(board->ringer_in2_pin, GPIO_OUT);
    gpio_put(board->ringer_in2_pin, 0);

    s_active = false;
    s_phase = false;
    s_last_toggle = get_absolute_time();
}

void ringer_start(void) {
    s_active = true;
    s_phase = false;
    stop_hbridge();
    s_pattern = pattern_standard;
    s_seg_idx = 0;
    s_seg_start_ms = now_ms();
    s_last_toggle = get_absolute_time();
}

void ringer_start_pattern(uint8_t pattern_id) {
    if (pattern_id >= PATTERN_COUNT) {
        pattern_id = 0;
    }
    s_active = true;
    s_phase = false;
    stop_hbridge();
    s_pattern = patterns[pattern_id];
    s_seg_idx = 0;
    s_seg_start_ms = now_ms();
    s_last_toggle = get_absolute_time();
}

void ringer_stop(void) {
    s_active = false;
    s_phase = false;
    stop_hbridge();
}

void ringer_update(void) {
    if (!s_active) {
        return;
    }

    uint16_t seg_dur = s_pattern[s_seg_idx];
    if (seg_dur == 0) {
        s_seg_idx = 0;
        s_seg_start_ms = now_ms();
        seg_dur = s_pattern[0];
    }

    uint32_t seg_elapsed = now_ms() - s_seg_start_ms;
    if (seg_elapsed >= seg_dur) {
        s_seg_idx++;
        s_seg_start_ms = now_ms();
        s_phase = false;
        stop_hbridge();
        return;
    }

    bool on_segment = (s_seg_idx % 2) == 0;

    if (!on_segment) {
        // Silent window: always stop H-bridge regardless of s_phase to avoid
        // DC bias that could saturate the bell coil or trigger H-bridge thermal shutdown.
        stop_hbridge();
        s_phase = false;
        return;
    }

    // Active ringing window: alternate H-bridge polarity at 20Hz
    absolute_time_t now_t = get_absolute_time();
    if (absolute_time_diff_us(s_last_toggle, now_t) >= RINGER_TOGGLE_INTERVAL_US) {
        s_phase = !s_phase;
        set_hbridge(s_phase);
        s_last_toggle = now_t;
    }
}
