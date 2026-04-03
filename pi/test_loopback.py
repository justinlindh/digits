#!/usr/bin/env python3
"""Full loopback integration test: keypad -> Pico UART -> Pi audio confirmation."""

from __future__ import annotations

import argparse
import math
import os
import shutil
import subprocess
import sys
import tempfile
import time
import wave
from pathlib import Path
from typing import Optional

from test_uart import PicoUART


def run(cmd: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(cmd, capture_output=True, text=True)


def find_codec_zero_playback_device() -> Optional[str]:
    result = run(["aplay", "-l"])
    if result.returncode != 0:
        print("❌ Unable to run aplay -l")
        if result.stderr.strip():
            print(f"   {result.stderr.strip()}")
        return None

    for line in result.stdout.splitlines():
        lower = line.lower()
        if "card" in lower and "device" in lower and "codec_zero" in lower:
            # Example: card 3: codec_zerosound [codec_zerosound], device 0: ...
            card = line.split("card", 1)[1].split(":", 1)[0].strip()
            device = line.split("device", 1)[1].split(":", 1)[0].strip()
            if card.isdigit() and device.isdigit():
                return f"hw:{card},{device}"
    return None


def generate_tone_with_sox(out_wav: Path, duration_sec: float = 0.2) -> bool:
    sox = shutil.which("sox")
    if not sox:
        return False

    cmd = [
        sox,
        "-n",
        "-r",
        "8000",
        "-c",
        "1",
        "-b",
        "16",
        str(out_wav),
        "synth",
        str(duration_sec),
        "sine",
        "800",
        "vol",
        "0.35",
    ]
    result = run(cmd)
    if result.returncode == 0 and out_wav.exists() and out_wav.stat().st_size > 1000:
        return True

    if result.stderr.strip():
        print(f"⚠️ sox failed: {result.stderr.strip()}")
    return False


def generate_tone_python_fallback(out_wav: Path, duration_sec: float = 0.2) -> bool:
    sample_rate = 8000
    frequency_hz = 800.0
    amplitude = 0.35
    total_samples = int(sample_rate * duration_sec)

    try:
        with wave.open(str(out_wav), "wb") as wf:
            wf.setnchannels(1)
            wf.setsampwidth(2)
            wf.setframerate(sample_rate)
            frames = bytearray()
            for i in range(total_samples):
                sample = int(32767 * amplitude * math.sin(2.0 * math.pi * frequency_hz * (i / sample_rate)))
                frames.extend(sample.to_bytes(2, byteorder="little", signed=True))
            wf.writeframes(bytes(frames))
        return out_wav.exists() and out_wav.stat().st_size > 1000
    except Exception as exc:
        print(f"❌ Python tone fallback failed: {exc}")
        return False


def play_confirmation_tone(playback_device: str) -> bool:
    with tempfile.NamedTemporaryFile(prefix="digits_confirm_", suffix=".wav", delete=False) as tmp:
        wav_path = Path(tmp.name)

    try:
        print("🎵 Generating 800Hz confirmation tone...")
        used_sox = generate_tone_with_sox(wav_path)
        if used_sox:
            print("✅ Tone generated with sox")
        else:
            print("⚠️ sox not available (or failed), using Python fallback")
            if not generate_tone_python_fallback(wav_path):
                return False

        play_cmd = ["aplay", "-D", playback_device, str(wav_path)]
        result = run(play_cmd)
        if result.returncode != 0:
            print("❌ Failed to play confirmation tone")
            if result.stderr.strip():
                print(f"   {result.stderr.strip()}")
            return False

        print("🔊 Confirmation tone played")
        return True
    finally:
        try:
            wav_path.unlink(missing_ok=True)
        except OSError:
            pass


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Full loopback integration test: keypad -> Pico UART -> Pi audio"
    )
    parser.add_argument("--port", default="/dev/serial0", help="UART port (default: /dev/serial0)")
    parser.add_argument(
        "--expected-digits",
        type=int,
        default=7,
        help="Expected dial length to show progress (default: 7)",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()

    print("🧪 Digits Full Loopback Integration Test")
    print("   keypad → Pico → UART → Pi → audio")

    print("\n1️⃣ Detecting Codec Zero playback device...")
    playback_device = find_codec_zero_playback_device()
    if not playback_device:
        print("❌ Codec Zero playback device not found in `aplay -l`")
        return 1
    print(f"✅ Found playback device: {playback_device}")

    print("\n2️⃣ Connecting to Pico UART...")
    try:
        uart = PicoUART(port=args.port, baudrate=115200)
    except Exception as exc:
        print(f"❌ Failed to open UART {args.port}: {exc}")
        return 1

    dialed_digits = ""
    offhook_seen = False

    print("✅ UART connected")
    print("\n3️⃣ Waiting for HOOK:OFF (lift handset)...")
    print("   Press Ctrl+C to exit.")

    try:
        while True:
            line = uart.recv(timeout=0.5)
            if line is None:
                continue

            if line.startswith("HOOK:OFF"):
                offhook_seen = True
                dialed_digits = ""
                print("📞 Handset is OFF-HOOK. Ready to collect digits.")
                continue

            if line.startswith("HOOK:ON"):
                offhook_seen = False
                print("📴 Handset is ON-HOOK. Waiting for off-hook...")
                continue

            if line.startswith("KEY:"):
                key = line.split(":", 1)[1].strip()
                if offhook_seen:
                    dialed_digits += key
                    print(f"🔢 Key {key}  | Progress: {len(dialed_digits)} of {args.expected_digits} | Digits: {dialed_digits}")
                else:
                    print(f"🔢 Key {key} (ignored until off-hook)")
                continue

            if line.startswith("DIAL:"):
                number = line.split(":", 1)[1].strip()
                print(f"☎️ DIAL event received: {number}")
                if number:
                    print(f"✅ Dialed number complete: {number}")
                else:
                    print("⚠️ Empty DIAL payload")

                if not play_confirmation_tone(playback_device):
                    print("❌ Audio confirmation failed")
                    return 1
                print("🎉 Loopback path confirmed: keypad → UART → Pi audio")
                dialed_digits = ""
                continue

            print(f"📡 {line}")

    except KeyboardInterrupt:
        print("\n🛑 Test stopped by user.")
        return 130
    finally:
        uart.close()


if __name__ == "__main__":
    sys.exit(main())
