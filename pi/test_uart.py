#!/usr/bin/env python3
"""UART communication test script for Pi Zero 2 W <-> Pico."""

from __future__ import annotations

import argparse
import sys
import time
from typing import Optional

import serial


class PicoUART:
    """Thin wrapper around serial.Serial for line-oriented Pico protocol."""

    def __init__(self, port: str = "/dev/serial0", baudrate: int = 115200):
        self.ser = serial.Serial(port=port, baudrate=baudrate, timeout=0.1)
        # Give the line discipline and peer a moment to settle.
        time.sleep(0.2)
        self.ser.reset_input_buffer()

    def send(self, msg: str) -> None:
        line = msg.rstrip("\n") + "\n"
        self.ser.write(line.encode("utf-8"))
        self.ser.flush()

    def recv(self, timeout: float = 1.0) -> Optional[str]:
        deadline = time.time() + timeout
        while time.time() < deadline:
            raw = self.ser.readline()
            if raw:
                return raw.decode("utf-8", errors="replace").strip()
        return None

    def expect(self, expected: str, timeout: float = 1.0) -> bool:
        deadline = time.time() + timeout
        while time.time() < deadline:
            line = self.recv(timeout=max(0.05, deadline - time.time()))
            if line is None:
                continue
            print(f"  <- {line}")
            if line == expected:
                return True
        return False

    def close(self) -> None:
        if self.ser.is_open:
            self.ser.close()


def step(name: str) -> None:
    print(f"\n=== {name} ===")


def run_automated_tests(uart: PicoUART) -> int:
    failures = 0

    step("1) PING/PONG health check")
    print("  -> PING")
    uart.send("PING")
    if uart.expect("PONG", timeout=2.0):
        print("  PASS: received PONG")
    else:
        print("  FAIL: expected PONG")
        failures += 1

    step("2) Ringer control test")
    print("  -> RING:START")
    uart.send("RING:START")
    if uart.expect("RING:ACK", timeout=2.0):
        print("  PASS: received RING:ACK")
    else:
        print("  FAIL: expected RING:ACK")
        failures += 1

    print("  Waiting 3 seconds while ringer runs...")
    time.sleep(3)

    print("  -> RING:STOP")
    uart.send("RING:STOP")
    if uart.expect("RING:DONE", timeout=2.0):
        print("  PASS: received RING:DONE")
    else:
        print("  FAIL: expected RING:DONE")
        failures += 1

    step("3) LED sequence (visual verification)")
    for cmd in ["LED:ON", "LED:BLINK", "LED:OFF"]:
        print(f"  -> {cmd}")
        uart.send(cmd)
        time.sleep(1)
    print("  VERIFY: Confirm LED turned on, blinked, then turned off.")

    step("4) Tone sequence (auditory verification)")
    for cmd in ["TONE:DIAL", "TONE:RINGBACK", "TONE:STOP"]:
        print(f"  -> {cmd}")
        uart.send(cmd)
        time.sleep(1.5)
    print("  VERIFY: Confirm dial tone then ringback tone were audible, then stopped.")

    step("Automated portion complete")
    if failures:
        print(f"Result: {failures} failing expectation(s).")
    else:
        print("Result: all command-response expectations passed.")
    return failures


def monitor_events(uart: PicoUART) -> None:
    step("5) Interactive monitor mode")
    print("Monitoring events from Pico. Press Ctrl+C to exit.")
    while True:
        line = uart.recv(timeout=0.5)
        if line is None:
            continue

        emoji = "🔢"
        if line.startswith("HOOK:OFF"):
            emoji = "📞"
        elif line.startswith("HOOK:ON"):
            emoji = "📴"
        elif line.startswith("KEY:"):
            emoji = "🔢"

        print(f"{emoji} {line}")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="UART communication test script for Pi <-> Pico"
    )
    parser.add_argument(
        "--port",
        default="/dev/serial0",
        help="Serial device path (default: /dev/serial0)",
    )
    parser.add_argument(
        "--monitor",
        action="store_true",
        help="After tests, keep running and print hook/keypad events",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    uart = PicoUART(port=args.port, baudrate=115200)
    try:
        if args.monitor:
            monitor_events(uart)
            return 0
        failures = run_automated_tests(uart)
        return 1 if failures else 0
    except KeyboardInterrupt:
        print("\nInterrupted by user.")
        return 130
    finally:
        uart.close()


if __name__ == "__main__":
    sys.exit(main())
