#!/usr/bin/env python3
"""Codec Zero (DA7212) verification + loopback test for Digits Pi Zero 2 W.

Checks include: i2cdetect -y 1, arecord -l, aplay -l.
"""

from __future__ import annotations

import os
import re
import subprocess
import sys
from pathlib import Path

TEST_WAV = Path("/tmp/digits_audio_test.wav")


def run_cmd(cmd: list[str]) -> subprocess.CompletedProcess:
    return subprocess.run(cmd, capture_output=True, text=True)


def stage(name: str, ok: bool, detail: str = "") -> bool:
    status = "PASS" if ok else "FAIL"
    suffix = f" - {detail}" if detail else ""
    print(f"[{status}] {name}{suffix}")
    return ok


def detect_codec_zero_i2c() -> bool:
    proc = run_cmd(["i2cdetect", "-y", "1"])
    if proc.returncode != 0:
        return stage("I2C detect command", False, proc.stderr.strip() or "i2cdetect failed")

    output = proc.stdout.lower()
    found = "1a" in output
    return stage("Codec Zero present on I2C bus 1 @ 0x1a", found)


def find_codec_zero_device(cmd: list[str], label: str) -> tuple[bool, str | None]:
    proc = run_cmd(cmd)
    if proc.returncode != 0:
        stage(f"{label} device list", False, proc.stderr.strip() or f"{' '.join(cmd)} failed")
        return False, None

    text = proc.stdout
    for line in text.splitlines():
        if "codec_zero" not in line.lower():
            continue

        m = re.search(r"card\s+(\d+):.*device\s+(\d+):", line, re.IGNORECASE)
        if not m:
            continue

        card, device = m.group(1), m.group(2)
        alsa_dev = f"hw:{card},{device}"
        stage(f"Found Codec Zero {label} device", True, alsa_dev)
        return True, alsa_dev

    stage(f"Found Codec Zero {label} device", False, "No codec_zero entry in ALSA list")
    return False, None


def record_test(capture_dev: str) -> bool:
    if TEST_WAV.exists():
        TEST_WAV.unlink()

    cmd = [
        "arecord",
        "-D",
        capture_dev,
        "-f",
        "S16_LE",
        "-r",
        "8000",
        "-c",
        "1",
        "-d",
        "3",
        str(TEST_WAV),
    ]
    proc = run_cmd(cmd)
    if proc.returncode != 0:
        return stage("Record 3s handset mic sample", False, proc.stderr.strip() or "arecord failed")

    return stage("Record 3s handset mic sample", True, str(TEST_WAV))


def validate_wav_size() -> bool:
    if not TEST_WAV.exists():
        return stage("Recorded WAV exists", False, f"Missing {TEST_WAV}")

    size = TEST_WAV.stat().st_size
    # 3s * 8000 samples/s * 2 bytes/sample = ~48000 bytes + header.
    reasonable = size >= 8000
    return stage("Recorded WAV file size reasonable", reasonable, f"{size} bytes")


def playback_test(playback_dev: str) -> bool:
    cmd = ["aplay", "-D", playback_dev, str(TEST_WAV)]
    proc = run_cmd(cmd)
    if proc.returncode != 0:
        return stage("Playback to handset earpiece", False, proc.stderr.strip() or "aplay failed")

    return stage("Playback to handset earpiece", True)


def main() -> int:
    checks: list[bool] = []

    checks.append(detect_codec_zero_i2c())

    cap_ok, capture_dev = find_codec_zero_device(["arecord", "-l"], "capture")
    play_ok, playback_dev = find_codec_zero_device(["aplay", "-l"], "playback")
    checks.extend([cap_ok, play_ok])

    if cap_ok and capture_dev:
        checks.append(record_test(capture_dev))
        checks.append(validate_wav_size())
    else:
        checks.append(stage("Record 3s handset mic sample", False, "Capture device unavailable"))
        checks.append(stage("Recorded WAV file size reasonable", False, "No recording created"))

    if play_ok and playback_dev and TEST_WAV.exists():
        checks.append(playback_test(playback_dev))
    else:
        checks.append(stage("Playback to handset earpiece", False, "Playback device or WAV unavailable"))

    all_ok = all(checks)
    print("\nOverall:", "PASS" if all_ok else "FAIL")
    return 0 if all_ok else 1


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        print("\nInterrupted")
        raise SystemExit(130)
