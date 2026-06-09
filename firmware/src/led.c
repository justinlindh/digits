#include "led.h"

#include "board.h"
#include "hardware/gpio.h"
#include "hardware/pwm.h"
#include "pico/time.h"

#define PWM_WRAP 999

#define BLINK_INTERVAL_US       500000
#define FAST_BLINK_INTERVAL_US  150000

// Double pulse: ON 100ms, OFF 100ms, ON 100ms, OFF 700ms
#define DP_ON_US    100000
#define DP_GAP_US   100000
#define DP_PAUSE_US 700000

// Connecting: ON 100ms, OFF 100ms, ON 100ms, OFF 400ms
#define CN_ON_US    100000
#define CN_GAP_US   100000
#define CN_PAUSE_US 400000

// Slow pulse: brief flash every 1.7s
#define SP_ON_US    200000
#define SP_OFF_US   1500000

// Slower pulse: short wink every ~3.15s. Used as the voicemail
// message-waiting indicator so it's visually distinct from SLOW_PULSE
// (boot-unpaired). Shorter ON + longer OFF keeps it from being mistaken
// for either BREATHING or the unpaired pulse.
#define SLOWER_ON_US    150000
#define SLOWER_OFF_US   3000000

// Breathing: 300 steps over 3s (10ms per step). LUT stores the rising half
// (150 entries, 0 to peak). Indices 150-299 mirror the LUT for the falling half.
// Values: round(999 * pow(sin(pi/2 * i/149), 2.2)) for i in 0..149.
static const uint16_t s_breathe_lut[150] = {
    0, 0, 0, 1, 1, 2, 2, 3, 4, 6,
    7, 9, 11, 13, 15, 17, 20, 22, 25, 29,
    32, 36, 39, 43, 47, 52, 56, 61, 66, 71,
    77, 82, 88, 94, 100, 106, 112, 119, 126, 133,
    140, 147, 155, 162, 170, 178, 186, 195, 203, 212,
    220, 229, 238, 247, 257, 266, 275, 285, 295, 304,
    314, 324, 334, 345, 355, 365, 376, 386, 397, 407,
    418, 428, 439, 450, 461, 471, 482, 493, 504, 515,
    526, 537, 547, 558, 569, 580, 591, 601, 612, 622,
    633, 644, 654, 664, 675, 685, 695, 705, 715, 725,
    734, 744, 754, 763, 772, 781, 790, 799, 808, 817,
    825, 833, 841, 849, 857, 865, 872, 879, 886, 893,
    900, 906, 913, 919, 925, 930, 936, 941, 946, 951,
    956, 960, 964, 968, 972, 975, 979, 982, 984, 987,
    989, 991, 993, 995, 996, 997, 998, 999, 999, 999,
};

static led_mode_t s_mode = LED_MODE_OFF;
static bool s_led_on = false;
static bool s_locked = false;
static absolute_time_t s_last_toggle;
static uint8_t s_step;
static uint s_pwm_slice;
static uint s_pwm_chan;
static uint16_t s_breathe_phase;

void led_init(void) {
    gpio_set_function(board->led_pin, GPIO_FUNC_PWM);
    s_pwm_slice = pwm_gpio_to_slice_num(board->led_pin);
    s_pwm_chan = pwm_gpio_to_channel(board->led_pin);
    pwm_set_clkdiv(s_pwm_slice, 125.0f);
    pwm_set_wrap(s_pwm_slice, PWM_WRAP);
    pwm_set_chan_level(s_pwm_slice, s_pwm_chan, 0);
    pwm_set_enabled(s_pwm_slice, true);

    s_mode = LED_MODE_OFF;
    s_led_on = false;
    s_locked = false;
    s_step = 0;
    s_last_toggle = get_absolute_time();
}

void led_set_locked(bool locked) {
    s_locked = locked;
}

bool led_is_locked(void) {
    return s_locked;
}

void led_set_mode(led_mode_t mode) {
    s_mode = mode;
    s_step = 0;
    s_breathe_phase = 0;

    if (mode == LED_MODE_OFF) {
        s_led_on = false;
        pwm_set_gpio_level(board->led_pin, 0);
    } else if (mode == LED_MODE_ON) {
        s_led_on = true;
        pwm_set_gpio_level(board->led_pin, PWM_WRAP);
    } else if (mode == LED_MODE_BREATHING) {
        // Breathing is driven from a LUT in led_update starting at phase 0 (the
        // ramp's dim end). Seed the output to that same starting level instead
        // of full brightness so there is no one-tick full-bright flash on mode
        // entry, including at boot when PHASE_PAIRED selects breathing.
        s_led_on = true;
        pwm_set_gpio_level(board->led_pin, s_breathe_lut[0]);
        s_last_toggle = get_absolute_time();
    } else {
        s_led_on = true;
        pwm_set_gpio_level(board->led_pin, PWM_WRAP);
        s_last_toggle = get_absolute_time();
    }
}

static void set_led(bool on) {
    s_led_on = on;
    pwm_set_gpio_level(board->led_pin, on ? PWM_WRAP : 0);
}

// Pattern step machine for multi-phase patterns.
// Double pulse: step 0=ON, 1=OFF(gap), 2=ON, 3=OFF(pause)
// Connecting:   step 0=ON, 1=OFF(gap), 2=ON, 3=OFF(pause)
static void update_pattern(uint32_t on_us, uint32_t gap_us, uint32_t pause_us) {
    absolute_time_t now = get_absolute_time();
    uint32_t elapsed = (uint32_t)absolute_time_diff_us(s_last_toggle, now);

    switch (s_step) {
    case 0:
        if (elapsed >= on_us) {
            set_led(false);
            s_step = 1;
            s_last_toggle = now;
        }
        break;
    case 1:
        if (elapsed >= gap_us) {
            set_led(true);
            s_step = 2;
            s_last_toggle = now;
        }
        break;
    case 2:
        if (elapsed >= on_us) {
            set_led(false);
            s_step = 3;
            s_last_toggle = now;
        }
        break;
    case 3:
        if (elapsed >= pause_us) {
            set_led(true);
            s_step = 0;
            s_last_toggle = now;
        }
        break;
    }
}

void led_update(void) {
    if (s_mode == LED_MODE_OFF || s_mode == LED_MODE_ON) {
        return;
    }

    absolute_time_t now = get_absolute_time();

    switch (s_mode) {
    case LED_MODE_BLINK:
        if (absolute_time_diff_us(s_last_toggle, now) >= BLINK_INTERVAL_US) {
            set_led(!s_led_on);
            s_last_toggle = now;
        }
        break;
    case LED_MODE_FAST_BLINK:
        if (absolute_time_diff_us(s_last_toggle, now) >= FAST_BLINK_INTERVAL_US) {
            set_led(!s_led_on);
            s_last_toggle = now;
        }
        break;
    case LED_MODE_DOUBLE_PULSE:
        update_pattern(DP_ON_US, DP_GAP_US, DP_PAUSE_US);
        break;
    case LED_MODE_CONNECTING:
        update_pattern(CN_ON_US, CN_GAP_US, CN_PAUSE_US);
        break;
    case LED_MODE_BREATHING: {
        uint16_t idx = (s_breathe_phase < 150)
            ? s_breathe_phase
            : (299 - s_breathe_phase);
        pwm_set_gpio_level(board->led_pin, s_breathe_lut[idx]);
        s_breathe_phase++;
        if (s_breathe_phase >= 300) {
            s_breathe_phase = 0;
        }
        break;
    }
    case LED_MODE_SLOW_PULSE:
        if (absolute_time_diff_us(s_last_toggle, now) >=
            (s_led_on ? SP_ON_US : SP_OFF_US)) {
            set_led(!s_led_on);
            s_last_toggle = now;
        }
        break;
    case LED_MODE_SLOWER_PULSE:
        if (absolute_time_diff_us(s_last_toggle, now) >=
            (s_led_on ? SLOWER_ON_US : SLOWER_OFF_US)) {
            set_led(!s_led_on);
            s_last_toggle = now;
        }
        break;
    default:
        break;
    }
}
