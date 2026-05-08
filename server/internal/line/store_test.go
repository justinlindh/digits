//go:build integration

package line

import (
	"context"
	"database/sql"
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
		_, _ = database.DB.Exec("DELETE FROM lines")
		_, _ = database.DB.Exec("DELETE FROM household_members")
		_, _ = database.DB.Exec("DELETE FROM households")
		_, _ = database.DB.Exec("DELETE FROM sessions")
		_, _ = database.DB.Exec("DELETE FROM magic_links")
		_, _ = database.DB.Exec("DELETE FROM users")
		_ = database.Close()
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

	l, err := s.Add(context.Background(), "5551234", "Alice", householdID)
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

	got, err := s.GetByID(context.Background(), l.ID)
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

	_, err := s.Add(context.Background(), "5559876", "Bob", householdID)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	got, err := s.GetByNumber(context.Background(), "5559876")
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

	if _, err := s.Add(context.Background(), "1110001", "Line A", householdID); err != nil {
		t.Fatalf("Add A: %v", err)
	}
	if _, err := s.Add(context.Background(), "1110002", "Line B", householdID); err != nil {
		t.Fatalf("Add B: %v", err)
	}
	if _, err := s.Add(context.Background(), "1110003", "Line Other", otherHouseholdID); err != nil {
		t.Fatalf("Add Other: %v", err)
	}

	lines, err := s.ListByHousehold(context.Background(), householdID)
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

	if _, err := s.Add(context.Background(), "9990001", "First", householdID); err != nil {
		t.Fatalf("Add first: %v", err)
	}

	// Adding the same number should fail due to UNIQUE constraint
	_, err := s.Add(context.Background(), "9990001", "Duplicate", householdID)
	if err == nil {
		t.Error("expected error for duplicate number, got nil")
	}
}

func TestNumberExists(t *testing.T) {
	s, database := testStore(t)
	householdID := createTestHousehold(t, database)

	exists, err := s.NumberExists(context.Background(), "8880001")
	if err != nil {
		t.Fatalf("NumberExists before add: %v", err)
	}
	if exists {
		t.Error("number should not exist before being added")
	}

	l, err := s.Add(context.Background(), "8880001", "Test", householdID)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	exists, err = s.NumberExists(context.Background(), "8880001")
	if err != nil {
		t.Fatalf("NumberExists after add: %v", err)
	}
	if !exists {
		t.Error("number should exist after being added")
	}

	// NumberExistsExcluding should return false for the same ID
	exists, err = s.NumberExistsExcluding(context.Background(), "8880001", l.ID)
	if err != nil {
		t.Fatalf("NumberExistsExcluding: %v", err)
	}
	if exists {
		t.Error("number should not exist when excluding its own ID")
	}

	// NumberExistsExcluding should return true for a different ID
	exists, err = s.NumberExistsExcluding(context.Background(), "8880001", l.ID+999)
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

	l, err := s.Add(context.Background(), "7770001", "ToDelete", householdID)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := s.Delete(context.Background(), l.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = s.GetByID(context.Background(), l.ID)
	if err == nil {
		t.Error("expected error after deletion, got nil")
	}

	// Deleting again should fail
	if err := s.Delete(context.Background(), l.ID); err == nil {
		t.Error("expected error deleting non-existent line, got nil")
	}
}

func TestUpdate(t *testing.T) {
	s, database := testStore(t)
	householdID := createTestHousehold(t, database)

	l, err := s.Add(context.Background(), "6660001", "Original", householdID)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := s.Update(context.Background(), l.ID, "6660002", "Updated"); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := s.GetByID(context.Background(), l.ID)
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

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"3140001", "314-0001"},
		{"5551234", "555-1234"},
		{"1234567", "123-4567"},
		{"short", "short"},       // too short; returned as-is
		{"12345678", "12345678"}, // too long; returned as-is
	}
	for _, tt := range tests {
		got := FormatNumber(tt.input)
		if got != tt.want {
			t.Errorf("FormatNumber(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestValidateNumber(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"3140001", false},
		{"314-0001", false}, // hyphenated form accepted
		{"12345", true},     // too short
		{"12345678", true},  // too long
		{"314-00001", true}, // too many digits
		{"3-140001", true},  // hyphen in wrong position
		{"abcdefg", true},   // non-digits
	}
	for _, tt := range tests {
		err := ValidateNumber(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateNumber(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
		}
	}
}

// testStoreWithHousehold bundles a Store, the raw *sql.DB, and a pre-created
// household ID for tests that need all three.
type testStoreWithHousehold struct {
	store       *Store
	rawDB       *sql.DB
	householdID string
}

// newTestStoreWithHousehold creates a testStoreWithHousehold using the same
// skip-if-no-DB pattern as testStore.
func newTestStoreWithHousehold(t *testing.T) (*testStoreWithHousehold, func()) {
	t.Helper()
	s, database := testStore(t)
	householdID := createTestHousehold(t, database)
	return &testStoreWithHousehold{
		store:       s,
		rawDB:       database.DB,
		householdID: householdID,
	}, func() {}
}

func TestStoreSettingsDefaultAndUpdate(t *testing.T) {
	store, cleanup := newTestStoreWithHousehold(t)
	defer cleanup()

	ln, err := store.store.Add(context.Background(), "555-0101", "Test", store.householdID)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Newly created line should come back with default-filled settings.
	got, err := store.store.GetByID(context.Background(), ln.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Settings.VoiceStyle != VoiceStyleCopper {
		t.Errorf("new line default VoiceStyle: got %q, want %q", got.Settings.VoiceStyle, VoiceStyleCopper)
	}

	// Update the setting.
	if err := store.store.UpdateSettings(context.Background(), ln.ID, Settings{VoiceStyle: VoiceStyleModern}); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	got, err = store.store.GetByID(context.Background(), ln.ID)
	if err != nil {
		t.Fatalf("reget: %v", err)
	}
	if got.Settings.VoiceStyle != VoiceStyleModern {
		t.Errorf("after update: got %q, want %q", got.Settings.VoiceStyle, VoiceStyleModern)
	}

	// Empty settings in DB should still return default after load.
	if _, err := store.rawDB.Exec(`UPDATE lines SET settings = '{}' WHERE id = $1`, ln.ID); err != nil {
		t.Fatalf("reset settings: %v", err)
	}
	got, err = store.store.GetByID(context.Background(), ln.ID)
	if err != nil {
		t.Fatalf("reget after reset: %v", err)
	}
	if got.Settings.VoiceStyle != VoiceStyleCopper {
		t.Errorf("empty json should fall through to default, got %q", got.Settings.VoiceStyle)
	}
}

func TestGetHouseholdIDByNumber(t *testing.T) {
	s, database := testStore(t)
	householdID := createTestHousehold(t, database)

	if _, err := s.Add(context.Background(), "5550001", "HHTest", householdID); err != nil {
		t.Fatalf("Add: %v", err)
	}

	got, err := s.GetHouseholdIDByNumber(context.Background(), "5550001")
	if err != nil {
		t.Fatalf("GetHouseholdIDByNumber: %v", err)
	}
	if got != householdID {
		t.Errorf("HouseholdID = %q, want %q", got, householdID)
	}

	// Non-existent number should return error
	_, err = s.GetHouseholdIDByNumber(context.Background(), "0000000")
	if err == nil {
		t.Error("expected error for non-existent number, got nil")
	}
}

func TestSetAllSilentByHousehold(t *testing.T) {
	store, cleanup := newTestStoreWithHousehold(t)
	defer cleanup()

	ctx := context.Background()
	l1, err := store.store.Add(ctx, "555-0201", "Kitchen", store.householdID)
	if err != nil {
		t.Fatalf("add l1: %v", err)
	}
	l2, err := store.store.Add(ctx, "555-0202", "Bedroom", store.householdID)
	if err != nil {
		t.Fatalf("add l2: %v", err)
	}

	// Batch-set all to silent.
	if err := store.store.SetAllSilentByHousehold(ctx, store.householdID, true); err != nil {
		t.Fatalf("SetAllSilentByHousehold(true): %v", err)
	}
	got1, _ := store.store.GetByID(ctx, l1.ID)
	got2, _ := store.store.GetByID(ctx, l2.ID)
	if !got1.Settings.SilentMode {
		t.Error("l1 SilentMode should be true after batch-set")
	}
	if !got2.Settings.SilentMode {
		t.Error("l2 SilentMode should be true after batch-set")
	}

	// Batch-set all to not silent.
	if err := store.store.SetAllSilentByHousehold(ctx, store.householdID, false); err != nil {
		t.Fatalf("SetAllSilentByHousehold(false): %v", err)
	}
	got1, _ = store.store.GetByID(ctx, l1.ID)
	got2, _ = store.store.GetByID(ctx, l2.ID)
	if got1.Settings.SilentMode {
		t.Error("l1 SilentMode should be false after batch-unset")
	}
	if got2.Settings.SilentMode {
		t.Error("l2 SilentMode should be false after batch-unset")
	}
}

func TestAllSilentByHousehold(t *testing.T) {
	store, cleanup := newTestStoreWithHousehold(t)
	defer cleanup()

	ctx := context.Background()

	// No lines: should return false.
	got, err := store.store.AllSilentByHousehold(ctx, store.householdID)
	if err != nil {
		t.Fatalf("AllSilentByHousehold (no lines): %v", err)
	}
	if got {
		t.Error("expected false when household has no lines")
	}

	l1, err := store.store.Add(ctx, "555-0301", "Kitchen", store.householdID)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := store.store.Add(ctx, "555-0302", "Bedroom", store.householdID); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Default: both not silent.
	got, _ = store.store.AllSilentByHousehold(ctx, store.householdID)
	if got {
		t.Error("expected false when no lines are silent")
	}

	// Silence one: still false.
	_ = store.store.UpdateSettings(ctx, l1.ID, Settings{VoiceStyle: VoiceStyleCopper, SilentMode: true})
	got, _ = store.store.AllSilentByHousehold(ctx, store.householdID)
	if got {
		t.Error("expected false when only one of two lines is silent")
	}

	// Silence all: true.
	_ = store.store.SetAllSilentByHousehold(ctx, store.householdID, true)
	got, _ = store.store.AllSilentByHousehold(ctx, store.householdID)
	if !got {
		t.Error("expected true when all lines are silent")
	}
}
