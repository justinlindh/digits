package audio

import (
	"reflect"
	"strings"
	"testing"
)

func TestFindCodecCardIn(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{
			name:  "card 0",
			input: " 0 [Zero           ]: RPi_Codec_Zero - RPi Codec Zero\n                      RPi Codec Zero\n",
			want:  0,
		},
		{
			name:  "card 1 with HDMI first",
			input: " 0 [vc4hdmi        ]: vc4-hdmi - vc4-hdmi\n                      vc4-hdmi\n 1 [Zero           ]: RPi_Codec_Zero - RPi Codec Zero\n                      RPi Codec Zero\n",
			want:  1,
		},
		{
			name:  "card 2",
			input: " 0 [foo            ]: bar\n 1 [baz            ]: qux\n 2 [Zero           ]: RPi_Codec_Zero - RPi Codec Zero\n",
			want:  2,
		},
		{
			name:    "no codec zero",
			input:   " 0 [vc4hdmi        ]: vc4-hdmi - vc4-hdmi\n",
			wantErr: true,
		},
		{
			name:    "empty",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := findCodecCardIn(strings.NewReader(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got card %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got card %d, want %d", got, tt.want)
			}
		})
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
