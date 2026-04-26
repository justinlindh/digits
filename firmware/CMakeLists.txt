cmake_minimum_required(VERSION 3.13)

# Hardware revision. Read BEFORE pico_sdk_import so we can pick the right
# boot stage 2 for the target hardware.
# 1 = ElectroCookie prototype with Pico H module (L298N ringer on GP11)
# 2 = v2 carrier PCB with bare RP2040 + W25Q16JV (DRV8871 ringer on GP19)
set(HARDWARE_REV 1 CACHE STRING "Target hardware revision")
if(NOT HARDWARE_REV MATCHES "^[12]$")
    message(FATAL_ERROR "HARDWARE_REV must be 1 or 2 (got '${HARDWARE_REV}')")
endif()
message(STATUS "Building for HARDWARE_REV=${HARDWARE_REV}")

# V2 carrier uses Winbond W25Q16JVSSIQ. The SDK default boot2_w25q080
# initializes the flash for QSPI quad-mode XIP using continuation commands
# the JV part rejects, after which the chip cannot run XIP, watchdog resets
# in a tight loop, and SWD goes silent because the cores are held in reset
# every iteration. boot2_generic_03h drops to slow single-bit 0x03 reads
# (~5 MB/s vs ~24 MB/s) but works on every compliant SPI flash. V1's Pico H
# module ships a Pi-matched flash so it keeps the SDK default.
if(HARDWARE_REV STREQUAL "2")
    set(PICO_DEFAULT_BOOT_STAGE2 boot2_generic_03h)
endif()

include(pico_sdk_import.cmake)

project(digits C CXX ASM)

set(CMAKE_C_STANDARD 11)
set(CMAKE_CXX_STANDARD 17)

pico_sdk_init()

add_executable(digits
    src/main.c
    src/hook.c
    src/keypad.c
    src/led.c
    src/tone.c
    src/ringer.c
    src/uart_proto.c
    src/phone_fsm.c
)

target_link_libraries(digits
    pico_stdlib
    hardware_gpio
    hardware_pwm
    hardware_uart
    hardware_timer
    hardware_watchdog
)

# Version info — CI passes DIGITS_VERSION; fallback to git describe with fw/ tags
if(NOT DIGITS_VERSION)
    execute_process(
        COMMAND git describe --tags --always --dirty --match "fw/v*"
        WORKING_DIRECTORY ${CMAKE_SOURCE_DIR}
        OUTPUT_VARIABLE DIGITS_VERSION
        OUTPUT_STRIP_TRAILING_WHITESPACE
        ERROR_QUIET
    )
    if(DIGITS_VERSION)
        # Strip fw/v prefix for display (fw/v1.0.0 -> 1.0.0)
        string(REGEX REPLACE "^fw/v" "" DIGITS_VERSION "${DIGITS_VERSION}")
    else()
        set(DIGITS_VERSION "unknown")
    endif()
endif()

if(NOT DIGITS_COMMIT)
    execute_process(
        COMMAND git rev-parse --short HEAD
        WORKING_DIRECTORY ${CMAKE_SOURCE_DIR}
        OUTPUT_VARIABLE DIGITS_COMMIT
        OUTPUT_STRIP_TRAILING_WHITESPACE
        ERROR_QUIET
    )
    if(NOT DIGITS_COMMIT)
        set(DIGITS_COMMIT "unknown")
    endif()
endif()

target_compile_definitions(digits PRIVATE
    FIRMWARE_VERSION="${DIGITS_VERSION}"
    FIRMWARE_COMMIT="${DIGITS_COMMIT}"
    HARDWARE_REV=${HARDWARE_REV}
)

# USB serial for debug console; UART0 reserved for Pi comms
pico_enable_stdio_usb(digits 1)
pico_enable_stdio_uart(digits 0)

pico_add_extra_outputs(digits)
