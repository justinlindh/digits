package turn

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestGenerateCredentials(t *testing.T) {
	gen := NewCredentialGenerator("supersecret", 24*time.Hour)
	creds := gen.Generate("user@digits")

	// Username encodes the expiry as the first colon-separated field so
	// coturn can parse it; the identifier follows. Validate both halves.
	before := time.Now().Unix()
	parts := strings.SplitN(creds.Username, ":", 2)
	if len(parts) != 2 {
		t.Fatalf("username %q is not <expiry>:<identifier>", creds.Username)
	}
	var expiry int64
	if _, err := fmt.Sscanf(parts[0], "%d", &expiry); err != nil {
		t.Fatalf("expiry not an int: %v", err)
	}
	if expiry < before+23*3600 {
		t.Errorf("expiry %d is less than ~23h in the future (before=%d)", expiry, before)
	}
	if parts[1] != "user@digits" {
		t.Errorf("identifier=%q, want user@digits", parts[1])
	}

	// Credential must be non-empty base64; coturn verifies it itself using
	// the shared secret, so we only check that we emitted well-formed data.
	if creds.Credential == "" {
		t.Fatal("expected non-empty credential")
	}
	if _, err := base64.StdEncoding.DecodeString(creds.Credential); err != nil {
		t.Errorf("credential is not valid base64: %v", err)
	}
}
