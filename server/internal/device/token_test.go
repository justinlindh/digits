package device

import (
	"encoding/hex"
	"testing"
)

func TestHashToken(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"hello", "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"},
		{"", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
	}
	for _, tc := range cases {
		got := HashToken(tc.input)
		if got != tc.want {
			t.Errorf("HashToken(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestHashToken_HexLength(t *testing.T) {
	got := HashToken("any-token")
	if len(got) != 64 {
		t.Errorf("HashToken length = %d, want 64", len(got))
	}
	if _, err := hex.DecodeString(got); err != nil {
		t.Errorf("HashToken returned non-hex output: %v", err)
	}
}
