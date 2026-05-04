package phonekit

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectCodecFromMarker(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "codec-device")
	if err := os.WriteFile(markerPath, []byte("digits_playback"), 0o644); err != nil {
		t.Fatal(err)
	}

	ap := &audioPlayer{
		codecMarkerPath: markerPath,
		probeFunc:       func(string) bool { return false },
	}

	device, rate := ap.detectCodec()
	if device != "digits_playback" {
		t.Errorf("device: got %q, want %q", device, "digits_playback")
	}
	if rate != 44100 {
		t.Errorf("rate: got %d, want 44100", rate)
	}
}

func TestDetectCodecFallbackV2(t *testing.T) {
	ap := &audioPlayer{
		codecMarkerPath: "/nonexistent/path/codec-device",
		probeFunc: func(card string) bool {
			return card == "digitscodec"
		},
	}

	device, rate := ap.detectCodec()
	if device != "hw:CARD=digitscodec,DEV=0" {
		t.Errorf("device: got %q, want %q", device, "hw:CARD=digitscodec,DEV=0")
	}
	if rate != 44100 {
		t.Errorf("rate: got %d, want 44100", rate)
	}
}

func TestDetectCodecFallbackV1(t *testing.T) {
	ap := &audioPlayer{
		codecMarkerPath: "/nonexistent/path/codec-device",
		probeFunc: func(card string) bool {
			return card == "Zero"
		},
	}

	device, rate := ap.detectCodec()
	if device != "plughw:CARD=Zero,DEV=0" {
		t.Errorf("device: got %q, want %q", device, "plughw:CARD=Zero,DEV=0")
	}
	if rate != 48000 {
		t.Errorf("rate: got %d, want 48000", rate)
	}
}

func TestDetectCodecDefault(t *testing.T) {
	ap := &audioPlayer{
		codecMarkerPath: "/nonexistent/path/codec-device",
		probeFunc:       func(string) bool { return false },
	}

	device, rate := ap.detectCodec()
	if device != "default" {
		t.Errorf("device: got %q, want %q", device, "default")
	}
	if rate != 48000 {
		t.Errorf("rate: got %d, want 48000", rate)
	}
}

func TestPlayContextCancel(t *testing.T) {
	ap := &audioPlayer{
		codecMarkerPath: "/nonexistent/path/codec-device",
		aplayPath:       "aplay",
		probeFunc:       func(string) bool { return false },
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := ap.Play(ctx, []byte("fake wav data"))
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}
