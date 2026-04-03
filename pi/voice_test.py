#!/usr/bin/env python3
"""Real-time mic → speaker loopback with 60Hz notch filter.

Records from the Codec Zero mic input and plays back through the
speaker output with a multi-notch filter removing 60Hz mains hum
and its harmonics.

Usage:
    python3 voice_test.py          # loopback with notch filter
    python3 voice_test.py --setup  # also configures mixer settings
    python3 voice_test.py --raw    # loopback WITHOUT filter (for comparison)

Press Ctrl+C to stop.
"""

import math
import os
import struct
import subprocess
import sys
import threading


SAMPLE_RATE = 48000
CHANNELS = 1
FORMAT = "S16_LE"
CAPTURE_DEVICE = "plughw:Zero"
PLAYBACK_DEVICE = "default"
PERIOD_FRAMES = 512
BYTES_PER_FRAME = 2  # S16_LE mono


class BiquadNotch:
    """Second-order IIR notch filter (biquad)."""

    def __init__(self, freq: float, q: float, fs: float):
        w0 = 2.0 * math.pi * freq / fs
        alpha = math.sin(w0) / (2.0 * q)
        b0 = 1.0
        b1 = -2.0 * math.cos(w0)
        b2 = 1.0
        a0 = 1.0 + alpha
        a1 = -2.0 * math.cos(w0)
        a2 = 1.0 - alpha
        # Normalize
        self.b0 = b0 / a0
        self.b1 = b1 / a0
        self.b2 = b2 / a0
        self.a1 = a1 / a0
        self.a2 = a2 / a0
        # State
        self.x1 = 0.0
        self.x2 = 0.0
        self.y1 = 0.0
        self.y2 = 0.0

    def process(self, x: float) -> float:
        y = (self.b0 * x + self.b1 * self.x1 + self.b2 * self.x2
             - self.a1 * self.y1 - self.a2 * self.y2)
        self.x2 = self.x1
        self.x1 = x
        self.y2 = self.y1
        self.y1 = y
        return y


class NotchFilterChain:
    """Chain of notch filters for 60Hz + harmonics."""

    def __init__(self, fs: float = 48000.0):
        # Notch at 60Hz and harmonics, Q=30 for tight notch
        self.filters = []
        for freq in [60, 120, 180, 240, 300]:
            self.filters.append(BiquadNotch(freq, 30.0, fs))

    def process_sample(self, x: float) -> float:
        for f in self.filters:
            x = f.process(x)
        return x

    def process_block(self, samples: list[int]) -> bytes:
        out = []
        for s in samples:
            filtered = self.process_sample(float(s))
            # Clamp to int16 range
            v = max(-32768, min(32767, int(filtered)))
            out.append(v)
        return struct.pack(f'<{len(out)}h', *out)


def setup_mixer():
    """Configure mixer for mic input + speaker output."""
    commands = [
        # Mic input path — external jack mic, single-ended
        ["amixer", "-c", "Zero", "sset", "Mic 1 Amp Source MUX", "MIC_P"],
        ["amixer", "-c", "Zero", "sset", "Mic 1", "100%", "on"],
        ["amixer", "-c", "Zero", "sset", "Mixin Left Mic 1", "on"],
        ["amixer", "-c", "Zero", "sset", "Mixin PGA", "80%", "on"],
        ["amixer", "-c", "Zero", "sset", "ADC", "95%", "on"],
        ["amixer", "-c", "Zero", "sset", "MIC Jack", "on"],
        # Disable onboard MEMS mic
        ["amixer", "-c", "Zero", "sset", "Onboard MIC", "off"],
        ["amixer", "-c", "Zero", "sset", "Mixin Left Mic 2", "off"],
        ["amixer", "-c", "Zero", "sset", "Mixin Right Mic 2", "off"],
        # Speaker output path
        ["amixer", "-c", "Zero", "sset", "Mixout Left DAC Left", "on"],
        ["amixer", "-c", "Zero", "sset", "Mixout Right DAC Right", "on"],
        ["amixer", "-c", "Zero", "sset", "Lineout", "35%", "on"],
        ["amixer", "-c", "Zero", "sset", "DAC", "88%"],
        # Disable gain ramping for low latency
        ["amixer", "-c", "Zero", "sset", "Headphone Gain Ramping", "off"],
        ["amixer", "-c", "Zero", "sset", "Lineout Gain Ramping", "off"],
        ["amixer", "-c", "Zero", "sset", "DAC Gain Ramping", "off"],
    ]
    for cmd in commands:
        subprocess.run(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    print("Mixer configured.")


def main() -> int:
    use_filter = "--raw" not in sys.argv

    if "--setup" in sys.argv:
        setup_mixer()

    if use_filter:
        notch = NotchFilterChain(SAMPLE_RATE)
        print("60Hz notch filter: ON (60, 120, 180, 240, 300 Hz)")
    else:
        notch = None
        print("Notch filter: OFF (raw passthrough)")

    print(f"Voice loopback: {CAPTURE_DEVICE} → {PLAYBACK_DEVICE}")
    print(f"  {SAMPLE_RATE}Hz, {FORMAT}, {CHANNELS}ch, period={PERIOD_FRAMES}")
    print()
    print("Speak into the handset — you should hear yourself.")
    print("Press Ctrl+C to stop.")
    print()

    if notch is None:
        # Raw mode — direct pipe, lowest latency
        rec = subprocess.Popen(
            ["arecord", "-D", CAPTURE_DEVICE, "-f", FORMAT, "-r", str(SAMPLE_RATE),
             "-c", str(CHANNELS), "-t", "raw",
             "--period-size", str(PERIOD_FRAMES),
             "--buffer-size", str(PERIOD_FRAMES * 4)],
            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
        )
        play = subprocess.Popen(
            ["aplay", "-D", PLAYBACK_DEVICE, "-f", FORMAT, "-r", str(SAMPLE_RATE),
             "-c", str(CHANNELS), "-t", "raw", "-q"],
            stdin=rec.stdout, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        rec.stdout.close()
        try:
            play.wait()
        except KeyboardInterrupt:
            pass
        finally:
            for p in (rec, play):
                if p.poll() is None:
                    p.terminate()
    else:
        # Filtered mode — read blocks, filter, write
        rec = subprocess.Popen(
            ["arecord", "-D", CAPTURE_DEVICE, "-f", FORMAT, "-r", str(SAMPLE_RATE),
             "-c", str(CHANNELS), "-t", "raw",
             "--period-size", str(PERIOD_FRAMES),
             "--buffer-size", str(PERIOD_FRAMES * 4)],
            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
        )
        play = subprocess.Popen(
            ["aplay", "-D", PLAYBACK_DEVICE, "-f", FORMAT, "-r", str(SAMPLE_RATE),
             "-c", str(CHANNELS), "-t", "raw", "-q"],
            stdin=subprocess.PIPE, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )

        block_bytes = PERIOD_FRAMES * BYTES_PER_FRAME

        try:
            while True:
                data = rec.stdout.read(block_bytes)
                if not data:
                    break
                n_samples = len(data) // 2
                samples = list(struct.unpack(f'<{n_samples}h', data))
                filtered = notch.process_block(samples)
                play.stdin.write(filtered)
        except (KeyboardInterrupt, BrokenPipeError):
            pass
        finally:
            for p in (rec, play):
                if p.poll() is None:
                    p.terminate()
                    try:
                        p.wait(timeout=2)
                    except subprocess.TimeoutExpired:
                        p.kill()

    print("\nDone.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
