package auth

import "testing"

func TestAppearance_Valid(t *testing.T) {
	tests := []struct {
		appearance Appearance
		want       bool
	}{
		{AppearanceDay, true},
		{AppearanceNight, true},
		{Appearance(""), false},
		{Appearance("unknown"), false},
		{Appearance("DAY"), false},
	}
	for _, tc := range tests {
		if got := tc.appearance.Valid(); got != tc.want {
			t.Errorf("Appearance(%q).Valid() = %v, want %v", tc.appearance, got, tc.want)
		}
	}
}
