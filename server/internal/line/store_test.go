package line

import (
	"os"
	"testing"

	"github.com/justinlindh/digits/server/internal/db"
)

// testStore creates a Store connected to the test database, running migrations first.
// Tests are skipped if TEST_DATABASE_URL is not set.
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
		database.DB.Exec("DELETE FROM lines")
		database.DB.Exec("DELETE FROM household_members")
		database.DB.Exec("DELETE FROM households")
		database.DB.Exec("DELETE FROM sessions")
		database.DB.Exec("DELETE FROM magic_links")
		database.DB.Exec("DELETE FROM users")
		database.Close()
	})
	return s, database
}

// createTestHousehold inserts a minimal household and returns its UUID.
func createTestHousehold(t *testing.T, database *db.Database) string {
	t.Helper()
	var id string
	err := database.DB.QueryRow(
		`INSERT INTO households (name) VALUES ($1) RETURNING id`,
		"Test Household",
	).Scan(&id)
	if err != nil {
		t.Fatalf("createTestHousehold: %v", err)
	}
	return id
}

func TestAddAndGetByID(t *testing.T) {
	s, database := testStore(t)
	householdID := createTestHousehold(t, database)

	l, err := s.Add("5551234", "Alice", householdID)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if l.ID == 0 {
		t.Error("line ID should not be zero")
	}
	if l.Number != "5551234" {
		t.Errorf("Number = %q, want 5551234", l.Number)
	}
	if l.Name != "Alice" {
		t.Errorf("Name = %q, want Alice", l.Name)
	}
	if l.HouseholdID != householdID {
		t.Errorf("HouseholdID = %q, want %q", l.HouseholdID, householdID)
	}

	got, err := s.GetByID(l.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != l.ID {
		t.Errorf("ID mismatch: got %d, want %d", got.ID, l.ID)
	}
	if got.Number != l.Number {
		t.Errorf("Number mismatch: got %q, want %q", got.Number, l.Number)
	}
}

func TestGetByNumber(t *testing.T) {
	s, database := testStore(t)
	householdID := createTestHousehold(t, database)

	_, err := s.Add("5559876", "Bob", householdID)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	got, err := s.GetByNumber("5559876")
	if err != nil {
		t.Fatalf("GetByNumber: %v", err)
	}
	if got.Number != "5559876" {
		t.Errorf("Number = %q, want 5559876", got.Number)
	}
	if got.Name != "Bob" {
		t.Errorf("Name = %q, want Bob", got.Name)
	}
}

func TestListByHousehold(t *testing.T) {
	s, database := testStore(t)
	householdID := createTestHousehold(t, database)
	otherHouseholdID := createTestHousehold(t, database)

	if _, err := s.Add("1110001", "Line A", householdID); err != nil {
		t.Fatalf("Add A: %v", err)
	}
	if _, err := s.Add("1110002", "Line B", householdID); err != nil {
		t.Fatalf("Add B: %v", err)
	}
	if _, err := s.Add("1110003", "Line Other", otherHouseholdID); err != nil {
		t.Fatalf("Add Other: %v", err)
	}

	lines, err := s.ListByHousehold(householdID)
	if err != nil {
		t.Fatalf("ListByHousehold: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	// Should be ordered by number
	if lines[0].Number != "1110001" {
		t.Errorf("lines[0].Number = %q, want 1110001", lines[0].Number)
	}
	if lines[1].Number != "1110002" {
		t.Errorf("lines[1].Number = %q, want 1110002", lines[1].Number)
	}
}

func TestDuplicateNumberRejection(t *testing.T) {
	s, database := testStore(t)
	householdID := createTestHousehold(t, database)

	if _, err := s.Add("9990001", "First", householdID); err != nil {
		t.Fatalf("Add first: %v", err)
	}

	// Adding the same number should fail due to UNIQUE constraint
	_, err := s.Add("9990001", "Duplicate", householdID)
	if err == nil {
		t.Error("expected error for duplicate number, got nil")
	}
}

func TestNumberExists(t *testing.T) {
	s, database := testStore(t)
	householdID := createTestHousehold(t, database)

	exists, err := s.NumberExists("8880001")
	if err != nil {
		t.Fatalf("NumberExists before add: %v", err)
	}
	if exists {
		t.Error("number should not exist before being added")
	}

	l, err := s.Add("8880001", "Test", householdID)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	exists, err = s.NumberExists("8880001")
	if err != nil {
		t.Fatalf("NumberExists after add: %v", err)
	}
	if !exists {
		t.Error("number should exist after being added")
	}

	// NumberExistsExcluding should return false for the same ID
	exists, err = s.NumberExistsExcluding("8880001", l.ID)
	if err != nil {
		t.Fatalf("NumberExistsExcluding: %v", err)
	}
	if exists {
		t.Error("number should not exist when excluding its own ID")
	}

	// NumberExistsExcluding should return true for a different ID
	exists, err = s.NumberExistsExcluding("8880001", l.ID+999)
	if err != nil {
		t.Fatalf("NumberExistsExcluding with different ID: %v", err)
	}
	if !exists {
		t.Error("number should exist when excluding a different ID")
	}
}

func TestDelete(t *testing.T) {
	s, database := testStore(t)
	householdID := createTestHousehold(t, database)

	l, err := s.Add("7770001", "ToDelete", householdID)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := s.Delete(l.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = s.GetByID(l.ID)
	if err == nil {
		t.Error("expected error after deletion, got nil")
	}

	// Deleting again should fail
	if err := s.Delete(l.ID); err == nil {
		t.Error("expected error deleting non-existent line, got nil")
	}
}

func TestUpdate(t *testing.T) {
	s, database := testStore(t)
	householdID := createTestHousehold(t, database)

	l, err := s.Add("6660001", "Original", householdID)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := s.Update(l.ID, "6660002", "Updated"); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := s.GetByID(l.ID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if got.Number != "6660002" {
		t.Errorf("Number = %q, want 6660002", got.Number)
	}
	if got.Name != "Updated" {
		t.Errorf("Name = %q, want Updated", got.Name)
	}
	if !got.UpdatedAt.After(l.UpdatedAt) {
		t.Error("UpdatedAt should be later after update")
	}
}

func TestGetHouseholdIDByNumber(t *testing.T) {
	s, database := testStore(t)
	householdID := createTestHousehold(t, database)

	if _, err := s.Add("5550001", "HHTest", householdID); err != nil {
		t.Fatalf("Add: %v", err)
	}

	got, err := s.GetHouseholdIDByNumber("5550001")
	if err != nil {
		t.Fatalf("GetHouseholdIDByNumber: %v", err)
	}
	if got != householdID {
		t.Errorf("HouseholdID = %q, want %q", got, householdID)
	}

	// Non-existent number should return error
	_, err = s.GetHouseholdIDByNumber("0000000")
	if err == nil {
		t.Error("expected error for non-existent number, got nil")
	}
}
