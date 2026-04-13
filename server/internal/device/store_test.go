//go:build integration

package device

import (
	"os"
	"testing"
	"time"

	"github.com/justinlindh/digits/server/internal/db"
)

// testStore opens a connection to the test database and returns a Store.
// The test is skipped if TEST_DATABASE_URL is not set.
func testStore(t *testing.T) (*Store, *db.Database) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping DB tests")
	}
	database, err := db.Open(dsn)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	s := NewStore(database)
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM devices")
		_, _ = database.DB.Exec("DELETE FROM lines")
		_, _ = database.DB.Exec("DELETE FROM household_members")
		_, _ = database.DB.Exec("DELETE FROM households")
		_ = database.Close()
	})
	return s, database
}

// createTestHousehold inserts a household and returns its UUID.
func createTestHousehold(t *testing.T, database *db.Database, name string) string {
	t.Helper()
	var id string
	err := database.DB.QueryRow(
		`INSERT INTO households (name) VALUES ($1) RETURNING id`, name,
	).Scan(&id)
	if err != nil {
		t.Fatalf("createTestHousehold(%s): %v", name, err)
	}
	return id
}

// createTestLine inserts a line in the given household and returns its ID.
func createTestLine(t *testing.T, database *db.Database, number, householdID string) int64 {
	t.Helper()
	var id int64
	err := database.DB.QueryRow(
		`INSERT INTO lines (number, household_id) VALUES ($1, $2) RETURNING id`,
		number, householdID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("createTestLine(%s): %v", number, err)
	}
	return id
}

func TestCreateAndGetByID(t *testing.T) {
	s, database := testStore(t)
	hhID := createTestHousehold(t, database, "Test Household")
	lineID := createTestLine(t, database, "5551234567", hhID)

	dev, err := s.Create(lineID, "hw-abc-123")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if dev.ID == 0 {
		t.Error("expected non-zero device ID")
	}
	if dev.LineID == nil || *dev.LineID != lineID {
		t.Errorf("LineID = %v, want %d", dev.LineID, lineID)
	}
	if dev.HardwareID != "hw-abc-123" {
		t.Errorf("HardwareID = %q, want hw-abc-123", dev.HardwareID)
	}

	got, err := s.GetByID(dev.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != dev.ID {
		t.Errorf("GetByID.ID = %d, want %d", got.ID, dev.ID)
	}
	if got.HardwareID != dev.HardwareID {
		t.Errorf("GetByID.HardwareID = %q, want %q", got.HardwareID, dev.HardwareID)
	}
}

func TestGetByHardwareID(t *testing.T) {
	s, database := testStore(t)
	hhID := createTestHousehold(t, database, "HW Household")
	lineID := createTestLine(t, database, "5559876543", hhID)

	dev, err := s.Create(lineID, "hw-xyz-999")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.GetByHardwareID("hw-xyz-999")
	if err != nil {
		t.Fatalf("GetByHardwareID: %v", err)
	}
	if got.ID != dev.ID {
		t.Errorf("ID = %d, want %d", got.ID, dev.ID)
	}

	_, err = s.GetByHardwareID("does-not-exist")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound for missing hardware ID, got %v", err)
	}
}

func TestListByLine(t *testing.T) {
	s, database := testStore(t)
	hhID := createTestHousehold(t, database, "List Household")
	lineA := createTestLine(t, database, "5550001111", hhID)
	lineB := createTestLine(t, database, "5550002222", hhID)

	_, err := s.Create(lineA, "hw-a1")
	if err != nil {
		t.Fatalf("Create device 1: %v", err)
	}
	_, err = s.Create(lineA, "hw-a2")
	if err != nil {
		t.Fatalf("Create device 2: %v", err)
	}
	_, err = s.Create(lineB, "hw-b1")
	if err != nil {
		t.Fatalf("Create device 3: %v", err)
	}

	devicesA, err := s.ListByLine(lineA)
	if err != nil {
		t.Fatalf("ListByLine(lineA): %v", err)
	}
	if len(devicesA) != 2 {
		t.Errorf("ListByLine(lineA) = %d devices, want 2", len(devicesA))
	}

	devicesB, err := s.ListByLine(lineB)
	if err != nil {
		t.Fatalf("ListByLine(lineB): %v", err)
	}
	if len(devicesB) != 1 {
		t.Errorf("ListByLine(lineB) = %d devices, want 1", len(devicesB))
	}
}

func TestDelete(t *testing.T) {
	s, database := testStore(t)
	hhID := createTestHousehold(t, database, "Delete Household")
	lineID := createTestLine(t, database, "5553334444", hhID)

	dev, err := s.Create(lineID, "hw-del-001")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.Delete(dev.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = s.GetByID(dev.ID)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}

	// Deleting again should return ErrNotFound
	err = s.Delete(dev.ID)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound on second delete, got %v", err)
	}
}

func TestTouchLastSeen(t *testing.T) {
	s, database := testStore(t)
	hhID := createTestHousehold(t, database, "LastSeen Household")
	lineID := createTestLine(t, database, "5558880001", hhID)

	dev, err := s.Create(lineID, "hw-lastseen-001")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Initially nil
	if dev.LastSeenAt != nil {
		t.Errorf("expected LastSeenAt to be nil initially, got %v", dev.LastSeenAt)
	}

	// Touch it
	if err := s.TouchLastSeen("hw-lastseen-001"); err != nil {
		t.Fatalf("TouchLastSeen: %v", err)
	}

	got, err := s.GetByID(dev.ID)
	if err != nil {
		t.Fatalf("GetByID after touch: %v", err)
	}
	if got.LastSeenAt == nil {
		t.Fatal("expected LastSeenAt to be set after TouchLastSeen")
	}
	if time.Since(*got.LastSeenAt) > 5*time.Second {
		t.Errorf("LastSeenAt too old: %v", got.LastSeenAt)
	}
}

func TestTouchLastSeen_UnknownHardware(t *testing.T) {
	s, _ := testStore(t)

	// Should not error for unknown hardware ID (just no rows affected)
	if err := s.TouchLastSeen("does-not-exist"); err != nil {
		t.Errorf("TouchLastSeen for unknown hardware: %v", err)
	}
}

func TestPairingCode(t *testing.T) {
	s, database := testStore(t)
	hhID := createTestHousehold(t, database, "Pairing Household")
	lineID := createTestLine(t, database, "5556667777", hhID)

	dev, err := s.Create(lineID, "hw-pair-001")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	expiresAt := time.Now().Add(10 * time.Minute)
	if err := s.SetPairingCode(dev.ID, "SECRET123", expiresAt); err != nil {
		t.Fatalf("SetPairingCode: %v", err)
	}

	got, err := s.GetByPairingCode("SECRET123")
	if err != nil {
		t.Fatalf("GetByPairingCode: %v", err)
	}
	if got.ID != dev.ID {
		t.Errorf("GetByPairingCode.ID = %d, want %d", got.ID, dev.ID)
	}

	// Expired code should not be found
	pastExpiry := time.Now().Add(-1 * time.Minute)
	if err := s.SetPairingCode(dev.ID, "EXPIRED", pastExpiry); err != nil {
		t.Fatalf("SetPairingCode (expired): %v", err)
	}
	_, err = s.GetByPairingCode("EXPIRED")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound for expired pairing code, got %v", err)
	}

	// CompletePairing clears the code
	if err := s.SetPairingCode(dev.ID, "COMPLETE", time.Now().Add(10*time.Minute)); err != nil {
		t.Fatalf("SetPairingCode (complete): %v", err)
	}
	if err := s.CompletePairing(dev.ID); err != nil {
		t.Fatalf("CompletePairing: %v", err)
	}

	completed, err := s.GetByID(dev.ID)
	if err != nil {
		t.Fatalf("GetByID after CompletePairing: %v", err)
	}
	if completed.PairingCode != nil {
		t.Errorf("expected PairingCode to be nil after CompletePairing, got %q", *completed.PairingCode)
	}
	if completed.PairedAt == nil {
		t.Error("expected PairedAt to be set after CompletePairing")
	}
}

func TestValidateToken_Correct(t *testing.T) {
	s, database := testStore(t)
	hhID := createTestHousehold(t, database, "Token Household")
	lineID := createTestLine(t, database, "5551110001", hhID)

	dev, err := s.Create(lineID, "hw-token-001")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Simulate what pairing does: store hashed token, mark as paired
	plaintext := "deadbeef01234567890abcdef01234567890abcdef01234567890abcdef012345"
	hashed := HashToken(plaintext)
	_, err = database.DB.Exec(
		`UPDATE devices SET device_token = $1, paired_at = NOW() WHERE id = $2`,
		hashed, dev.ID,
	)
	if err != nil {
		t.Fatalf("set hashed token: %v", err)
	}

	valid, err := s.ValidateToken("hw-token-001", plaintext)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if !valid {
		t.Error("expected ValidateToken to return true for correct token")
	}
}

func TestValidateToken_Wrong(t *testing.T) {
	s, database := testStore(t)
	hhID := createTestHousehold(t, database, "Token Wrong Household")
	lineID := createTestLine(t, database, "5551110002", hhID)

	dev, err := s.Create(lineID, "hw-token-002")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	plaintext := "deadbeef01234567890abcdef01234567890abcdef01234567890abcdef012345"
	hashed := HashToken(plaintext)
	_, err = database.DB.Exec(
		`UPDATE devices SET device_token = $1, paired_at = NOW() WHERE id = $2`,
		hashed, dev.ID,
	)
	if err != nil {
		t.Fatalf("set hashed token: %v", err)
	}

	valid, err := s.ValidateToken("hw-token-002", "wrong-token")
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if valid {
		t.Error("expected ValidateToken to return false for wrong token")
	}
}

func TestValidateToken_NonExistent(t *testing.T) {
	s, _ := testStore(t)

	valid, err := s.ValidateToken("hw-does-not-exist", "any-token")
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if valid {
		t.Error("expected ValidateToken to return false for non-existent hardware ID")
	}
}
