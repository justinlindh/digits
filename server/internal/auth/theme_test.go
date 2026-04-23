package auth

import "testing"

func TestThemeValid(t *testing.T) {
	cases := []struct {
		in   Theme
		want bool
	}{
		{ThemeIntercom, true},
		{ThemeDialup, true},
		{ThemeAnsweringMachine, true},
		{Theme(""), false},
		{Theme("ledger"), false},
	}
	for _, c := range cases {
		if got := c.in.Valid(); got != c.want {
			t.Errorf("Theme(%q).Valid() = %v, want %v", string(c.in), got, c.want)
		}
	}
}
