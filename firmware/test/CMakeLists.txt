cmake_minimum_required(VERSION 3.13)

# Host-side unit tests for the pure-logic firmware modules. This is a standalone
# native build: it does NOT use the Pico SDK. Production sources are compiled
# against the fake SDK headers under fakes/ (which forward to a controllable
# clock and GPIO engine), so the same hook.c / keypad.c / uart_proto.c /
# phone_fsm.c that ship on the Pico run on the host under test.

project(digits_fw_tests C)

set(CMAKE_C_STANDARD 11)
set(CMAKE_C_STANDARD_REQUIRED ON)

set(SRC_DIR ${CMAKE_CURRENT_SOURCE_DIR}/../src)

add_executable(fw_tests
    # Test runner and per-module suites.
    test_main.c
    test_hook.c
    test_keypad.c
    test_uart_proto.c
    test_fsm.c
    test_phase_sync.c

    # Fake SDK engine + fake modules.
    fakes/fake_env.c
    fakes/fake_uart.c
    fakes/fake_board.c
    fakes/fake_led.c
    fakes/fake_phase.c

    # Production sources under test (compiled against the fakes). phone_fsm.c is
    # NOT listed here: test_fsm.c includes it directly so the static buf_appendf
    # accumulator is reachable, which would collide with a separate compile.
    ${SRC_DIR}/hook.c
    ${SRC_DIR}/keypad.c
    ${SRC_DIR}/uart_proto.c
    ${SRC_DIR}/ringer.c
    # Pure phase-to-LED mapping shared by main.c and phone_fsm.c. No hardware
    # dependency, so the real source links directly and the FSM tests exercise
    # the shipping mapping rather than a fake.
    ${SRC_DIR}/led_phase.c
)

target_include_directories(fw_tests PRIVATE
    # Fakes first so "pico/..." and "hardware/..." resolve to the host shims.
    ${CMAKE_CURRENT_SOURCE_DIR}/fakes
    ${CMAKE_CURRENT_SOURCE_DIR}
    ${SRC_DIR}
)

# -Wno-format: hook.c prints int64_t with %lld, which is correct on the 32-bit
# Pico target (int64_t == long long) but trips -Wformat on an LP64 host where
# int64_t == long. The format strings are right for the firmware; suppress the
# host-only mismatch rather than alter production code for the test build.
target_compile_options(fw_tests PRIVATE -Wall -Wextra -Wno-format)

# Absolute repo root, baked in at configure time. test_phase_sync.c uses it to
# open firmware/src/phase.h and pi/digitsd/internal/phone/serial.go regardless
# of the working directory the test binary is run from.
target_compile_definitions(fw_tests PRIVATE
    REPO_ROOT="${CMAKE_CURRENT_SOURCE_DIR}/../.."
)

enable_testing()
add_test(NAME fw_tests COMMAND fw_tests)
