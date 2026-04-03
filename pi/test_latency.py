#!/usr/bin/env python3
"""Measure audio playback latency on the Codec Zero.

Tests multiple approaches and logs timestamps at each stage.
"""

import subprocess
import time
import os
import wave
import array

TONE_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'tones')
TONE_FILE = os.path.join(TONE_DIR, 'tone_dial.wav')


def test_aplay_wav(device: str, label: str, buffer_size: int | None = None) -> None:
    """Test latency of spawning aplay with a WAV file."""
    cmd = ['aplay', '-D', device, '-q', TONE_FILE]
    if buffer_size:
        cmd.extend(['--buffer-size', str(buffer_size), '--period-size', str(buffer_size // 4)])

    t0 = time.monotonic_ns()
    proc = subprocess.Popen(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    t1 = time.monotonic_ns()
    time.sleep(1.5)
    proc.terminate()
    proc.wait()

    fork_ms = (t1 - t0) / 1_000_000
    print(f"[{label}] fork+exec: {fork_ms:.1f}ms")


def test_pipe_persistent(device: str, label: str, buffer_size: int = 2048) -> None:
    """Test latency of writing to a persistent aplay pipe."""
    # Load tone data as stereo
    with wave.open(TONE_FILE, 'r') as wf:
        raw = wf.readframes(wf.getnframes())
    mono = array.array('h')
    mono.frombytes(raw)
    stereo = array.array('h')
    for s in mono:
        stereo.append(s)
        stereo.append(s)
    pcm = stereo.tobytes()

    period = buffer_size // 4
    cmd = ['aplay', '-D', device, '-f', 'S16_LE', '-r', '48000',
           '-c', '2', '-t', 'raw', '-q',
           '--buffer-size', str(buffer_size), '--period-size', str(period)]

    proc = subprocess.Popen(cmd, stdin=subprocess.PIPE,
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

    # Let it initialize
    time.sleep(0.5)

    # Write silence first to prime the pipe
    silence = b'\x00' * (buffer_size * 4)
    proc.stdin.write(silence)
    proc.stdin.flush()
    time.sleep(0.3)

    # Now measure write latency
    t0 = time.monotonic_ns()
    proc.stdin.write(pcm[:buffer_size * 4])  # write one buffer
    proc.stdin.flush()
    t1 = time.monotonic_ns()

    time.sleep(1.5)
    proc.terminate()
    proc.wait()

    write_ms = (t1 - t0) / 1_000_000
    print(f"[{label}] pipe write: {write_ms:.1f}ms")


def test_rapid_sequence(device: str, label: str) -> None:
    """Simulate 3 rapid DTMF presses ~500ms apart via separate aplay calls."""
    dtmf_files = [
        os.path.join(TONE_DIR, 'dtmf_1.wav'),
        os.path.join(TONE_DIR, 'dtmf_2.wav'),
        os.path.join(TONE_DIR, 'dtmf_3.wav'),
    ]
    times = []
    for f in dtmf_files:
        t0 = time.monotonic_ns()
        proc = subprocess.Popen(
            ['aplay', '-D', device, '-q', f],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        t1 = time.monotonic_ns()
        times.append((t0, t1, proc))
        time.sleep(0.5)

    # Wait for all to finish
    for _, _, proc in times:
        proc.wait()

    print(f"[{label}] 3 rapid DTMF fork times:")
    for i, (t0, t1, _) in enumerate(times):
        ms = (t1 - t0) / 1_000_000
        print(f"  press {i+1}: fork {ms:.1f}ms")


def main():
    print("=" * 60)
    print("Codec Zero Latency Test")
    print("=" * 60)
    print()

    # Test 1: aplay WAV with default buffers via plughw
    print("--- Test 1: aplay WAV (plughw:Zero, default buffers) ---")
    print("LISTEN: you should hear tone ~NOW")
    test_aplay_wav('plughw:Zero', 'plughw-default')
    time.sleep(0.5)

    # Test 2: aplay WAV with small buffers via plughw
    print("\n--- Test 2: aplay WAV (plughw:Zero, buffer=2048) ---")
    print("LISTEN: you should hear tone ~NOW")
    test_aplay_wav('plughw:Zero', 'plughw-small', buffer_size=2048)
    time.sleep(0.5)

    # Test 3: persistent pipe to hw:Zero
    print("\n--- Test 3: Persistent pipe (hw:Zero, buffer=2048) ---")
    print("LISTEN: you should hear tone ~NOW")
    test_pipe_persistent('hw:Zero', 'pipe-hw', buffer_size=2048)
    time.sleep(0.5)

    # Test 4: persistent pipe with larger buffer
    print("\n--- Test 4: Persistent pipe (hw:Zero, buffer=8192) ---")
    print("LISTEN: you should hear tone ~NOW")
    test_pipe_persistent('hw:Zero', 'pipe-hw-large', buffer_size=8192)
    time.sleep(0.5)

    # Test 5: rapid DTMF sequence
    print("\n--- Test 5: 3 rapid DTMF presses (plughw:Zero) ---")
    print("LISTEN: should hear 1-2-3 spaced ~500ms apart")
    test_rapid_sequence('plughw:Zero', 'rapid-plughw')
    time.sleep(0.5)

    print("\n" + "=" * 60)
    print("Tests complete. Check which sounded most responsive.")
    print("=" * 60)


if __name__ == '__main__':
    main()
