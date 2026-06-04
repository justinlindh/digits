package pairing

import (
	"encoding/hex"
	"testing"
)

func TestRandomCode_Format(t *testing.T) {
	code, err := randomCode(6)
	if err != nil {
		t.Fatalf("randomCode: %v", err)
	}
	if len(code) != 6 {
		t.Errorf("expected length 6, got %d: %q", len(code), code)
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			t.Errorf("non-digit character in code: %q", code)
			break
		}
	}
}

func TestRandomHex_Length(t *testing.T) {
	for _, n := range []int{1, 16, 32, 64} {
		got, err := randomHex(n)
		if err != nil {
			t.Fatalf("randomHex(%d): %v", n, err)
		}
		if len(got) != n*2 {
			t.Errorf("randomHex(%d): got length %d, want %d", n, len(got), n*2)
		}
		if _, err := hex.DecodeString(got); err != nil {
			t.Errorf("randomHex(%d): not valid hex: %v", n, err)
		}
	}
}
