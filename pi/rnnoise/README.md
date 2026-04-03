# RNNoise — Real-Time Noise Suppression for Digits

Pre-built static library (aarch64, cross-compiled from [xiph/rnnoise](https://github.com/xiph/rnnoise))
for the Pi Zero 2 W with Codec Zero audio board.

## Why a static .a in git?

The Pi Zero 2 W has 512MB RAM. Compiling RNNoise from source (especially `rnnoise_data.c` — 58MB of
model weights) causes OOM. The static lib was cross-compiled on an x86_64 machine with
`aarch64-linux-gnu-gcc` and is ready to link on any aarch64 Debian system.

## Prerequisites

```bash
sudo apt-get install -y gcc libasound2-dev
```

## Build

```bash
cd pi/rnnoise
make
```

Produces three binaries:

| Binary | Description |
|--------|-------------|
| `rnnoise_bench` | 10-second benchmark — captures from mic, processes through RNNoise, reports CPU timing vs 10ms frame budget |
| `rnnoise_rp` | Record N seconds → denoise → playback through earpiece. `--raw` flag adds raw playback for A/B comparison |
| `rnnoise_loop` | Real-time mic → RNNoise → earpiece loopback. `--raw` flag bypasses denoise for comparison. Ctrl+C to stop |

## Performance (Pi Zero 2 W)

Tested 2026-03-21:

- **Frame budget:** 10.0ms (480 samples @ 48kHz)
- **Avg process time:** 3.99ms (39.9% of budget)
- **Max spike:** 5.81ms
- **ALSA overruns:** 0
- **Verdict:** ✅ Real-time capable with ~60% headroom

## Audio setup

The Codec Zero requires stereo capture (DA7213 constraint). These tools capture stereo and
extract the left channel (external mic jack). Playback is also stereo (mono duplicated to both channels).

Lineout must be enabled and unmuted for earpiece playback:

```bash
amixer -c Zero sset 'Lineout' 50 on
amixer -c Zero sset 'Mixout Left DAC Left' on
amixer -c Zero sset 'Mixout Right DAC Right' on
```

## Rebuilding the static library (cross-compile)

If you need to rebuild from source (e.g., new RNNoise version):

```bash
# On an x86_64 machine with aarch64 cross-compiler:
sudo apt-get install -y gcc-aarch64-linux-gnu
git clone https://github.com/xiph/rnnoise.git
cd rnnoise
./autogen.sh
./configure --host=aarch64-linux-gnu --enable-static --disable-shared \
            --disable-examples --disable-doc CC=aarch64-linux-gnu-gcc
make -j$(nproc)
cp .libs/librnnoise.a /path/to/digits/pi/rnnoise/
cp include/rnnoise.h /path/to/digits/pi/rnnoise/
```
