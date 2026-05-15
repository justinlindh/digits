package main

import (
	"slices"
	"testing"
)

func TestMessageNumberClips(t *testing.T) {
	tests := []struct {
		name   string
		number int
		want   []string
	}{
		{"zero yields no clips", 0, nil},
		{"negative yields no clips", -1, nil},
		{"first message", 1, []string{"vm_message", "spoken_1"}},
		{"single digit", 7, []string{"vm_message", "spoken_7"}},
		{"highest digit clip", 9, []string{"vm_message", "spoken_9"}},
		{"above nine falls back to bare word", 10, []string{"vm_message"}},
		{"well above nine falls back to bare word", 42, []string{"vm_message"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := messageNumberClips(tt.number)
			if !slices.Equal(got, tt.want) {
				t.Errorf("messageNumberClips(%d) = %v, want %v", tt.number, got, tt.want)
			}
		})
	}
}
