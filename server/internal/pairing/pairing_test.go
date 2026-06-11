//go:build integration

package pairing

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"regexp"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/justinlindh/digits/server/internal/db"
)

func setupStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping DB tests")
	}
	database, err := db.Open(dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	// Clean up test data
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM devices WHERE hardware_id LIKE 'test-%'")
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number LIKE '555%'")
		_, _ = database.DB.Exec("DELETE FROM households WHERE name = 'pairing test'")
	})

	return NewStore(database.DB)
}

// isPaired reports whether the device row has paired_at set. Production code
// never reads pairing state back by hardware ID (the device learns it is
// paired from the claim response pushed over its WebSocket), so this check
// lives in the tests.
func isPaired(t *testing.T, s *Store, hardwareID string) bool {
	t.Helper()
	var pairedAt sql.NullTime
	err := s.db.QueryRowContext(context.Background(), `
		SELECT paired_at FROM devices WHERE hardware_id = $1
	`, hardwareID).Scan(&pairedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		t.Fatalf("check paired %s: %v", hardwareID, err)
	}
	return pairedAt.Valid
}

// seedHousehold inserts a fresh household row and returns its UUID. Tests that
// call ClaimDevice need a valid household_id because lines.household_id has a
// foreign key onto households.id.
func seedHousehold(t *testing.T, s *Store) string {
	t.Helper()
	var id string
	if err := s.db.QueryRow(`INSERT INTO households (name) VALUES ('pairing test') RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("seed household: %v", err)
	}
	return id
}

func TestGenerateCode_Returns6Digits(t *testing.T) {
	s := setupStore(t)
	code, err := s.GenerateCode(context.Background(), "test-hw-001")
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	if !regexp.MustCompile(`^\d{6}$`).MatchString(code) {
		t.Errorf("expected 6-digit code, got %q", code)
	}
}

func TestGenerateCode_RotatesOnEveryCall(t *testing.T) {
	s := setupStore(t)
	code1, err := s.GenerateCode(context.Background(), "test-hw-002")
	if err != nil {
		t.Fatalf("GenerateCode 1: %v", err)
	}
	code2, err := s.GenerateCode(context.Background(), "test-hw-002")
	if err != nil {
		t.Fatalf("GenerateCode 2: %v", err)
	}
	if code1 == code2 {
		t.Errorf("expected rotated code on second call, got same %q both times", code1)
	}
}

func TestClaimDevice_Success(t *testing.T) {
	s := setupStore(t)
	hhID := seedHousehold(t, s)
	code, err := s.GenerateCode(context.Background(), "test-hw-003")
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}

	token, _, err := s.ClaimDevice(context.Background(), code, "5550100", "Kitchen Phone", "Kitchen Phone", hhID)
	if err != nil {
		t.Fatalf("ClaimDevice: %v", err)
	}
	if len(token) != 64 {
		t.Errorf("expected 64-char hex token, got %d chars: %q", len(token), token)
	}

	if !isPaired(t, s, "test-hw-003") {
		t.Error("expected device to be paired after ClaimDevice")
	}
}

func TestClaimDevice_FailOnInvalidCode(t *testing.T) {
	s := setupStore(t)
	hhID := seedHousehold(t, s)
	_, _, err := s.ClaimDevice(context.Background(), "999999", "5550101", "Bad Phone", "Bad Phone", hhID)
	if !errors.Is(err, ErrInvalidCode) {
		t.Errorf("expected ErrInvalidCode, got %v", err)
	}
}

func TestClaimDevice_FailOnExpiredCode(t *testing.T) {
	s := setupStore(t)
	code, err := s.GenerateCode(context.Background(), "test-hw-004")
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}

	// Manually expire the code
	_, err = s.db.Exec(`UPDATE devices SET pairing_code_expires_at = $1 WHERE hardware_id = $2`,
		time.Now().Add(-1*time.Minute), "test-hw-004")
	if err != nil {
		t.Fatalf("expire code: %v", err)
	}

	_, _, err = s.ClaimDevice(context.Background(), code, "5550102", "Expired Phone", "Expired Phone", seedHousehold(t, s))
	if !errors.Is(err, ErrInvalidCode) {
		t.Errorf("expected ErrInvalidCode for expired code, got %v", err)
	}
}

func TestGenerateCode_DoesNotPair(t *testing.T) {
	s := setupStore(t)
	_, err := s.GenerateCode(context.Background(), "test-hw-005")
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	if isPaired(t, s, "test-hw-005") {
		t.Error("expected device to stay unpaired before claiming")
	}
}

func TestCleanupExpiredCodes(t *testing.T) {
	s := setupStore(t)
	hwID := "test-hw-cleanup-001"

	// Insert a device with an already-expired pairing code. line_id is nullable
	// since migration v7 (a device exists before it's paired to a line).
	_, err := s.db.Exec(`
		INSERT INTO devices (line_id, hardware_id, pairing_code, pairing_code_expires_at)
		VALUES (NULL, $1, '123456', $2)
		ON CONFLICT (hardware_id) DO UPDATE
		SET pairing_code = '123456', pairing_code_expires_at = $2
	`, hwID, time.Now().Add(-1*time.Minute))
	if err != nil {
		t.Fatalf("insert expired device: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.db.Exec("DELETE FROM devices WHERE hardware_id = $1", hwID)
	})

	n, err := s.CleanupExpired(context.Background())
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if n < 1 {
		t.Errorf("expected at least 1 cleaned, got %d", n)
	}

	// Verify code is now NULL
	var code *string
	err = s.db.QueryRow(`SELECT pairing_code FROM devices WHERE hardware_id = $1`, hwID).Scan(&code)
	if err != nil {
		t.Fatalf("query after cleanup: %v", err)
	}
	if code != nil {
		t.Errorf("expected pairing_code to be NULL after cleanup, got %q", *code)
	}
}
