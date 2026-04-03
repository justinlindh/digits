package turn

import (
	"testing"
	"time"
)

func TestGenerateCredentials(t *testing.T) {
	gen := NewCredentialGenerator("supersecret", 24*time.Hour)
	creds := gen.Generate("user@digits")

	if creds.Username == "" {
		t.Error("expected non-empty username")
	}
	if creds.Credential == "" {
		t.Error("expected non-empty credential")
	}
	if !gen.Verify(creds.Username, creds.Credential) {
		t.Error("expected Verify to return true for freshly generated credentials")
	}
}

func TestVerify_TamperedCredential(t *testing.T) {
	gen := NewCredentialGenerator("supersecret", 24*time.Hour)
	creds := gen.Generate("user@digits")

	if gen.Verify(creds.Username, "tampered-credential-xyz") {
		t.Error("expected Verify to return false for tampered credential")
	}
}

func TestVerify_ExpiredCredential(t *testing.T) {
	// Negative TTL puts expiry in the past
	gen := NewCredentialGenerator("supersecret", -time.Hour)
	creds := gen.Generate("user@digits")

	if gen.Verify(creds.Username, creds.Credential) {
		t.Error("expected Verify to return false for expired credential")
	}
}
