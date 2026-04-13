package audio

import (
	"log/slog"
	"sync"
	"sync/atomic"
)

// PipelineConfig holds configuration for the audio pipeline.
type PipelineConfig struct {
	SampleRate  int
	FrameMs     int
	OpusBitrate int
	MicChannel  int  // 0=left, 1=right. Both codecs route mic to left channel.
	Denoise     bool
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
		SampleRate:  48000,
		FrameMs:     20,
		OpusBitrate: 24000,
		MicChannel:  0, // Both codecs route mic to left channel
		Denoise:     true,
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
	filters   *BiquadChain // pre-denoise bandpass (optional)
	character atomic.Pointer[BiquadChain] // post-denoise POTS character, swappable live
	denoiser  *Denoiser
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

// Voice style identifiers accepted by SetVoiceStyle. Kept here rather than in
// the signal package so the audio package has no upstream dependency.
const (
	VoiceStyleCopper = "copper"
	VoiceStyleModern = "modern"
)

// SetVoiceStyle swaps the post-denoise character filter atomically. Safe to
// call from any goroutine at any time, including mid-call: captureLoop reads
// the pointer each frame so the next frame after the swap picks up the new
// chain. Unknown style values fall back to copper so garbage config can't
// silently disable the effect.
func (p *Pipeline) SetVoiceStyle(style string) {
	switch style {
	case VoiceStyleModern:
		p.character.Store(nil)
	default:
		p.character.Store(NewPOTSCharacterChain(p.cfg.SampleRate))
	}
}

// loadCharacter is a test helper. Returns the current character chain (may
// be nil).
func (p *Pipeline) loadCharacter() *BiquadChain {
	return p.character.Load()
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

		mono := ExtractChannel(stereo, 2, p.cfg.MicChannel)

		mono = p.filters.Process(mono)

		if p.denoiser != nil {
			mono = p.denoiser.Process(mono)
		}

		if ch := p.character.Load(); ch != nil {
			mono = ch.Process(mono)
		}

		// Non-blocking send — drop frame if consumer is slow.
		select {
		case p.outPCM <- mono:
		default:
		}
	}
}
