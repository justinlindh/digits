package turn

import (
	"crypto/hmac"
	"crypto/sha1"
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

// TestGenerateCredential_HMACMatches pins the credential to the exact
// HMAC-SHA1 of the username. The base64-validity check above would still pass
// if Generate hashed the wrong field (or used the wrong key); this recomputes
// the digest independently so a regression in the scheme is caught here rather
// than only by coturn rejecting calls in production.
func TestGenerateCredential_HMACMatches(t *testing.T) {
	const secret = "supersecret"
	gen := NewCredentialGenerator(secret, 24*time.Hour)
	creds := gen.Generate("user@digits")

	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(creds.Username))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	if creds.Credential != want {
		t.Errorf("credential = %q, want HMAC-SHA1 of username = %q", creds.Credential, want)
	}
}

// TestGenerateCredential_KnownAnswer is a fixed test vector: a known secret and
// a known username produce a known credential. It guards the wire format
// (HMAC-SHA1 + standard base64) against an accidental algorithm or encoding
// swap that the self-consistency check above would not detect.
func TestGenerateCredential_KnownAnswer(t *testing.T) {
	// Independently computed: base64(HMAC-SHA1("secret123", "1700000000:alice")).
	const (
		secret   = "secret123"
		username = "1700000000:alice"
		want     = "pItSt+AgllVYw7AC0p93hqNrtPQ="
	)
	gen := NewCredentialGenerator(secret, time.Hour)
	got := gen.computeHMAC(username)
	if got != want {
		t.Errorf("computeHMAC(%q) = %q, want %q", username, got, want)
	}
}
