package audio

import (
	"testing"
)

func TestRNNoiseFrameSize(t *testing.T) {
	if RNNoiseFrameSize != 480 {
		t.Errorf("expected RNNoiseFrameSize=480, got %d", RNNoiseFrameSize)
	}
}

func TestInt16ToFloat32(t *testing.T) {
	in := []int16{0, 16384, -16384, 32767}
	want := []float32{0.0, 16384.0, -16384.0, 32767.0}
	got := int16ToFloat32(in)
	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestFloat32ToInt16(t *testing.T) {
	in := []float32{0.0, 16384.0, -16384.0, 40000.0}
	want := []int16{0, 16384, -16384, 32767}
	got := float32ToInt16(in)
	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestFloat32ToInt16_NegativeClamp(t *testing.T) {
	in := []float32{-40000.0}
	want := []int16{-32768}
	got := float32ToInt16(in)
	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %d, want %d", len(got), len(want))
	}
	if got[0] != want[0] {
		t.Errorf("got %v, want %v", got[0], want[0])
	}
}

func TestDenoiseSilence(t *testing.T) {
	d, err := NewDenoiser()
	if err != nil {
		t.Skipf("skipping: NewDenoiser failed (likely aarch64 lib on x86_64): %v", err)
	}
	defer d.Close()

	// 960 samples of silence (20ms at 48kHz)
	silence := make([]int16, 960)
	out := d.Process(silence)

	if len(out) != 960 {
		t.Fatalf("expected output length 960, got %d", len(out))
	}

	// RNNoise on silence should produce near-silence
	const threshold = int16(500)
	for i, s := range out {
		if s > threshold || s < -threshold {
			t.Errorf("sample[%d]=%d exceeds silence threshold ±%d", i, s, threshold)
		}
	}
}
