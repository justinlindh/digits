package main

import "testing"

func TestHookFlashCapable(t *testing.T) {
	cases := []struct {
		ver  string
		want bool
	}{
		{"1.4.0", false},    // pre-flash
		{"1.4.9", false},    // still pre-flash
		{"1.5.0", true},     // first version with flash
		{"1.5.1", true},     // patch
		{"1.6.0", true},     // minor
		{"2.0.0", true},     // major
		{"", false},         // unknown
		{"dev", false},      // non-semver
		{"invalid", false},  // non-semver
	}
	for _, c := range cases {
		got := hookFlashCapable(c.ver)
		if got != c.want {
			t.Errorf("hookFlashCapable(%q) = %v, want %v", c.ver, got, c.want)
		}
	}
}
