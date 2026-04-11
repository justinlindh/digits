package phone

import "testing"

func TestVolumeToALSA_Range(t *testing.T) {
	if got := volumeToALSA(0); got != 40 {
		t.Errorf("volumeToALSA(0) = %d, want 40", got)
	}
	if got := volumeToALSA(9); got != 115 {
		t.Errorf("volumeToALSA(9) = %d, want 115", got)
	}
}

func TestVolumeToALSA_Default(t *testing.T) {
	got := volumeToALSA(5)
	if got < 70 || got > 90 {
		t.Errorf("volumeToALSA(5) = %d, want between 70-90", got)
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
