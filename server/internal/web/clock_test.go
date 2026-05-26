package web

import (
	"testing"
	"time"
)

func TestClockParts(t *testing.T) {
	tests := []struct {
		in   int
		h, m, s int
	}{
		{0, 0, 0, 0},
		{-1, 0, 0, 0},
		{-3661, 0, 0, 0},
		{59, 0, 0, 59},
		{60, 0, 1, 0},
		{3599, 0, 59, 59},
		{3600, 1, 0, 0},
		{3661, 1, 1, 1},
		{7322, 2, 2, 2},
	}
	for _, tt := range tests {
		h, m, s := clockParts(tt.in)
		if h != tt.h || m != tt.m || s != tt.s {
			t.Errorf("clockParts(%d) = (%d,%d,%d), want (%d,%d,%d)", tt.in, h, m, s, tt.h, tt.m, tt.s)
		}
	}
}

func TestFmtElapsed(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{-time.Second, "0:00"},
		{0, "0:00"},
		{59 * time.Second, "0:59"},
		{60 * time.Second, "1:00"},
		{90 * time.Second, "1:30"},
		{3599 * time.Second, "59:59"},
		{3600 * time.Second, "1:00:00"},
		{3661 * time.Second, "1:01:01"},
	}
	for _, tt := range tests {
		got := fmtElapsed(tt.d)
		if got != tt.want {
			t.Errorf("fmtElapsed(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestFmtDurationClock(t *testing.T) {
	funcs := baseTemplateFuncs()
	fn := funcs["fmtDurationClock"].(func(int) string)

	tests := []struct {
		in   int
		want string
	}{
		{-1, "00:00"},
		{0, "00:00"},
		{59, "00:59"},
		{60, "01:00"},
		{3599, "59:59"},
		{3600, "1:00:00"},
		{3661, "1:01:01"},
	}
	for _, tt := range tests {
		got := fn(tt.in)
		if got != tt.want {
			t.Errorf("fmtDurationClock(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
