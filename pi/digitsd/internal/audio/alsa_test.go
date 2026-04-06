package audio

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestFindCodecCard(t *testing.T) {
	// Only run on devices that have the Codec Zero
	f, err := os.ReadFile("/proc/asound/cards")
	if err != nil || !strings.Contains(string(f), "[Zero") {
		t.Skip("Codec Zero not present, skipping")
	}

	card, err := FindCodecCard()
	if err != nil {
		t.Fatalf("FindCodecCard() error: %v", err)
	}
	if card < 0 {
		t.Errorf("FindCodecCard() = %d, want >= 0", card)
	}
}

func TestCodecDeviceName(t *testing.T) {
	f, err := os.ReadFile("/proc/asound/cards")
	if err != nil || !strings.Contains(string(f), "[Zero") {
		t.Skip("Codec Zero not present, skipping")
	}

	dev, err := CodecDeviceName()
	if err != nil {
		t.Fatalf("CodecDeviceName() error: %v", err)
	}
	if !strings.HasPrefix(dev, "plughw:") {
		t.Errorf("CodecDeviceName() = %q, want plughw:N,0 format", dev)
	}
}

func TestDefaultCaptureConfig(t *testing.T) {
	cfg := DefaultCaptureConfig()
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
	// Stereo interleaved: [L0,R0, L1,R1, L2,R2]
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
