package household

import (
	"encoding/hex"
	"testing"
)

func TestGenerateInviteToken_ValidHex(t *testing.T) {
	for i := 0; i < 50; i++ {
		tok, err := generateInviteToken()
		if err != nil {
			t.Fatalf("generateInviteToken: %v", err)
		}
		b, err := hex.DecodeString(tok)
		if err != nil {
			t.Fatalf("token %q is not valid hex: %v", tok, err)
		}
		if len(b) != 32 {
			t.Fatalf("expected 32 bytes, got %d", len(b))
		}
	}
}
