package audio

import (
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// PipelineConfig holds configuration for the audio pipeline.
type PipelineConfig struct {
	SampleRate int
	MicChannel int // 0=left, 1=right. Both codecs route mic to left channel.
	Denoise    bool
	// Bandpass enables the POTS telephony bandpass + mains notch comb on
	// the capture path, applied BEFORE the denoiser. See NewPOTSChain for
	// the exact filter topology.
	Bandpass bool
	// Character enables a pure 300-3400 Hz POTS bandpass applied AFTER the
	// denoiser as a cosmetic voice-color effect that makes calls sound like
	// a legacy copper-wire phone. See NewPOTSCharacterChain.
	Character bool
}

// DefaultPipelineConfig returns sensible defaults for both codec types.
func DefaultPipelineConfig() PipelineConfig {
	return PipelineConfig{
		SampleRate: 48000,
		MicChannel: 0, // Both codecs route mic to left channel
		Denoise:    true,
		// Bandpass off by default: RNNoise alone handles both the 60 Hz
		// mains hum and the broadband preamp hiss on the prototype phones
		// cleanly. A fixed biquad chain in front of RNNoise only distorts
		// voice without improving noise. Leaving the POTS filter code in
		// place so it can be re-enabled for testing or for a future codec
		// without RNNoise.
		Bandpass: false,
		// Character defaults to true (copper voice) because the physical Digits
		// phones are vintage handsets and the POTS color matches their aesthetic.
		// Web UI toggles per line.
		Character: true,
	}
}

// Pipeline ties ALSA capture → RNNoise denoising → outPCM channel.
// Capture-only: playback is handled by the Mixer.
type Pipeline struct {
	cfg       PipelineConfig
	capture   *Capture
	filters   *BiquadChain                // pre-denoise bandpass (optional)
	character atomic.Pointer[BiquadChain] // post-denoise POTS character, swappable live
	denoiser  *Denoiser
	muted     atomic.Bool
	beepBuf   atomic.Pointer[[]int16]
	beepPos   atomic.Int64
	outPCM    chan []int16 // denoised mono 20ms frames for WebRTC to encode
	stop      chan struct{}
	wg        sync.WaitGroup
}

// NewPipeline creates a Pipeline with the given config. Call Start() to open
// ALSA devices and begin streaming.
func NewPipeline(cfg PipelineConfig) *Pipeline {
	p := &Pipeline{
		cfg:    cfg,
		outPCM: make(chan []int16, 8),
		stop:   make(chan struct{}),
	}
	if cfg.Character {
		p.character.Store(NewPOTSCharacterChain(cfg.SampleRate))
	}
	return p
}

// OutFrames returns the read-only channel of captured+denoised mono PCM frames.
func (p *Pipeline) OutFrames() <-chan []int16 {
	return p.outPCM
}

// SetVoiceStyle swaps the post-denoise character filter atomically. Safe to
// call from any goroutine at any time, including mid-call: captureLoop reads
// the pointer each frame so the next frame after the swap picks up the new
// chain. Unknown style values fall back to copper so garbage config can't
// silently disable the effect.
func (p *Pipeline) SetVoiceStyle(style string) {
	switch style {
	case "modern":
		p.character.Store(nil)
	default:
		p.character.Store(NewPOTSCharacterChain(p.cfg.SampleRate))
	}
}

// SetMuted toggles the outbound mic mute. When true, outbound PCM frames are
// replaced with zero-samples. With Opus DTX enabled on the encoder, the
// receiving end renders low-level comfort noise, matching 90s POTS silent
// hold. Safe to call concurrently from any goroutine.
func (p *Pipeline) SetMuted(v bool) { p.muted.Store(v) }

// Muted reports the current mute state.
func (p *Pipeline) Muted() bool { return p.muted.Load() }

// maybeMute zeroes all samples in frame when muted is true.
// In-place mutation is safe here: the frame slice is freshly allocated or
// about to be discarded after the outPCM send.
func maybeMute(frame []int16, muted bool) {
	if !muted {
		return
	}
	for i := range frame {
		frame[i] = 0
	}
}

// SynthGreetingBeep synthesizes a 1kHz sine beep of the given duration at the
// requested sample rate and returns the PCM buffer. A zero-length result means
// the duration rounded to zero samples. Callers that need the beep on the
// outbound capture path use PlayGreetingBeep; callers that need it in the
// earpiece play the buffer through the mixer.
func SynthGreetingBeep(sampleRate int, d time.Duration) []int16 {
	totalSamples := int(d.Seconds() * float64(sampleRate))
	if totalSamples == 0 {
		return nil
	}

	const freq = 1000.0
	const amplitude = 10000.0            // ~30% of int16 max
	fadeSamples := sampleRate * 5 / 1000 // 5ms fade

	buf := make([]int16, totalSamples)
	for i := range buf {
		t := float64(i) / float64(sampleRate)
		sample := amplitude * math.Sin(2*math.Pi*freq*t)

		// Fade envelope.
		if i < fadeSamples {
			sample *= float64(i) / float64(fadeSamples)
		} else if i >= totalSamples-fadeSamples {
			sample *= float64(totalSamples-1-i) / float64(fadeSamples)
		}

		buf[i] = int16(sample)
	}

	return buf
}

// PlayGreetingBeep synthesizes a 1kHz sine beep of the given duration and
// arms it for injection into the capture loop. The beep replaces real mic
// frames for its duration so the caller hears it in the outbound stream.
func (p *Pipeline) PlayGreetingBeep(d time.Duration) {
	buf := SynthGreetingBeep(p.cfg.SampleRate, d)
	if len(buf) == 0 {
		return
	}
	p.beepPos.Store(0)
	p.beepBuf.Store(&buf)
}

// PlayGreetingSamples arms a pre-computed PCM buffer for injection into the
// capture loop. The buffer replaces real mic frames for its duration so the
// caller hears it in the outbound stream. Reuses the beep injection slot;
// calling this while a beep or prior greeting is still draining replaces it.
// A zero-length buffer is a no-op.
func (p *Pipeline) PlayGreetingSamples(samples []int16) {
	if len(samples) == 0 {
		return
	}
	buf := make([]int16, len(samples))
	copy(buf, samples)
	p.beepPos.Store(0)
	p.beepBuf.Store(&buf)
}

// nextBeepFrame returns the next frame of beep audio, or nil if no beep is
// active. The returned slice is always frameSize samples long (zero-padded at
// the end of the buffer).
func (p *Pipeline) nextBeepFrame(frameSize int) []int16 {
	bufPtr := p.beepBuf.Load()
	if bufPtr == nil {
		return nil
	}
	buf := *bufPtr
	pos := int(p.beepPos.Load())
	if pos >= len(buf) {
		p.beepBuf.Store(nil)
		return nil
	}
	end := pos + frameSize
	if end > len(buf) {
		end = len(buf)
	}
	frame := make([]int16, frameSize)
	copy(frame, buf[pos:end])
	p.beepPos.Store(int64(end))
	if end >= len(buf) {
		p.beepBuf.Store(nil)
	}
	return frame
}

// Start opens the ALSA capture device and begins the capture goroutine.
func (p *Pipeline) Start() error {
	cap, err := NewCapture(DefaultCaptureConfig())
	if err != nil {
		return err
	}
	p.capture = cap

	if p.cfg.Bandpass {
		p.filters = NewPOTSChain(p.cfg.SampleRate)
	}

	if p.cfg.Denoise {
		d, err := NewDenoiser()
		if err != nil {
			slog.Warn("audio: denoiser unavailable, running without denoise", "error", err)
			p.denoiser = nil
		} else {
			p.denoiser = d
		}
	}

	p.wg.Add(1)
	go p.captureLoop()

	return nil
}

// Stop shuts down the pipeline goroutines and closes all ALSA/denoiser handles.
func (p *Pipeline) Stop() {
	close(p.stop)
	p.wg.Wait()

	// Safe to close now: captureLoop is the sole sender on outPCM and it has
	// returned (it is registered in p.wg, and p.wg.Wait above blocks until it
	// exits). Closing unblocks consumers that range over OutFrames so they do
	// not leak a goroutine per completed call.
	close(p.outPCM)

	if p.capture != nil {
		p.capture.Close()
		p.capture = nil
	}
	if p.denoiser != nil {
		p.denoiser.Close()
		p.denoiser = nil
	}
}

func (p *Pipeline) captureLoop() {
	defer p.wg.Done()
	for {
		// Check for stop (non-blocking).
		select {
		case <-p.stop:
			return
		default:
		}

		stereo, err := p.capture.ReadFrame()
		if err != nil {
			slog.Error("audio: capture read error", "error", err)
			continue
		}

		// Beep injection: if a greeting beep is active, replace mic data with
		// synthesized tone and discard the real capture.
		if beepFrame := p.nextBeepFrame(len(stereo) / 2); beepFrame != nil {
			select {
			case p.outPCM <- beepFrame:
			default:
			}
			continue
		}

		mono := ExtractChannel(stereo, 2, p.cfg.MicChannel)

		// On the default config the bandpass is off and p.filters is nil; skip
		// the call so we do not allocate + copy a fresh buffer 50x/sec to no-op.
		// ExtractChannel already returns a fresh slice, so the in-place denoiser
		// below is safe operating on it directly.
		if p.filters != nil {
			mono = p.filters.Process(mono)
		}

		if p.denoiser != nil {
			mono = p.denoiser.Process(mono)
		}

		if ch := p.character.Load(); ch != nil {
			mono = ch.Process(mono)
		}

		maybeMute(mono, p.muted.Load())

		// Non-blocking send — drop frame if consumer is slow.
		select {
		case p.outPCM <- mono:
		default:
		}
	}
}
