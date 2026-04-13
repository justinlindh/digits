package audio

import "math"

// Biquad is a second-order IIR filter section. Coefficients are computed once
// at construction from one of the factory functions below; Process runs the
// direct-form I recurrence on int16 PCM, keeping state in float64 to avoid
// truncation inside the feedback path.
type Biquad struct {
	b0, b1, b2 float64
	a1, a2     float64
	x1, x2     float64
	y1, y2     float64
}

// newBiquad normalizes raw RBJ coefficients by a0 and returns a ready section.
func newBiquad(b0, b1, b2, a0, a1, a2 float64) *Biquad {
	return &Biquad{
		b0: b0 / a0,
		b1: b1 / a0,
		b2: b2 / a0,
		a1: a1 / a0,
		a2: a2 / a0,
	}
}

// NewNotch builds a band-stop biquad centered at f0 with quality factor Q.
// RBJ audio-EQ cookbook formulas.
func NewNotch(sampleRate int, f0, q float64) *Biquad {
	w0 := 2 * math.Pi * f0 / float64(sampleRate)
	cosW := math.Cos(w0)
	alpha := math.Sin(w0) / (2 * q)
	return newBiquad(
		1, -2*cosW, 1,
		1+alpha, -2*cosW, 1-alpha,
	)
}

// NewHPF builds a 2nd-order Butterworth high-pass biquad at f0.
// Cascade two of them for a 4th-order response (Linkwitz-Riley style).
func NewHPF(sampleRate int, f0 float64) *Biquad {
	const q = math.Sqrt2 / 2 // Butterworth
	w0 := 2 * math.Pi * f0 / float64(sampleRate)
	cosW := math.Cos(w0)
	alpha := math.Sin(w0) / (2 * q)
	return newBiquad(
		(1+cosW)/2, -(1+cosW), (1+cosW)/2,
		1+alpha, -2*cosW, 1-alpha,
	)
}

// NewLPF builds a 2nd-order Butterworth low-pass biquad at f0.
func NewLPF(sampleRate int, f0 float64) *Biquad {
	const q = math.Sqrt2 / 2
	w0 := 2 * math.Pi * f0 / float64(sampleRate)
	cosW := math.Cos(w0)
	alpha := math.Sin(w0) / (2 * q)
	return newBiquad(
		(1-cosW)/2, 1-cosW, (1-cosW)/2,
		1+alpha, -2*cosW, 1-alpha,
	)
}

// Reset clears the delay line.
func (f *Biquad) Reset() { f.x1, f.x2, f.y1, f.y2 = 0, 0, 0, 0 }

// process runs one sample through the direct-form I difference equation.
func (f *Biquad) process(x float64) float64 {
	y := f.b0*x + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2
	f.x2 = f.x1
	f.x1 = x
	f.y2 = f.y1
	f.y1 = y
	return y
}

// BiquadChain cascades several Biquad sections and runs a PCM buffer through
// the cascade sample-by-sample. A nil chain is a safe no-op so callers can
// disable filtering by passing nil without branching.
type BiquadChain struct {
	stages []*Biquad
}

// NewBiquadChain builds a chain from a variadic list of sections.
func NewBiquadChain(stages ...*Biquad) *BiquadChain {
	return &BiquadChain{stages: append([]*Biquad(nil), stages...)}
}

// Reset clears state on every stage.
func (c *BiquadChain) Reset() {
	if c == nil {
		return
	}
	for _, s := range c.stages {
		s.Reset()
	}
}

// Process runs the cascade over the input and returns a new slice of the same
// length. The returned slice is always independent of the input so callers
// (or downstream stages like RNNoise that mutate their buffer in place) don't
// have to reason about aliasing. A nil or empty chain still copies.
func (c *BiquadChain) Process(in []int16) []int16 {
	out := make([]int16, len(in))
	if c == nil || len(c.stages) == 0 {
		copy(out, in)
		return out
	}
	for i, s := range in {
		v := float64(s)
		for _, stage := range c.stages {
			v = stage.process(v)
		}
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		out[i] = int16(v)
	}
	return out
}

// NewPOTSChain builds the POTS-telephony bandpass + mains notch comb that was
// validated in March on the prototype phones:
//
//   - 200 Hz 4th-order Butterworth high-pass (two cascaded 2nd-order sections)
//   - 3400 Hz 4th-order Butterworth low-pass
//   - 60 Hz notch comb at every harmonic from 60 Hz to 3400 Hz
//
// The HPF does the heavy lifting against 60 Hz mains hum (roughly 40 dB at
// the fundamental), the LPF removes hiss above the telephony band, and the
// notch comb surgically kills in-band harmonics without touching voice
// formants.
//
// Each notch uses Q = f/2 so its -3 dB bandwidth is a constant ~2 Hz
// regardless of frequency. North American mains is stable to well under
// 0.1 Hz, so 2 Hz is plenty to catch harmonic drift, and at that width the
// notches never overlap their neighbors on the comb grid. (A constant Q=30
// as the earlier Python prototype used would over-widen the high-frequency
// notches until they overlapped and took ~11 dB out of 2 kHz speech.)
func NewPOTSChain(sampleRate int) *BiquadChain {
	stages := make([]*Biquad, 0, 64)
	stages = append(stages, NewHPF(sampleRate, 200), NewHPF(sampleRate, 200))
	stages = append(stages, NewLPF(sampleRate, 3400), NewLPF(sampleRate, 3400))
	for f := 60.0; f <= 3400; f += 60 {
		stages = append(stages, NewNotch(sampleRate, f, f/2))
	}
	return NewBiquadChain(stages...)
}
