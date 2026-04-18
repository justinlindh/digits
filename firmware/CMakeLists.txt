cmake_minimum_required(VERSION 3.13)

include(pico_sdk_import.cmake)

project(digits C CXX ASM)

set(CMAKE_C_STANDARD 11)
set(CMAKE_CXX_STANDARD 17)

pico_sdk_init()

# Hardware revision. Controls GPIO assignments that differ between the v1
# ElectroCookie protoboard (L298N ringer on GP11) and the v2 carrier PCB
# (DRV8871 ringer on GP19). Defaults to 1 so prototype devices continue
# to work; pass -DHARDWARE_REV=2 for v2 carrier boards.
set(HARDWARE_REV 1 CACHE STRING "Target hardware revision (1 = ElectroCookie prototype, 2 = v2 carrier PCB)")
if(NOT HARDWARE_REV MATCHES "^[12]$")
    message(FATAL_ERROR "HARDWARE_REV must be 1 or 2 (got '${HARDWARE_REV}')")
endif()
message(STATUS "Building for HARDWARE_REV=${HARDWARE_REV}")

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
