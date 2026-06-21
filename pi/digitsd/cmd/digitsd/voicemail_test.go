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

func TestCountPhraseClips(t *testing.T) {
	const singular, plural = "vm_saved_message", "vm_saved_messages"
	tests := []struct {
		name  string
		count int
		want  []string
	}{
		{"zero yields no clips", 0, nil},
		{"negative yields no clips", -1, nil},
		{"one message is singular", 1, []string{"vm_you_have", "spoken_1", singular}},
		{"several messages are plural", 4, []string{"vm_you_have", "spoken_4", plural}},
		{"highest digit clip", 9, []string{"vm_you_have", "spoken_9", plural}},
		{"above nine caps at 9", 10, []string{"vm_you_have", "spoken_9", plural}},
		{"well above nine caps at 9", 50, []string{"vm_you_have", "spoken_9", plural}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countPhraseClips(tt.count, singular, plural)
			if !slices.Equal(got, tt.want) {
				t.Errorf("countPhraseClips(%d) = %v, want %v", tt.count, got, tt.want)
			}
		})
	}
}
