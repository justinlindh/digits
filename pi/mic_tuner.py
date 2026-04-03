#!/usr/bin/env python3
"""Interactive terminal mic tuner for Codec Zero.

SSH into the Pi and run this to adjust gain settings in real-time
while monitoring mic input levels.

Controls:
  1/2  — Mic 1 gain down/up (0-100%, 6dB steps)
  3/4  — Mixin PGA down/up (0-100%)
  5/6  — ADC gain down/up (0-100%)
  m    — Toggle onboard MEMS mic (Mic 2)
  r    — Record 5s and save to /tmp/mic_tuner_sample.wav
  q    — Quit

Requires: arecord available, Codec Zero on card 'Zero'
"""

import os
import struct
import subprocess
import sys
import termios
import time
import tty
import math
import threading


CARD = "Zero"
SAMPLE_RATE = 48000
MIC1_STEPS = [0, 14, 29, 43, 57, 71, 86, 100]  # rough 6dB steps
MIC1_DB = ["mute", "6dB", "12dB", "18dB", "24dB", "30dB", "36dB"]


class MicTuner:
    def __init__(self):
        self.mic1_idx = 4       # 57% / 18dB
        self.pga_pct = 33
        self.adc_pct = 95
        self.mems_on = False
        self.running = True
        self.recording = False
        self.last_rms = 0
        self.last_peak = 0
        self.last_60hz = 0

        # Initial setup
        self._amixer("Mic 1 Amp Source MUX", "MIC_P")
        self._amixer("Mic 1", f"{MIC1_STEPS[self.mic1_idx]}%", "on")
        self._amixer("Mixin Left Mic 1", "on")
        self._amixer("MIC Jack", "on")
        self._amixer("Onboard MIC", "off")
        self._amixer("Mixin Left Mic 2", "off")
        self._amixer("Mixin Right Mic 2", "off")
        self._amixer("Mixin PGA", f"{self.pga_pct}%", "on")
        self._amixer("ADC", f"{self.adc_pct}%", "on")

    def _amixer(self, control, *args):
        cmd = ["amixer", "-c", CARD, "sset", control] + list(args)
        subprocess.run(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

    def _get_level(self, control):
        r = subprocess.run(["amixer", "-c", CARD, "sget", control],
                           capture_output=True, text=True)
        for line in r.stdout.splitlines():
            if "Mono:" in line:
                # Extract percentage
                if "[" in line:
                    start = line.index("[") + 1
                    end = line.index("%")
                    return int(line[start:end])
        return -1

    def apply_settings(self):
        self._amixer("Mic 1", f"{MIC1_STEPS[self.mic1_idx]}%", "on")
        self._amixer("Mixin PGA", f"{self.pga_pct}%", "on")
        self._amixer("ADC", f"{self.adc_pct}%", "on")
        if self.mems_on:
            self._amixer("Onboard MIC", "on")
            self._amixer("Mixin Left Mic 2", "on")
        else:
            self._amixer("Onboard MIC", "off")
            self._amixer("Mixin Left Mic 2", "off")
            self._amixer("Mixin Right Mic 2", "off")

    def monitor_loop(self):
        """Continuously read mic and compute levels."""
        while self.running:
            try:
                proc = subprocess.Popen(
                    ["arecord", "-D", "plughw:Zero", "-f", "S16_LE",
                     "-r", str(SAMPLE_RATE), "-c", "1", "-d", "1", "-t", "raw"],
                    stdout=subprocess.PIPE, stderr=subprocess.DEVNULL
                )
                data = proc.stdout.read()
                proc.wait()

                if len(data) < 4:
                    continue

                n = len(data) // 2
                samples = struct.unpack(f'<{n}h', data)

                # RMS
                mean = sum(samples) / n
                ac_rms = math.sqrt(sum((s - mean) ** 2 for s in samples) / n)

                # Peak
                peak = max(abs(s) for s in samples)

                # 60Hz magnitude (DFT at 60Hz)
                N = min(n, 48000)
                re60 = sum(samples[i] * math.cos(2 * math.pi * 60 * i / SAMPLE_RATE) for i in range(N)) / N
                im60 = sum(samples[i] * math.sin(2 * math.pi * 60 * i / SAMPLE_RATE) for i in range(N)) / N
                mag60 = 2 * math.sqrt(re60 * re60 + im60 * im60)

                self.last_rms = ac_rms
                self.last_peak = peak
                self.last_60hz = mag60

            except Exception:
                time.sleep(0.5)

    def record_sample(self):
        self.recording = True
        subprocess.run(
            ["arecord", "-D", "plughw:Zero", "-f", "S16_LE",
             "-r", str(SAMPLE_RATE), "-c", "1", "-d", "5",
             "/tmp/mic_tuner_sample.wav"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL
        )
        self.recording = False

    def draw(self):
        # VU meter bar
        bar_len = 40
        level = min(1.0, self.last_rms / 5000.0)
        filled = int(level * bar_len)
        bar = "█" * filled + "░" * (bar_len - filled)

        os.system("clear")
        print("╔══════════════════════════════════════════════╗")
        print("║         Codec Zero Mic Tuner                ║")
        print("╠══════════════════════════════════════════════╣")
        print(f"║  Mic 1 Gain:  {MIC1_STEPS[self.mic1_idx]:>3}%  [1↓ 2↑]              ║")
        print(f"║  Mixin PGA:   {self.pga_pct:>3}%  [3↓ 4↑]              ║")
        print(f"║  ADC:         {self.adc_pct:>3}%  [5↓ 6↑]              ║")
        print(f"║  MEMS Mic:    {'ON ' if self.mems_on else 'OFF'}   [m toggle]            ║")
        print("╠══════════════════════════════════════════════╣")
        print(f"║  RMS:  {self.last_rms:>7.1f}  |{bar}|     ║")
        print(f"║  Peak: {self.last_peak:>7}  60Hz: {self.last_60hz:>7.1f}          ║")
        print("╠══════════════════════════════════════════════╣")
        if self.recording:
            print("║  ● RECORDING to /tmp/mic_tuner_sample.wav   ║")
        else:
            print("║  [r] Record 5s   [q] Quit                   ║")
        print("╚══════════════════════════════════════════════╝")

    def run(self):
        # Start monitor thread
        t = threading.Thread(target=self.monitor_loop, daemon=True)
        t.start()

        # Set terminal to raw mode
        fd = sys.stdin.fileno()
        old = termios.tcgetattr(fd)
        try:
            tty.setraw(fd)
            tty.setcbreak(fd)

            while self.running:
                self.draw()
                # Non-blocking read with timeout
                import select
                if select.select([sys.stdin], [], [], 0.5)[0]:
                    ch = sys.stdin.read(1)
                    if ch == 'q':
                        self.running = False
                    elif ch == '1':
                        self.mic1_idx = max(0, self.mic1_idx - 1)
                        self.apply_settings()
                    elif ch == '2':
                        self.mic1_idx = min(len(MIC1_STEPS) - 1, self.mic1_idx + 1)
                        self.apply_settings()
                    elif ch == '3':
                        self.pga_pct = max(0, self.pga_pct - 5)
                        self.apply_settings()
                    elif ch == '4':
                        self.pga_pct = min(100, self.pga_pct + 5)
                        self.apply_settings()
                    elif ch == '5':
                        self.adc_pct = max(0, self.adc_pct - 5)
                        self.apply_settings()
                    elif ch == '6':
                        self.adc_pct = min(100, self.adc_pct + 5)
                        self.apply_settings()
                    elif ch == 'm':
                        self.mems_on = not self.mems_on
                        self.apply_settings()
                    elif ch == 'r' and not self.recording:
                        rt = threading.Thread(target=self.record_sample, daemon=True)
                        rt.start()
        finally:
            termios.tcsetattr(fd, termios.TCSADRAIN, old)
            print("\nDone.")


if __name__ == "__main__":
    MicTuner().run()
