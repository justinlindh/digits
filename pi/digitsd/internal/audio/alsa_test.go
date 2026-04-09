package audio

import (
	"reflect"
	"testing"
)

func TestDefaultCaptureConfig(t *testing.T) {
	cfg := DefaultCaptureConfig()
	if cfg.Device != CodecDeviceName {
		t.Errorf("Device = %q, want %q", cfg.Device, CodecDeviceName)
	}
	if cfg.SampleRate != 48000 {
		t.Errorf("SampleRate = %d, want 48000", cfg.SampleRate)
	}
	if cfg.Channels != 2 {
		t.Errorf("Channels = %d, want 2 (stereo)", cfg.Channels)
	}
	if cfg.FrameSize != 960 {
		t.Errorf("FrameSize = %d, want 960", cfg.FrameSize)
	}
}

func TestDefaultPlaybackConfig(t *testing.T) {
	cfg := DefaultPlaybackConfig()
	if cfg.Device != "default" {
		t.Errorf("Device = %q, want %q", cfg.Device, "default")
	}
	if cfg.SampleRate != 48000 {
		t.Errorf("SampleRate = %d, want 48000", cfg.SampleRate)
	}
	if cfg.Channels != 1 {
		t.Errorf("Channels = %d, want 1 (mono)", cfg.Channels)
	}
	if cfg.FrameSize != 960 {
		t.Errorf("FrameSize = %d, want 960", cfg.FrameSize)
	}
}

func TestExtractChannel_Right(t *testing.T) {
	interleaved := []int16{100, 200, 300, 400, 500, 600}
	got := ExtractChannel(interleaved, 2, 1)
	want := []int16{200, 400, 600}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtractChannel right = %v, want %v", got, want)
	}
}

func TestExtractChannel_Left(t *testing.T) {
	interleaved := []int16{100, 200, 300, 400, 500, 600}
	got := ExtractChannel(interleaved, 2, 0)
	want := []int16{100, 300, 500}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtractChannel left = %v, want %v", got, want)
	}
}

func TestExtractChannel_Empty(t *testing.T) {
	got := ExtractChannel([]int16{}, 2, 0)
	if len(got) != 0 {
		t.Errorf("ExtractChannel empty = %v, want empty slice", got)
	}
}
