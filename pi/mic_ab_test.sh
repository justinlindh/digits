#!/bin/bash
# A/B test: records with and without notch filter
# Talk into handset continuously while this runs

# Ensure correct mic routing
amixer -c Zero sset 'Onboard MIC' off > /dev/null 2>&1
amixer -c Zero sset 'Mixin Left Mic 2' off > /dev/null 2>&1
amixer -c Zero sset 'Mixin Right Mic 2' off > /dev/null 2>&1
amixer -c Zero sset 'Mic 1 Amp Source MUX' 'MIC_P' > /dev/null 2>&1
amixer -c Zero sset 'Mic 1' 71% on > /dev/null 2>&1
amixer -c Zero sset 'Mixin Left Mic 1' on > /dev/null 2>&1
amixer -c Zero sset 'Mixin PGA' 33% on > /dev/null 2>&1
amixer -c Zero sset 'ADC' 95% on > /dev/null 2>&1
amixer -c Zero sset 'MIC Jack' on > /dev/null 2>&1

echo "=== A/B Mic Test ==="
echo "Talk into the handset for the next 20 seconds..."
echo ""

echo "Recording RAW (no filter) for 10s..."
arecord -D plughw:Zero -f S16_LE -r 48000 -c 1 -d 10 /tmp/mic_raw.wav 2>/dev/null
echo "Raw done."

echo "Recording FILTERED for 10s..."
arecord -D plughw:Zero -f S16_LE -r 48000 -c 1 -d 10 -t raw /tmp/mic_capture.raw 2>/dev/null
python3 << 'PYEOF'
import struct, math, wave

class BiquadNotch:
    def __init__(self, freq, q, fs):
        w0 = 2.0 * math.pi * freq / fs
        alpha = math.sin(w0) / (2.0 * q)
        b0 = 1.0; b1 = -2.0 * math.cos(w0); b2 = 1.0
        a0 = 1.0 + alpha; a1 = -2.0 * math.cos(w0); a2 = 1.0 - alpha
        self.b0=b0/a0; self.b1=b1/a0; self.b2=b2/a0; self.a1=a1/a0; self.a2=a2/a0
        self.x1=0; self.x2=0; self.y1=0; self.y2=0
    def process(self, x):
        y = self.b0*x + self.b1*self.x1 + self.b2*self.x2 - self.a1*self.y1 - self.a2*self.y2
        self.x2=self.x1; self.x1=x; self.y2=self.y1; self.y1=y
        return y

# POTS bandpass: 300-3400 Hz (2nd order Butterworth HPF + LPF)
class BiquadHPF:
    """2nd-order Butterworth highpass."""
    def __init__(self, freq, fs):
        w0 = 2.0 * math.pi * freq / fs
        alpha = math.sin(w0) / (2.0 * 0.7071)  # Q=sqrt(2)/2 for Butterworth
        cosw = math.cos(w0)
        a0 = 1.0 + alpha
        self.b0 = ((1.0 + cosw) / 2.0) / a0
        self.b1 = (-(1.0 + cosw)) / a0
        self.b2 = ((1.0 + cosw) / 2.0) / a0
        self.a1 = (-2.0 * cosw) / a0
        self.a2 = (1.0 - alpha) / a0
        self.x1=0; self.x2=0; self.y1=0; self.y2=0
    def process(self, x):
        y = self.b0*x + self.b1*self.x1 + self.b2*self.x2 - self.a1*self.y1 - self.a2*self.y2
        self.x2=self.x1; self.x1=x; self.y2=self.y1; self.y1=y
        return y

class BiquadLPF:
    """2nd-order Butterworth lowpass."""
    def __init__(self, freq, fs):
        w0 = 2.0 * math.pi * freq / fs
        alpha = math.sin(w0) / (2.0 * 0.7071)
        cosw = math.cos(w0)
        a0 = 1.0 + alpha
        self.b0 = ((1.0 - cosw) / 2.0) / a0
        self.b1 = (1.0 - cosw) / a0
        self.b2 = ((1.0 - cosw) / 2.0) / a0
        self.a1 = (-2.0 * cosw) / a0
        self.a2 = (1.0 - alpha) / a0
        self.x1=0; self.x2=0; self.y1=0; self.y2=0
    def process(self, x):
        y = self.b0*x + self.b1*self.x1 + self.b2*self.x2 - self.a1*self.y1 - self.a2*self.y2
        self.x2=self.x1; self.x1=x; self.y2=self.y1; self.y1=y
        return y

# HPF at 200Hz (4th order) to kill low-freq rumble
# LPF at 3400Hz (4th order) to kill high-freq noise
# Notch comb at every 60Hz harmonic within passband (200-3400Hz)
filters = [
    BiquadHPF(200.0, 48000.0),
    BiquadHPF(200.0, 48000.0),
    BiquadLPF(3400.0, 48000.0),
    BiquadLPF(3400.0, 48000.0),
] + [BiquadNotch(f, 30.0, 48000.0) for f in range(60, 3401, 60)]

with open('/tmp/mic_capture.raw', 'rb') as f:
    raw = f.read()
samples = struct.unpack('<{}h'.format(len(raw)//2), raw)

out = []
for s in samples:
    x = float(s)
    for filt in filters:
        x = filt.process(x)
    out.append(max(-32768, min(32767, int(x))))

with wave.open('/tmp/mic_filtered.wav', 'w') as w:
    w.setnchannels(1)
    w.setsampwidth(2)
    w.setframerate(48000)
    w.writeframes(struct.pack('<{}h'.format(len(out)), *out))
print("Filtered done.")
PYEOF

echo ""
echo "Files ready:"
echo "  /tmp/mic_raw.wav      (no filter)"
echo "  /tmp/mic_filtered.wav (60Hz notch filter)"
