package tokens

import (
	"encoding/hex"
	"testing"
)

func TestHash(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"hello", "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"},
		{"", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
	}
	for _, tc := range cases {
		got := Hash(tc.input)
		if got != tc.want {
			t.Errorf("Hash(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestHash_HexLength(t *testing.T) {
	got := Hash("any-token")
	if len(got) != 64 {
		t.Errorf("Hash length = %d, want 64", len(got))
	}
	if _, err := hex.DecodeString(got); err != nil {
		t.Errorf("Hash returned non-hex output: %v", err)
	}
}

func TestRandomHex_Length(t *testing.T) {
	for _, n := range []int{1, 16, 32, 64} {
		got, err := RandomHex(n)
		if err != nil {
			t.Fatalf("RandomHex(%d): %v", n, err)
		}
		if len(got) != n*2 {
			t.Errorf("RandomHex(%d): got length %d, want %d", n, len(got), n*2)
		}
		if _, err := hex.DecodeString(got); err != nil {
			t.Errorf("RandomHex(%d): not valid hex: %v", n, err)
		}
	}
}

func TestRandomHex_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		got, err := RandomHex(16)
		if err != nil {
			t.Fatalf("RandomHex: %v", err)
		}
		if seen[got] {
			t.Fatalf("RandomHex produced a duplicate value: %q", got)
		}
		seen[got] = true
	}
}
