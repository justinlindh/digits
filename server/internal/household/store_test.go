package household

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
	s := NewStore(database.DB)
	t.Cleanup(func() {
		database.DB.Exec("DELETE FROM household_members")
		database.DB.Exec("DELETE FROM households")
		database.DB.Exec("DELETE FROM sessions")
		database.DB.Exec("DELETE FROM magic_links")
		database.DB.Exec("DELETE FROM users")
		database.Close()
	})
	return s, database
}

// createTestUser inserts a minimal user row and returns its UUID.
func createTestUser(t *testing.T, database *db.Database, email string) string {
	t.Helper()
	var id string
	err := database.DB.QueryRow(
		`INSERT INTO users (email, name) VALUES ($1, $2) RETURNING id`,
		email, "Test User",
	).Scan(&id)
	if err != nil {
		t.Fatalf("createTestUser: %v", err)
	}
	return id
}

func TestCreate(t *testing.T) {
	s, database := testStore(t)
	userID := createTestUser(t, database, "owner@example.com")

	h, err := s.Create("Smith Family", userID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if h.ID == "" {
		t.Error("household ID should not be empty")
	}
	if h.Name != "Smith Family" {
		t.Errorf("name = %q, want Smith Family", h.Name)
	}

	// GetByID should return the same household
	got, err := s.GetByID(h.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != h.ID {
		t.Errorf("ID mismatch: got %s, want %s", got.ID, h.ID)
	}
	if got.Name != h.Name {
		t.Errorf("name mismatch: got %q, want %q", got.Name, h.Name)
	}
}

func TestGetForUser(t *testing.T) {
	s, database := testStore(t)
	userID := createTestUser(t, database, "member@example.com")

	h, err := s.Create("Jones Family", userID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	households, err := s.GetForUser(userID)
	if err != nil {
		t.Fatalf("GetForUser: %v", err)
	}
	if len(households) != 1 {
		t.Fatalf("expected 1 household, got %d", len(households))
	}
	if households[0].ID != h.ID {
		t.Errorf("household ID mismatch: got %s, want %s", households[0].ID, h.ID)
	}
	if households[0].Name != "Jones Family" {
		t.Errorf("name = %q, want Jones Family", households[0].Name)
	}
}

func TestGetForUser_Empty(t *testing.T) {
	s, database := testStore(t)
	userID := createTestUser(t, database, "loner@example.com")

	households, err := s.GetForUser(userID)
	if err != nil {
		t.Fatalf("GetForUser: %v", err)
	}
	if len(households) != 0 {
		t.Errorf("expected 0 households, got %d", len(households))
	}
}

func TestGetRole(t *testing.T) {
	s, database := testStore(t)
	ownerID := createTestUser(t, database, "admin@example.com")

	h, err := s.Create("Admin Family", ownerID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	role, err := s.GetRole(ownerID, h.ID)
	if err != nil {
		t.Fatalf("GetRole: %v", err)
	}
	if role != "admin" {
		t.Errorf("role = %q, want admin", role)
	}
}

func TestGetRole_NotMember(t *testing.T) {
	s, database := testStore(t)
	ownerID := createTestUser(t, database, "owner2@example.com")
	otherID := createTestUser(t, database, "stranger@example.com")

	h, err := s.Create("Private Family", ownerID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = s.GetRole(otherID, h.ID)
	if err == nil {
		t.Error("expected error for non-member, got nil")
	}
}

func TestAddMember(t *testing.T) {
	s, database := testStore(t)
	ownerID := createTestUser(t, database, "addowner@example.com")
	memberID := createTestUser(t, database, "newmember@example.com")

	h, err := s.Create("Growing Family", ownerID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.AddMember(memberID, h.ID, "member"); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	role, err := s.GetRole(memberID, h.ID)
	if err != nil {
		t.Fatalf("GetRole after AddMember: %v", err)
	}
	if role != "member" {
		t.Errorf("role = %q, want member", role)
	}

	// Update role via AddMember (ON CONFLICT DO UPDATE)
	if err := s.AddMember(memberID, h.ID, "admin"); err != nil {
		t.Fatalf("AddMember update role: %v", err)
	}
	role, err = s.GetRole(memberID, h.ID)
	if err != nil {
		t.Fatalf("GetRole after role update: %v", err)
	}
	if role != "admin" {
		t.Errorf("updated role = %q, want admin", role)
	}
}

func TestUpdateName(t *testing.T) {
	s, database := testStore(t)
	userID := createTestUser(t, database, "rename@example.com")

	h, err := s.Create("Old Name", userID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.UpdateName(h.ID, "New Name"); err != nil {
		t.Fatalf("UpdateName: %v", err)
	}

	got, err := s.GetByID(h.ID)
	if err != nil {
		t.Fatalf("GetByID after rename: %v", err)
	}
	if got.Name != "New Name" {
		t.Errorf("name = %q, want New Name", got.Name)
	}
}
