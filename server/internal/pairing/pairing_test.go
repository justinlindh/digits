package pairing

import (
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
	t.Cleanup(func() { database.Close() })

	// Clean up test data
	t.Cleanup(func() {
		database.DB.Exec("DELETE FROM devices WHERE hardware_id LIKE 'test-%'")
		database.DB.Exec("DELETE FROM lines WHERE number LIKE '555%'")
	})

	return NewStore(database.DB)
}

func TestGenerateCode_Returns6Digits(t *testing.T) {
	s := setupStore(t)
	code, err := s.GenerateCode("test-hw-001")
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	if !regexp.MustCompile(`^\d{6}$`).MatchString(code) {
		t.Errorf("expected 6-digit code, got %q", code)
	}
}

func TestGenerateCode_ReusesBeforeExpiry(t *testing.T) {
	s := setupStore(t)
	code1, err := s.GenerateCode("test-hw-002")
	if err != nil {
		t.Fatalf("GenerateCode 1: %v", err)
	}
	code2, err := s.GenerateCode("test-hw-002")
	if err != nil {
		t.Fatalf("GenerateCode 2: %v", err)
	}
	if code1 != code2 {
		t.Errorf("expected same code before expiry, got %q and %q", code1, code2)
	}
}

func TestClaimDevice_Success(t *testing.T) {
	s := setupStore(t)
	code, err := s.GenerateCode("test-hw-003")
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}

	token, _, err := s.ClaimDevice(code, "5550100", "Kitchen Phone", "00000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatalf("ClaimDevice: %v", err)
	}
	if len(token) != 64 {
		t.Errorf("expected 64-char hex token, got %d chars: %q", len(token), token)
	}

	paired, err := s.IsPaired("test-hw-003")
	if err != nil {
		t.Fatalf("IsPaired: %v", err)
	}
	if !paired {
		t.Error("expected device to be paired after ClaimDevice")
	}
}

func TestClaimDevice_FailOnInvalidCode(t *testing.T) {
	s := setupStore(t)
	_, _, err := s.ClaimDevice("999999", "5550101", "Bad Phone", "00000000-0000-0000-0000-000000000001")
	if err != ErrInvalidCode {
		t.Errorf("expected ErrInvalidCode, got %v", err)
	}
}

func TestClaimDevice_FailOnExpiredCode(t *testing.T) {
	s := setupStore(t)
	code, err := s.GenerateCode("test-hw-004")
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}

	// Manually expire the code
	_, err = s.db.Exec(`UPDATE devices SET pairing_code_expires_at = $1 WHERE hardware_id = $2`,
		time.Now().Add(-1*time.Minute), "test-hw-004")
	if err != nil {
		t.Fatalf("expire code: %v", err)
	}

	_, _, err = s.ClaimDevice(code, "5550102", "Expired Phone", "00000000-0000-0000-0000-000000000001")
	if err != ErrInvalidCode {
		t.Errorf("expected ErrInvalidCode for expired code, got %v", err)
	}
}

func TestIsPaired_FalseForUnknown(t *testing.T) {
	s := setupStore(t)
	paired, err := s.IsPaired("test-hw-nonexistent")
	if err != nil {
		t.Fatalf("IsPaired: %v", err)
	}
	if paired {
		t.Error("expected false for unknown hardware ID")
	}
}

func TestIsPaired_FalseBeforeClaim(t *testing.T) {
	s := setupStore(t)
	_, err := s.GenerateCode("test-hw-005")
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	paired, err := s.IsPaired("test-hw-005")
	if err != nil {
		t.Fatalf("IsPaired: %v", err)
	}
	if paired {
		t.Error("expected false before claiming")
	}
}

func TestCleanupExpiredCodes(t *testing.T) {
	s := setupStore(t)
	hwID := "test-hw-cleanup-001"

	// Insert a device with an already-expired pairing code
	_, err := s.db.Exec(`
		INSERT INTO devices (line_id, hardware_id, pairing_code, pairing_code_expires_at)
		VALUES (0, $1, '123456', $2)
		ON CONFLICT (hardware_id) DO UPDATE
		SET pairing_code = '123456', pairing_code_expires_at = $2
	`, hwID, time.Now().Add(-1*time.Minute))
	if err != nil {
		t.Fatalf("insert expired device: %v", err)
	}
	t.Cleanup(func() {
		s.db.Exec("DELETE FROM devices WHERE hardware_id = $1", hwID)
	})

	n, err := s.CleanupExpired()
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

func TestRegenerateCode(t *testing.T) {
	s := setupStore(t)
	hwID := "test-hw-regen-001"
	t.Cleanup(func() {
		s.db.Exec("DELETE FROM devices WHERE hardware_id = $1", hwID)
	})

	// Generate initial code
	code1, err := s.GenerateCode(hwID)
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}

	// Regenerate — must return a 6-digit code
	code2, err := s.RegenerateCode(hwID)
	if err != nil {
		t.Fatalf("RegenerateCode: %v", err)
	}

	if len(code2) != CodeLength {
		t.Errorf("expected %d-digit code, got %q (len %d)", CodeLength, code2, len(code2))
	}

	// Statistically very unlikely to be the same; treat equality as a failure
	if code1 == code2 {
		t.Logf("note: regenerated code happens to match original (%q); rerunning once", code1)
		code3, err := s.RegenerateCode(hwID)
		if err != nil {
			t.Fatalf("RegenerateCode retry: %v", err)
		}
		if code1 == code3 {
			t.Errorf("regenerated code matched original twice (%q) — likely a bug", code1)
		}
	}
}

func TestRandomCode(t *testing.T) {
	code, err := randomCode(6)
	if err != nil {
		t.Fatalf("randomCode: %v", err)
	}
	if len(code) != 6 {
		t.Errorf("expected length 6, got %d: %q", len(code), code)
	}
	// Should be all digits
	for _, c := range code {
		if c < '0' || c > '9' {
			t.Errorf("non-digit character in code: %q", code)
			break
		}
	}
}
