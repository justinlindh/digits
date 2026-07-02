package audio

import (
	"testing"
	"time"
)

// TestPipeline_StopClosesOutFrames locks in the contract that Stop closes the
// output channel so a `for range OutFrames()` consumer drains buffered frames
// and then exits, instead of blocking forever and leaking its goroutine.
func TestPipeline_StopClosesOutFrames(t *testing.T) {
	p := NewPipeline(PipelineConfig{})

	// Frames a consumer would still see buffered when a call ends.
	p.outPCM <- []int16{1, 2, 3}
	p.outPCM <- []int16{4, 5, 6}

	p.Stop()

	done := make(chan int, 1)
	go func() {
		n := 0
		for range p.OutFrames() {
			n++
		}
		done <- n
	}()

	select {
	case n := <-done:
		if n != 2 {
			t.Fatalf("drained %d buffered frames, want 2", n)
		}
	case <-time.After(time.Second):
		t.Fatal("range over OutFrames did not terminate: Stop left the channel open")
	}
}

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
