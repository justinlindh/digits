//go:build integration

package device

import (
	"context"
	"database/sql"
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
		t.Skip("TEST_DATABASE_URL not set, skipping DB tests")
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

// insertTestDevice inserts a device row directly so tests of read-side and
// state-mutation methods don't depend on a Store insert API. Returns the
// device id.
func insertTestDevice(t *testing.T, database *db.Database, lineID int64, hardwareID string) int64 {
	t.Helper()
	var id int64
	err := database.DB.QueryRow(
		`INSERT INTO devices (line_id, hardware_id) VALUES ($1, $2) RETURNING id`,
		lineID, hardwareID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insertTestDevice(%s): %v", hardwareID, err)
	}
	return id
}

func TestListByLine(t *testing.T) {
	s, database := testStore(t)
	hhID := createTestHousehold(t, database, "List Household")
	lineA := createTestLine(t, database, "5550001111", hhID)
	lineB := createTestLine(t, database, "5550002222", hhID)

	insertTestDevice(t, database, lineA, "hw-a1")
	insertTestDevice(t, database, lineA, "hw-a2")
	insertTestDevice(t, database, lineB, "hw-b1")

	devicesA, err := s.ListByLine(context.Background(), lineA)
	if err != nil {
		t.Fatalf("ListByLine(lineA): %v", err)
	}
	if len(devicesA) != 2 {
		t.Errorf("ListByLine(lineA) = %d devices, want 2", len(devicesA))
	}

	devicesB, err := s.ListByLine(context.Background(), lineB)
	if err != nil {
		t.Fatalf("ListByLine(lineB): %v", err)
	}
	if len(devicesB) != 1 {
		t.Errorf("ListByLine(lineB) = %d devices, want 1", len(devicesB))
	}
}

func TestTouchLastSeen(t *testing.T) {
	s, database := testStore(t)
	hhID := createTestHousehold(t, database, "LastSeen Household")
	lineID := createTestLine(t, database, "5558880001", hhID)

	insertTestDevice(t, database, lineID, "hw-lastseen-001")

	if err := s.TouchLastSeen(context.Background(), "hw-lastseen-001"); err != nil {
		t.Fatalf("TouchLastSeen: %v", err)
	}

	var lastSeenAt sql.NullTime
	err := database.DB.QueryRow(
		`SELECT last_seen_at FROM devices WHERE hardware_id = $1`,
		"hw-lastseen-001",
	).Scan(&lastSeenAt)
	if err != nil {
		t.Fatalf("read last_seen_at: %v", err)
	}
	if !lastSeenAt.Valid {
		t.Fatal("expected last_seen_at to be set after TouchLastSeen")
	}
	if time.Since(lastSeenAt.Time) > 5*time.Second {
		t.Errorf("last_seen_at too old: %v", lastSeenAt.Time)
	}
}

func TestTouchLastSeen_UnknownHardware(t *testing.T) {
	s, _ := testStore(t)

	// Should not error for unknown hardware ID (just no rows affected).
	if err := s.TouchLastSeen(context.Background(), "does-not-exist"); err != nil {
		t.Errorf("TouchLastSeen for unknown hardware: %v", err)
	}
}

// pairTestDevice marks the device row as paired with the given plaintext
// token (stored as its sha256 hash). Used by AuthStatus tests to set up
// the paired-with-token state directly without going through the pairing
// package.
func pairTestDevice(t *testing.T, database *db.Database, hardwareID, plaintextToken string) {
	t.Helper()
	_, err := database.DB.Exec(
		`UPDATE devices SET device_token = $1, paired_at = NOW() WHERE hardware_id = $2`,
		HashToken(plaintextToken), hardwareID,
	)
	if err != nil {
		t.Fatalf("pairTestDevice(%s): %v", hardwareID, err)
	}
}

func TestAuthStatus_PairedCorrectToken(t *testing.T) {
	s, database := testStore(t)
	hhID := createTestHousehold(t, database, "Auth Household")
	lineID := createTestLine(t, database, "5551110001", hhID)
	insertTestDevice(t, database, lineID, "hw-auth-001")
	const plaintext = "deadbeef01234567890abcdef01234567890abcdef01234567890abcdef012345"
	pairTestDevice(t, database, "hw-auth-001", plaintext)

	paired, valid, err := s.AuthStatus(context.Background(), "hw-auth-001", plaintext)
	if err != nil {
		t.Fatalf("AuthStatus: %v", err)
	}
	if !paired || !valid {
		t.Errorf("AuthStatus = paired=%v valid=%v, want paired=true valid=true", paired, valid)
	}
}

func TestAuthStatus_PairedWrongToken(t *testing.T) {
	s, database := testStore(t)
	hhID := createTestHousehold(t, database, "Auth Wrong Household")
	lineID := createTestLine(t, database, "5551110002", hhID)
	insertTestDevice(t, database, lineID, "hw-auth-002")
	pairTestDevice(t, database, "hw-auth-002", "real-token")

	paired, valid, err := s.AuthStatus(context.Background(), "hw-auth-002", "wrong-token")
	if err != nil {
		t.Fatalf("AuthStatus: %v", err)
	}
	if !paired {
		t.Error("expected paired=true")
	}
	if valid {
		t.Error("expected valid=false for wrong token")
	}
}

func TestAuthStatus_NonExistent(t *testing.T) {
	s, _ := testStore(t)

	paired, valid, err := s.AuthStatus(context.Background(), "hw-does-not-exist", "any-token")
	if err != nil {
		t.Fatalf("AuthStatus: %v", err)
	}
	if paired || valid {
		t.Errorf("AuthStatus = paired=%v valid=%v, want both false", paired, valid)
	}
}
