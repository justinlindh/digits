package auth

import (
	"errors"
	"testing"
)

func TestParseGoogleUserinfo(t *testing.T) {
	t.Run("verified email parses", func(t *testing.T) {
		body := []byte(`{"id":"123","email":"a@example.com","name":"A","verified_email":true}`)
		info, err := parseGoogleUserinfo(body)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Email != "a@example.com" || info.ID != "123" {
			t.Fatalf("unexpected info: %+v", info)
		}
	})

	t.Run("unverified email is rejected", func(t *testing.T) {
		body := []byte(`{"id":"123","email":"victim@example.com","name":"A","verified_email":false}`)
		info, err := parseGoogleUserinfo(body)
		if !errors.Is(err, errUnverifiedEmail) {
			t.Fatalf("expected errUnverifiedEmail, got %v", err)
		}
		// Info is still returned so the caller can log the offending Google ID.
		if info.ID != "123" {
			t.Fatalf("expected info ID to be returned alongside error, got %q", info.ID)
		}
	})

	t.Run("missing verified_email field is treated as unverified", func(t *testing.T) {
		body := []byte(`{"id":"123","email":"victim@example.com","name":"A"}`)
		_, err := parseGoogleUserinfo(body)
		if !errors.Is(err, errUnverifiedEmail) {
			t.Fatalf("expected errUnverifiedEmail for absent field, got %v", err)
		}
	})

	t.Run("malformed json returns parse error", func(t *testing.T) {
		_, err := parseGoogleUserinfo([]byte(`{not json`))
		if err == nil || errors.Is(err, errUnverifiedEmail) {
			t.Fatalf("expected a parse error, got %v", err)
		}
	})
}
