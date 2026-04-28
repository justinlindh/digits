package household

import "testing"

func TestGenerateInviteCode_UppercaseAlphanumericOnly(t *testing.T) {
	for i := 0; i < 200; i++ {
		code, err := generateInviteCode()
		if err != nil {
			t.Fatalf("generateInviteCode: %v", err)
		}
		if len(code) != inviteCodeLength {
			t.Errorf("expected length %d, got %d (%q)", inviteCodeLength, len(code), code)
		}
		for _, r := range code {
			if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
				t.Fatalf("code %q contains non-uppercase-alphanumeric rune %q (iter %d)", code, r, i)
			}
		}
	}
}
