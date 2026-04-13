package audio

import "testing"

func TestPipelineConfig(t *testing.T) {
	cfg := DefaultPipelineConfig()
	if cfg.SampleRate != 48000 {
		t.Errorf("SampleRate: got %d, want 48000", cfg.SampleRate)
	}
	if cfg.FrameMs != 20 {
		t.Errorf("FrameMs: got %d, want 20", cfg.FrameMs)
	}
	if cfg.OpusBitrate != 24000 {
		t.Errorf("OpusBitrate: got %d, want 24000", cfg.OpusBitrate)
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
}

func TestNewPipeline(t *testing.T) {
	p := NewPipeline(DefaultPipelineConfig())
	if p.OutFrames() == nil {
		t.Error("OutFrames() returned nil channel")
	}
}
