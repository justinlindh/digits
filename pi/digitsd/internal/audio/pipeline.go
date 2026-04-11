package audio

import (
	"log/slog"
	"sync"
)

// PipelineConfig holds configuration for the audio pipeline.
type PipelineConfig struct {
	SampleRate  int
	FrameMs     int
	OpusBitrate int
	MicChannel  int  // 0=left, 1=right. TLV320AIC3104: mic on MIC1LP (left channel).
	Denoise     bool
}

// DefaultPipelineConfig returns sensible defaults for the TLV320AIC3104 codec setup.
func DefaultPipelineConfig() PipelineConfig {
	return PipelineConfig{
		SampleRate:  48000,
		FrameMs:     20,
		OpusBitrate: 24000,
		MicChannel:  0, // Mic on MIC1LP, captured as left channel
		Denoise:     true,
	}
}

// Pipeline ties ALSA capture → RNNoise denoising → outPCM channel.
// Capture-only: playback is handled by the Mixer.
type Pipeline struct {
	cfg      PipelineConfig
	capture  *Capture
	denoiser *Denoiser
	outPCM   chan []int16 // denoised mono 20ms frames for WebRTC to encode
	stop     chan struct{}
	wg       sync.WaitGroup
}

// NewPipeline creates a Pipeline with the given config. Call Start() to open
// ALSA devices and begin streaming.
func NewPipeline(cfg PipelineConfig) *Pipeline {
	return &Pipeline{
		cfg:    cfg,
		outPCM: make(chan []int16, 8),
		stop:   make(chan struct{}),
	}
}

// OutFrames returns the read-only channel of captured+denoised mono PCM frames.
func (p *Pipeline) OutFrames() <-chan []int16 {
	return p.outPCM
}

// Start opens the ALSA capture device and begins the capture goroutine.
func (p *Pipeline) Start() error {
	cap, err := NewCapture(DefaultCaptureConfig())
	if err != nil {
		return err
	}
	p.capture = cap

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

		if p.denoiser != nil {
			mono = p.denoiser.Process(mono)
		}

		// Non-blocking send — drop frame if consumer is slow.
		select {
		case p.outPCM <- mono:
		default:
		}
	}
}
