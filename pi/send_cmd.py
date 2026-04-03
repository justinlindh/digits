#!/usr/bin/env python3
"""Send a single command to the Pico via UART and print the response.

Usage: python3 send_cmd.py RING:START
       python3 send_cmd.py PING
       python3 send_cmd.py STATE?

Stops dtmf-uart.service, sends the command, reads response(s) for up to
2 seconds, then restarts the service. Keeps service ownership intact.
"""

import subprocess
import sys
import time

import serial

PORT = '/dev/serial0'
BAUD = 115200
RESPONSE_WAIT = 2.0  # seconds to collect responses


def main():
    if len(sys.argv) < 2:
        print(f"Usage: {sys.argv[0]} <COMMAND> [timeout_secs]")
        print("Examples: RING:START  RING:STOP  PING  STATE?  RESET  KEYDUMP")
        sys.exit(1)

    cmd = sys.argv[1]
    wait = float(sys.argv[2]) if len(sys.argv) > 2 else RESPONSE_WAIT

    # Stop service
    subprocess.run(['sudo', 'systemctl', 'stop', 'dtmf-uart.service'],
                   capture_output=True, timeout=5)
    time.sleep(0.3)

    try:
        s = serial.Serial(PORT, BAUD, timeout=0.5)
        s.reset_input_buffer()
        s.write(f'{cmd}\r\n'.encode())
        print(f'>>> {cmd}')

        deadline = time.monotonic() + wait
        while time.monotonic() < deadline:
            raw = s.readline()
            if raw:
                line = raw.decode('utf-8', errors='replace').strip()
                if line:
                    print(f'  {line}')
        s.close()
    finally:
        # Always restart service
        subprocess.run(['sudo', 'systemctl', 'start', 'dtmf-uart.service'],
                       capture_output=True, timeout=5)
        print('\n[service restarted]')


if __name__ == '__main__':
    main()
