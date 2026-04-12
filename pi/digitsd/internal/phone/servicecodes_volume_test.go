package phone

import (
	"testing"

	"github.com/justinlindh/digits/pi/digitsd/internal/audio"
)

func TestVolumeToALSA_Range(t *testing.T) {
	min, max := audio.CodecALSARange()
	if got := volumeToALSA(0); got != min {
		t.Errorf("volumeToALSA(0) = %d, want %d", got, min)
	}
	if got := volumeToALSA(9); got != max {
		t.Errorf("volumeToALSA(9) = %d, want %d", got, max)
	}
}

func TestVolumeToALSA_Default(t *testing.T) {
	min, max := audio.CodecALSARange()
	got := volumeToALSA(5)
	mid := (min + max) / 2
	if got < mid-10 || got > mid+10 {
		t.Errorf("volumeToALSA(5) = %d, want near %d", got, mid)
	}
}

func TestVolumeToALSA_Clamp(t *testing.T) {
	if got := volumeToALSA(-1); got != volumeToALSA(0) {
		t.Errorf("volumeToALSA(-1) = %d, want %d", got, volumeToALSA(0))
	}
	if got := volumeToALSA(10); got != volumeToALSA(9) {
		t.Errorf("volumeToALSA(10) = %d, want %d", got, volumeToALSA(9))
	}
}
