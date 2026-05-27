package audio

import "testing"

func TestMaybeMute_ZerosWhenMuted(t *testing.T) {
	frame := []int16{1, 2, 3, 4, 5}
	maybeMute(frame, true)
	for i, s := range frame {
		if s != 0 {
			t.Fatalf("index %d: expected 0, got %d", i, s)
		}
	}
}

func TestMaybeMute_PassthroughWhenUnmuted(t *testing.T) {
	frame := []int16{1, 2, 3, 4, 5}
	maybeMute(frame, false)
	for i, want := range []int16{1, 2, 3, 4, 5} {
		if frame[i] != want {
			t.Fatalf("index %d: expected %d, got %d", i, want, frame[i])
		}
	}
}

func TestPipeline_SetMutedRoundTrip(t *testing.T) {
	p := &Pipeline{}
	if p.Muted() {
		t.Fatalf("expected unmuted by default")
	}
	p.SetMuted(true)
	if !p.Muted() {
		t.Fatalf("expected muted after SetMuted(true)")
	}
	p.SetMuted(false)
	if p.Muted() {
		t.Fatalf("expected unmuted after SetMuted(false)")
	}
}

func TestPipelineConfig(t *testing.T) {
	cfg := DefaultPipelineConfig()
	if cfg.SampleRate != 48000 {
		t.Errorf("SampleRate: got %d, want 48000", cfg.SampleRate)
	}
	if cfg.MicChannel != 0 {
		t.Errorf("MicChannel: got %d, want 0", cfg.MicChannel)
	}
	if !cfg.Denoise {
		t.Error("Denoise: got false, want true")
	}
	if cfg.Bandpass {
		t.Error("Bandpass: got true, want false (RNNoise handles hum)")
	}
	if !cfg.Character {
		t.Error("Character: got false, want true (copper default)")
	}
}

func TestNewPipeline(t *testing.T) {
	p := NewPipeline(DefaultPipelineConfig())
	if p.OutFrames() == nil {
		t.Error("OutFrames() returned nil channel")
	}
}
