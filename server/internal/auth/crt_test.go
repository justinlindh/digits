package auth

import "testing"

func TestCRTMode_Valid(t *testing.T) {
	tests := []struct {
		mode CRTMode
		want bool
	}{
		{CRTModeOff, true},
		{CRTModeConnecting, true},
		{CRTModeAll, true},
		{CRTMode(""), false},
		{CRTMode("unknown"), false},
		{CRTMode("ALL"), false},
	}
	for _, tc := range tests {
		if got := tc.mode.Valid(); got != tc.want {
			t.Errorf("CRTMode(%q).Valid() = %v, want %v", tc.mode, got, tc.want)
		}
	}
}
