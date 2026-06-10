#ifndef DIGITS_TEST_FAKE_ENV_H
#define DIGITS_TEST_FAKE_ENV_H

// Host-side controllable substitutes for the Pico SDK time and GPIO surfaces
// that the firmware under test depends on. The production sources include the
// fake SDK headers under firmware/test/fakes/ instead of the real ones (the
// host build puts that directory first on the include path), and those headers
// forward to the engine declared here.
//
// Tests drive the engine directly: advance a virtual monotonic clock and set
// the raw level of any GPIO, then call the firmware poll/update functions and
// assert on the resulting state. Nothing here touches real hardware or real
// wall-clock time, so the [100,600]ms flash window, 50ms debounce, and 80ms
// keypad debounce are all exercised deterministically.

#include <stdbool.h>
#include <stdint.h>

#define FAKE_GPIO_COUNT 30

// Reset the entire fake environment to a known state: clock at 0us, every GPIO
// level low, every GPIO pull cleared. Call at the start of each test.
void fake_env_reset(void);

// Virtual monotonic clock.
void fake_clock_advance_us(int64_t us);
void fake_clock_advance_ms(int64_t ms);
uint64_t fake_clock_now_us(void);

// Raw GPIO level control. fake_gpio_set_level mirrors what the physical pin
// would read; fake_gpio_get_level reports the last value the firmware drove on
// an output pin (used to inspect ringer H-bridge phase).
void fake_gpio_set_level(unsigned pin, bool high);
bool fake_gpio_get_level(unsigned pin);

#endif  // DIGITS_TEST_FAKE_ENV_H
