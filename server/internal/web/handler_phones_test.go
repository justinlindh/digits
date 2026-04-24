package web

import (
	"testing"

	"github.com/justinlindh/digits/server/internal/line"
)

func TestEffectiveSilent(t *testing.T) {
	cases := []struct {
		name         string
		linesSilent  bool
		householdDND bool
		want         bool
	}{
		{"both off", false, false, false},
		{"line silent only", true, false, true},
		{"household DND only", false, true, true},
		{"both on", true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := effectiveSilent(line.Settings{SilentMode: tc.linesSilent}, tc.householdDND)
			if got != tc.want {
				t.Errorf("effectiveSilent(silent=%v, dnd=%v) = %v, want %v",
					tc.linesSilent, tc.householdDND, got, tc.want)
			}
		})
	}
}
