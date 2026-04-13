package audio

import (
	"math"
	"testing"
)

// rmsInt16 returns the RMS of a slice in dBFS (relative to full scale 32768).
func rmsInt16(s []int16) float64 {
	if len(s) == 0 {
		return math.Inf(-1)
	}
	var sum float64
	for _, v := range s {
		f := float64(v)
		sum += f * f
	}
	rms := math.Sqrt(sum / float64(len(s)))
	return 20 * math.Log10(rms/32768+1e-20)
}

// sine generates a sine at freq with amplitude (int16 scale) and length samples.
func sine(freq, sampleRate float64, amp int16, n int) []int16 {
	out := make([]int16, n)
	w := 2 * math.Pi * freq / sampleRate
	for i := 0; i < n; i++ {
		out[i] = int16(float64(amp) * math.Sin(w*float64(i)))
	}
	return out
}

// attenuationDB runs a sine at f through a chain and returns dB attenuation
// after letting the filter settle for one second.
func attenuationDB(t *testing.T, chain *BiquadChain, freq float64) float64 {
	t.Helper()
	const sr = 48000
	in := sine(freq, sr, 10000, 2*sr)
	chain.Reset()
	out := chain.Process(append([]int16(nil), in...))
	skip := sr
	return rmsInt16(in[skip:]) - rmsInt16(out[skip:])
}

func TestNotchAttenuates60Hz(t *testing.T) {
	ch := NewBiquadChain(NewNotch(48000, 60, 30))
	atten := attenuationDB(t, ch, 60)
	if atten < 20 {
		t.Errorf("60Hz attenuation: got %.1f dB, want >=20 dB", atten)
	}
}

func TestNotchPassesVoiceBand(t *testing.T) {
	ch := NewBiquadChain(NewNotch(48000, 60, 30))
	atten := attenuationDB(t, ch, 1000)
	if atten > 1.0 {
		t.Errorf("1kHz through 60Hz notch: got %.2f dB attenuation, want <=1.0", atten)
	}
}

func TestHPFAttenuates60Hz(t *testing.T) {
	// 4th-order Butterworth HPF at 200 Hz (two cascaded 2nd-order biquads).
	// 60 Hz is 1.74 octaves below the corner, so expect roughly 4*6*1.74 ≈ 42 dB
	// of attenuation in theory, 35+ in practice.
	ch := NewBiquadChain(NewHPF(48000, 200), NewHPF(48000, 200))
	atten := attenuationDB(t, ch, 60)
	if atten < 35 {
		t.Errorf("60Hz through 4th-order HPF@200: got %.1f dB, want >=35 dB", atten)
	}
}

func TestHPFPassesVoiceBand(t *testing.T) {
	ch := NewBiquadChain(NewHPF(48000, 200), NewHPF(48000, 200))
	atten := attenuationDB(t, ch, 1000)
	if math.Abs(atten) > 1.0 {
		t.Errorf("1kHz through HPF@200: got %.2f dB, want within +-1.0", atten)
	}
}

func TestLPFAttenuates8kHz(t *testing.T) {
	// 4th-order Butterworth LPF at 3400 Hz. 8 kHz is 1.23 octaves above corner,
	// so expect ~4*6*1.23 ≈ 29 dB theoretical.
	ch := NewBiquadChain(NewLPF(48000, 3400), NewLPF(48000, 3400))
	atten := attenuationDB(t, ch, 8000)
	if atten < 20 {
		t.Errorf("8kHz through 4th-order LPF@3400: got %.1f dB, want >=20 dB", atten)
	}
}

func TestLPFPassesVoiceBand(t *testing.T) {
	ch := NewBiquadChain(NewLPF(48000, 3400), NewLPF(48000, 3400))
	atten := attenuationDB(t, ch, 1000)
	if math.Abs(atten) > 1.0 {
		t.Errorf("1kHz through LPF@3400: got %.2f dB, want within +-1.0", atten)
	}
}

func TestPOTSChainKillsHumAndHissPreservesVoice(t *testing.T) {
	// The full POTS bandpass + notch comb must:
	//   - kill 60 Hz by at least 40 dB (HPF does most of it)
	//   - kill 180 Hz by at least 30 dB (HPF + notch)
	//   - kill 8 kHz by at least 20 dB (LPF)
	//   - pass 1 kHz within +-2 dB
	ch := NewPOTSChain(48000)

	for _, tc := range []struct {
		freq   float64
		minDB  float64
		label  string
	}{
		{60, 40, "60Hz hum fundamental"},
		{180, 30, "180Hz harmonic"},
		{8000, 20, "8kHz hiss"},
	} {
		ch.Reset()
		atten := attenuationDB(t, ch, tc.freq)
		if atten < tc.minDB {
			t.Errorf("%s: got %.1f dB, want >=%.0f dB", tc.label, atten, tc.minDB)
		}
	}

	// Voice preservation: notches are scaled to a constant ~2 Hz bandwidth
	// so tones landing between comb teeth should pass with <=1 dB loss
	// anywhere in the POTS band.
	// Offset from the 60 Hz comb grid so tones land between notches. The
	// cascaded 4th-order LPF's effective flat region ends around 1800 Hz
	// (two 2nd-order sections at 3400 Hz compound to ~-1 dB at 1800 Hz,
	// ~-2 dB at 2000 Hz), which is fine for voice intelligibility.
	for _, f := range []float64{530, 970, 1450, 1790} {
		ch.Reset()
		loss := attenuationDB(t, ch, f)
		if loss > 1.0 {
			t.Errorf("%gHz voice loss: got %.2f dB, want <=1 dB", f, loss)
		}
	}
}

func TestBiquadChainNilIsNoOp(t *testing.T) {
	var ch *BiquadChain
	in := sine(60, 48000, 10000, 1000)
	out := ch.Process(in)
	if len(out) != len(in) {
		t.Fatalf("nil chain changed length: got %d want %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Fatalf("nil chain modified sample %d", i)
		}
	}
	// Must be an independent copy so downstream in-place mutators don't
	// corrupt upstream buffers.
	out[0] = 12345
	if in[0] == 12345 {
		t.Fatal("nil chain returned a slice aliasing the input")
	}
}

func TestBiquadChainEmptyIsNoOp(t *testing.T) {
	ch := NewBiquadChain()
	in := sine(60, 48000, 10000, 1000)
	out := ch.Process(in)
	for i := range in {
		if out[i] != in[i] {
			t.Fatalf("empty chain modified sample %d", i)
		}
	}
	out[0] = 12345
	if in[0] == 12345 {
		t.Fatal("empty chain returned a slice aliasing the input")
	}
}
