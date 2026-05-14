package audio

import (
	"testing"
	"time"
)

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
