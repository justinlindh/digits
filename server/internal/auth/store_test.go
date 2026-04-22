//go:build integration

package auth

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/device"
)

// testDB creates a Store connected to the test database, running migrations first.
// Tests are skipped if TEST_DATABASE_URL is not set.
func testDB(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping DB tests")
	}
	// Use db.Open to ensure migrations run (creates tables)
	database, err := db.Open(dsn)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	s := NewStoreFromDB(database.DB)
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM sessions")
		_, _ = database.DB.Exec("DELETE FROM magic_links")
		_, _ = database.DB.Exec("DELETE FROM users")
		_ = database.Close()
	})
	return s
}

func TestCreateAndGetUser(t *testing.T) {
	s := testDB(t)

	u, err := s.CreateUser(context.Background(), "test@example.com", "Test User", nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.Email != "test@example.com" {
		t.Errorf("email = %q, want test@example.com", u.Email)
	}
	if u.Name != "Test User" {
		t.Errorf("name = %q, want Test User", u.Name)
	}
	if u.ID == "" {
		t.Error("ID should not be empty")
	}
	if u.GoogleID != nil {
		t.Errorf("GoogleID should be nil, got %v", u.GoogleID)
	}

	// GetUserByEmail
	got, err := s.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("ID mismatch: got %s, want %s", got.ID, u.ID)
	}

	// GetUserByID
	got2, err := s.GetUserByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got2.Email != u.Email {
		t.Errorf("email mismatch via ID lookup")
	}
}

func TestGetUserByEmail_NotFound(t *testing.T) {
	s := testDB(t)
	_, err := s.GetUserByEmail(context.Background(), "nobody@example.com")
	if !errors.Is(err, ErrUserNotFound) {
		t.Errorf("expected ErrUserNotFound, got %v", err)
	}
}

func TestGetUserByGoogleID_NotFound(t *testing.T) {
	s := testDB(t)
	_, err := s.GetUserByGoogleID(context.Background(), "no-such-google-id")
	if !errors.Is(err, ErrUserNotFound) {
		t.Errorf("expected ErrUserNotFound, got %v", err)
	}
}

func TestGetUserByID_NotFound(t *testing.T) {
	s := testDB(t)
	_, err := s.GetUserByID(context.Background(), "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, ErrUserNotFound) {
		t.Errorf("expected ErrUserNotFound, got %v", err)
	}
}

func TestCreateUserWithGoogleID(t *testing.T) {
	s := testDB(t)
	gid := "google-sub-123"
	u, err := s.CreateUser(context.Background(), "google@example.com", "Google User", &gid)
	if err != nil {
		t.Fatalf("CreateUser with GoogleID: %v", err)
	}
	if u.GoogleID == nil || *u.GoogleID != gid {
		t.Errorf("GoogleID = %v, want %q", u.GoogleID, gid)
	}

	// GetUserByGoogleID
	got, err := s.GetUserByGoogleID(context.Background(), gid)
	if err != nil {
		t.Fatalf("GetUserByGoogleID: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("ID mismatch via google lookup")
	}
}

func TestLinkGoogleID(t *testing.T) {
	s := testDB(t)
	u, err := s.CreateUser(context.Background(), "link@example.com", "Link User", nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := s.LinkGoogleID(context.Background(), u.ID, "new-google-id"); err != nil {
		t.Fatalf("LinkGoogleID: %v", err)
	}

	got, err := s.GetUserByGoogleID(context.Background(), "new-google-id")
	if err != nil {
		t.Fatalf("GetUserByGoogleID after link: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("ID mismatch after linking google ID")
	}
}

func TestUpdateLastLogin(t *testing.T) {
	s := testDB(t)
	u, err := s.CreateUser(context.Background(), "login@example.com", "Login User", nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := s.UpdateLastLogin(context.Background(), u.ID); err != nil {
		t.Fatalf("UpdateLastLogin: %v", err)
	}

	got, err := s.GetUserByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.LastLoginAt == nil {
		t.Error("LastLoginAt should be set after UpdateLastLogin")
	}
}

func TestCreateAndValidateSession(t *testing.T) {
	s := testDB(t)
	u, err := s.CreateUser(context.Background(), "session@test.com", "Session User", nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	token, sess, err := s.CreateSession(context.Background(), u.ID, 24*time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if token == "" {
		t.Error("token should not be empty")
	}
	if sess.UserID != u.ID {
		t.Errorf("session UserID = %s, want %s", sess.UserID, u.ID)
	}
	if sess.ID == "" {
		t.Error("session ID should not be empty")
	}

	// Validate the session
	got, err := s.ValidateSession(context.Background(), token)
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}
	if got.UserID != u.ID {
		t.Errorf("validated session user mismatch")
	}
	if got.ID != sess.ID {
		t.Errorf("validated session ID mismatch")
	}
}

func TestValidateSession_Invalid(t *testing.T) {
	s := testDB(t)
	_, err := s.ValidateSession(context.Background(), "totally-fake-token")
	if err == nil {
		t.Error("expected error for invalid token, got nil")
	}
}

func TestDeleteSession(t *testing.T) {
	s := testDB(t)
	u, err := s.CreateUser(context.Background(), "delete@test.com", "Delete User", nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	token, _, err := s.CreateSession(context.Background(), u.ID, 24*time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := s.DeleteSession(context.Background(), token); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	_, err = s.ValidateSession(context.Background(), token)
	if err == nil {
		t.Error("session should be invalid after deletion")
	}
}

func TestRefreshSession(t *testing.T) {
	s := testDB(t)
	u, err := s.CreateUser(context.Background(), "refresh@test.com", "Refresh User", nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	token, sess, err := s.CreateSession(context.Background(), u.ID, 24*time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	originalExpiry := sess.ExpiresAt

	if err := s.RefreshSession(context.Background(), token, 48*time.Hour); err != nil {
		t.Fatalf("RefreshSession: %v", err)
	}

	got, err := s.ValidateSession(context.Background(), token)
	if err != nil {
		t.Fatalf("ValidateSession after refresh: %v", err)
	}
	if !got.ExpiresAt.After(originalExpiry) {
		t.Errorf("refreshed expiry %v should be after original %v", got.ExpiresAt, originalExpiry)
	}
}

func TestCreateAndValidateMagicLink(t *testing.T) {
	s := testDB(t)

	token, err := s.CreateMagicLink(context.Background(), "magic@test.com", 15*time.Minute)
	if err != nil {
		t.Fatalf("CreateMagicLink: %v", err)
	}
	if token == "" {
		t.Error("magic link token should not be empty")
	}

	// First use should succeed
	email, err := s.ValidateMagicLink(context.Background(), token)
	if err != nil {
		t.Fatalf("ValidateMagicLink: %v", err)
	}
	if email != "magic@test.com" {
		t.Errorf("email = %q, want magic@test.com", email)
	}

	// Second use should fail (single-use enforcement)
	_, err = s.ValidateMagicLink(context.Background(), token)
	if err == nil {
		t.Error("expected error on reuse of magic link, got nil")
	}
}

func TestValidateMagicLink_InvalidToken(t *testing.T) {
	s := testDB(t)
	_, err := s.ValidateMagicLink(context.Background(), "fake-magic-token")
	if err == nil {
		t.Error("expected error for invalid magic link token, got nil")
	}
}

func TestCleanupExpired(t *testing.T) {
	s := testDB(t)
	u, err := s.CreateUser(context.Background(), "cleanup@test.com", "Cleanup User", nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Create a session and then force-expire it
	token, _, err := s.CreateSession(context.Background(), u.ID, 24*time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	hash := device.HashToken(token)
	_, _ = s.db.Exec(`UPDATE sessions SET expires_at = NOW() - interval '1 second' WHERE token_hash = $1`, hash)

	// Create a magic link and force-expire it
	mlToken, err := s.CreateMagicLink(context.Background(), "cleanup@test.com", 15*time.Minute)
	if err != nil {
		t.Fatalf("CreateMagicLink: %v", err)
	}
	mlHash := device.HashToken(mlToken)
	_, _ = s.db.Exec(`UPDATE magic_links SET expires_at = NOW() - interval '1 second' WHERE token_hash = $1`, mlHash)

	if err := s.CleanupExpired(context.Background()); err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}

	// Session should be gone (validate returns error)
	_, err = s.ValidateSession(context.Background(), token)
	if err == nil {
		t.Error("expired session should be invalid after cleanup")
	}
}

func TestSetAndLoadCRTMode(t *testing.T) {
	s := testDB(t)

	u, err := s.CreateUser(context.Background(), "crt@test.com", "CRT User", nil)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	// Newly-created users default to CRTModeConnecting.
	if u.CRTMode != CRTModeConnecting {
		t.Errorf("default CRTMode = %q, want %q", u.CRTMode, CRTModeConnecting)
	}

	if err := s.SetCRTMode(context.Background(), u.ID, CRTModeAll); err != nil {
		t.Fatalf("SetCRTMode: %v", err)
	}

	got, err := s.GetUserByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.CRTMode != CRTModeAll {
		t.Errorf("after SetCRTMode(all), loaded CRTMode = %q, want %q", got.CRTMode, CRTModeAll)
	}

	// Invalid values are rejected.
	if err := s.SetCRTMode(context.Background(), u.ID, CRTMode("bogus")); err == nil {
		t.Error("expected error for invalid CRTMode, got nil")
	}
}
