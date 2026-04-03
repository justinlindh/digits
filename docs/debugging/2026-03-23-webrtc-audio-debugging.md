# WebRTC Audio Debugging Session — 2026-03-22/23

## Summary
Full end-to-end WebRTC audio working in both directions. Browser→Pi has ~500-600ms sustained one-way latency that slowly drifts (~12ms/sec). Pi→Browser has negligible perceptible latency.

## Bugs Found & Fixed

### 1. RTP Header Corruption (ROOT CAUSE of garbled audio)
**Symptom:** Garbled static, escalating noise that eventually clips at full scale.  
**Root cause:** `track.Read(buf)` returns the **full RTP packet** (12+ byte header + payload), but we were passing the entire buffer to the Opus decoder. The decoder treated the RTP headers as audio data, causing cumulative state corruption.  
**Fix:** Changed to `track.ReadRTP()` which parses the RTP packet, then pass only `pkt.Payload` to the decoder.  
**Files:** `cmd/digitsd/main.go` — both `makeCall` and `AnswerCall` handlers.  
**Evidence:** PCM dump file showed exponentially growing peak levels: 0→32768 over 6 seconds.

### 2. Silence Spin Loop (caused constant garbage playback)
**Symptom:** Garbled static even after decode fix.  
**Root cause:** `playbackLoop()` had a `default` case in `select` that wrote silence frames in a tight spin loop. Real audio frames got buried — maybe 1 in 10,000 writes was actual audio.  
**Fix:** Removed the `default` case entirely. ALSA underruns are preferable to 99.99% silence injection.  
**File:** `internal/audio/pipeline.go`

### 3. Channel Queue Latency Accumulation  
**Symptom:** ~400ms sustained latency from buffered frames.  
**Root cause:** `inPCM` channel (capacity 8) was constantly full. RTP packets arrive in ~10ms bursts, playback consumes at ~20ms/frame. Channel backpressure caused `chan_send` to block ~30ms per frame.  
**Fix:** Eliminated the channel entirely. WebRTC track reader goroutine now calls `WritePlayback()` directly — `snd_pcm_writei` blocking IS the rate limiter (same as rnnoise_rp and latbench).  
**File:** `internal/audio/pipeline.go`, `cmd/digitsd/main.go`

### 4. Pre-Answer RTP Buffering  
**Symptom:** First ~2 seconds of call had buffered audio played back, causing fixed offset.  
**Root cause:** `OnRemoteTrack` fires when WebRTC connects (during ring), but audio pipeline doesn't start until user goes off-hook. RTP packets queue in pion's internal buffer during the entire ring duration.  
**Fix:** `OnRemoteTrack` goroutine reads and discards packets while `d.pipeline == nil`, then does a fast drain when pipeline becomes available.  
**File:** `cmd/digitsd/main.go`

### 5. Missing RTCP Drain  
**Symptom:** Potential buffer backpressure in pion.  
**Root cause:** `pc.AddTrack()` returns an `*RTPSender` whose RTCP packets must be read. We were discarding the sender. Unread RTCP can back up pion's internal buffers.  
**Fix:** Added goroutine to drain RTCP from the RTPSender in `peer.go`.  
**File:** `internal/webrtc/peer.go`  
**Note:** Did NOT measurably reduce latency in automated testing.

## Remaining Latency Issue

### Measured: ~500-600ms one-way, Browser/VM→Pi direction only
- **Pi→Browser:** Near-instantaneous (sub-100ms perceived)
- **Browser→Pi:** ~500-600ms sustained, slowly growing (~12ms/sec drift)

### Measurement Method
Built `cmd/latclient/main.go` — a Go WebRTC client that:
1. Connects to signald as extension 3140002
2. Calls 3140001 (Pi)
3. Auto-answers via UART log injection (`RX: HOOK:OFF`)
4. Sends 1kHz tone with precise wallclock timestamps
5. Both sides NTP-synced (Pi offset +16ms, VM +0.7ms, net ~15ms)

### Measurement Data (01:00 test)
```
Warmup start (VM): 00:56.133 → Pi first play: 00:56.629 (adj 56.614) = 481ms
SEND[50]  VM 01:00.133 → Pi PLAY[202] 01:00.665 (adj 00.650) = 517ms
SEND[250] VM 01:04.134 → Pi PLAY[405] 01:04.729 (adj 04.714) = 580ms
SEND[450] VM 01:08.134 → Pi PLAY[607] 01:08.761 (adj 08.746) = 612ms
```
Drift: +95ms over 8 seconds ≈ 12ms/sec

### What's NOT the cause
- **Opus decode:** 486µs average, 789µs max (per latbench)
- **ALSA write (snd_pcm_writei):** 19.3ms average = exactly one 960-sample period at 48kHz. Blocks for real-time pacing. Zero overhead.
- **Go pipeline code:** latbench plays 3 seconds of decode→ALSA in 2.963s wall time
- **Network:** VM→Pi ping avg 6.4ms, max 18ms
- **Ring/answer time:** Eliminated from measurement via warmup + auto-answer

### Suspected causes
1. **dmix buffer:** 2048 samples at 48kHz = 42.7ms. Measured by `dmixlat` tool. Cannot bypass because `dtmf_uart.py` holds `hw:Zero` device exclusively.
2. **dtmf_uart.py keepalive:** Runs `aplay /dev/zero` continuously through dmix. Creates a permanently-open stale playback stream. dmix must synchronize our writes with this stream.
3. **pion's `TrackLocalStaticSample.WriteSample()`:** May add internal buffering/packetization delay. Alternative: `TrackLocalStaticRTP` for lower-level control.
4. **Go runtime:** GC pauses, goroutine scheduling on Pi Zero (single-core ARM). Could cause micro-stalls that accumulate.
5. **Clock drift between sender timing and dmix timing:** The 12ms/sec growth suggests the audio consumption rate doesn't perfectly match the production rate. dmix runs at its own clock rate.

### The asymmetry explained
- **Pi→Browser (fast):** Capture reads directly from `plughw:1,0` (no dmix), encode + send is immediate. Browser has a small optimized jitter buffer.
- **Browser→Pi (slow):** Must play through dmix (shared with dtmf_uart.py keepalive). dmix adds ~42ms minimum. The stale keepalive stream may cause additional synchronization overhead.

## Tools Built
| Tool | Path | Purpose |
|------|------|---------|
| `alsatest` | `cmd/alsatest/main.go` | Play 440Hz sine through ALSA, accepts device arg |
| `latbench` | `cmd/latbench/main.go` | Opus encode→decode→ALSA playback benchmark |
| `dmixlat` | `cmd/dmixlat/main.go` | Measure ALSA output delay via `snd_pcm_delay()` |
| `latclient` | `cmd/latclient/main.go` | Go WebRTC client for automated latency measurement |

## Key Architecture Facts (Updated 2026-03-23)
- **Playback device:** `plughw:1,0` (direct hardware, no dmix). digitsd owns ALSA exclusively.
- **Capture device:** `plughw:1,0` (direct hardware access, no dmix).
- **dtmf_uart.py: RETIRED.** Replaced by digitsd which handles serial, tones, DAC keepalive, and socket server in Go.
- **DAC keepalive:** In-process silence writer in digitsd (prevents DA7213 power-down ramp-up latency).
- **Tone generation:** PCM samples loaded from WAV files at startup, written directly to ALSA.
- Codec Zero (DA7213) at 48kHz, stereo capture (external mic on right channel).
- ALSA config: `/etc/asound.conf` still has dmix config but it's no longer used by digitsd.

## Resolution (2026-03-23)

**ROOT CAUSE CONFIRMED: dmix + dtmf_uart.py DAC keepalive.**

dtmf_uart.py was consolidated into digitsd (Go) — commits `b0fea75` through `175d87e`. digitsd now owns serial port, UART protocol, tone playback, DAC keepalive, and socket server directly. Playback uses `plughw:1,0` (direct hardware, no dmix).

### Results After Fix
- **Before:** 500-600ms one-way, drifting +12ms/sec
- **After:** ~75-90ms one-way, **zero drift**, rock stable over 15s tests
- **recv→play delta:** Consistent 19.9ms (exactly one ALSA period at 48kHz)
- Human-verified as "absolutely flawless" during real browser↔phone call

### What Fixed It
1. Eliminated dmix from playback path (~42ms buffer gone)
2. Eliminated dmix sync overhead with stale keepalive stream (unknown but significant)
3. Eliminated the 12ms/sec clock drift (caused by dmix running at its own clock rate vs the RTP stream rate)
4. GC disable during calls (`debug.SetGCPercent(-1)`) removed micro-stalls
5. 60ms jitter buffer smooths network jitter without accumulating delay
6. ALSA startup stall fix eliminated initial 935ms spike

### What Was NOT the Cause
- Pion's `WriteSample()` / `TrackLocalStaticRTP` — not a factor once dmix was removed
- Go runtime scheduling — negligible on Pi Zero (single-core ARM)
- Network — 6.4ms avg, trivial contribution

### Lesson Learned
Justin suggested removing dtmf_uart.py multiple times before this was attempted. The architectural simplification was the correct fix all along. Hours of incremental debugging (channel tuning, buffer sizing, GC profiling) were wasted because the fundamental problem was a shared audio device with a stale keepalive stream causing dmix synchronization overhead and clock drift.
