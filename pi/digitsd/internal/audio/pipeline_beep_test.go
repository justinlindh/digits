package audio

import (
	"testing"
	"time"
)

// drainBeepFrames consumes all remaining beep frames of the given size.
func (p *Pipeline) drainBeepFrames(frameSize int) [][]int16 {
	var frames [][]int16
	for {
		frame := p.nextBeepFrame(frameSize)
		if frame == nil {
			break
		}
		frames = append(frames, frame)
	}
	return frames
}

func TestPlayGreetingBeep_ReplacesFrames(t *testing.T) {
	cfg := DefaultPipelineConfig()
	p := NewPipeline(cfg)

	frameSize := cfg.SampleRate * cfg.FrameMs / 1000 // 960

	p.PlayGreetingBeep(40 * time.Millisecond) // 2 frames at 20ms each

	frames := p.drainBeepFrames(frameSize)
	if len(frames) != 2 {
		t.Fatalf("expected 2 beep frames, got %d", len(frames))
	}

	hasNonZero := false
	for _, s := range frames[0] {
		if s != 0 {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		t.Error("first beep frame is all zeros")
	}

	extra := p.drainBeepFrames(frameSize)
	if len(extra) != 0 {
		t.Errorf("expected 0 frames after beep exhausted, got %d", len(extra))
	}
}

func TestPlayGreetingBeep_FadeEnvelope(t *testing.T) {
	cfg := DefaultPipelineConfig()
	p := NewPipeline(cfg)

	p.PlayGreetingBeep(100 * time.Millisecond) // 5 frames

	frameSize := cfg.SampleRate * cfg.FrameMs / 1000
	frames := p.drainBeepFrames(frameSize)
	if len(frames) == 0 {
		t.Fatal("no beep frames")
	}

	if abs16(frames[0][0]) > 500 {
		t.Errorf("expected fade-in: first sample = %d, want near 0", frames[0][0])
	}

	lastFrame := frames[len(frames)-1]
	lastSample := lastFrame[len(lastFrame)-1]
	if abs16(lastSample) > 500 {
		t.Errorf("expected fade-out: last sample = %d, want near 0", lastSample)
	}
}

func abs16(v int16) int16 {
	if v < 0 {
		return -v
	}
	return v
}

func TestPlayGreetingSamples_ReplacesFrames(t *testing.T) {
	cfg := DefaultPipelineConfig()
	p := NewPipeline(cfg)

	frameSize := cfg.SampleRate * cfg.FrameMs / 1000 // 960

	// Two frames of distinctive samples.
	samples := make([]int16, frameSize*2)
	for i := range samples {
		samples[i] = int16(i % 1000)
	}

	p.PlayGreetingSamples(samples)

	frames := p.drainBeepFrames(frameSize)
	if len(frames) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(frames))
	}

	for i := 0; i < frameSize; i++ {
		if frames[0][i] != samples[i] {
			t.Errorf("frame[0][%d] = %d, want %d", i, frames[0][i], samples[i])
			break
		}
	}
	for i := 0; i < frameSize; i++ {
		if frames[1][i] != samples[frameSize+i] {
			t.Errorf("frame[1][%d] = %d, want %d", i, frames[1][i], samples[frameSize+i])
			break
		}
	}

	extra := p.drainBeepFrames(frameSize)
	if len(extra) != 0 {
		t.Errorf("expected 0 frames after greeting exhausted, got %d", len(extra))
	}
}

func TestPlayGreetingSamples_EmptyIsNoop(t *testing.T) {
	cfg := DefaultPipelineConfig()
	p := NewPipeline(cfg)

	p.PlayGreetingSamples(nil)
	p.PlayGreetingSamples([]int16{})

	frameSize := cfg.SampleRate * cfg.FrameMs / 1000
	frames := p.drainBeepFrames(frameSize)
	if len(frames) != 0 {
		t.Errorf("expected 0 frames after no-op call, got %d", len(frames))
	}
}
