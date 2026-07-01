//go:build integration

package household

import (
	"context"
	"errors"
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
		t.Skip("TEST_DATABASE_URL not set, skipping DB tests")
	}
	database, err := db.Open(dsn)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	s := NewStore(database.DB)
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM household_invites")
		_, _ = database.DB.Exec("DELETE FROM household_members")
		_, _ = database.DB.Exec("DELETE FROM households")
		_, _ = database.DB.Exec("DELETE FROM sessions")
		_, _ = database.DB.Exec("DELETE FROM magic_links")
		_, _ = database.DB.Exec("DELETE FROM users")
		_ = database.Close()
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

	h, err := s.Create(context.Background(), "Smith Family", userID)
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
	got, err := s.GetByID(context.Background(), h.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != h.ID {
		t.Errorf("ID mismatch: got %s, want %s", got.ID, h.ID)
	}
	if got.Name != h.Name {
		t.Errorf("name mismatch: got %q, want %q", got.Name, h.Name)
	}
	if got.Timezone != "UTC" {
		t.Errorf("default timezone = %q, want UTC", got.Timezone)
	}
}

func TestGetForUser(t *testing.T) {
	s, database := testStore(t)
	userID := createTestUser(t, database, "member@example.com")

	h, err := s.Create(context.Background(), "Jones Family", userID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	households, err := s.GetForUser(context.Background(), userID)
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

	households, err := s.GetForUser(context.Background(), userID)
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

	h, err := s.Create(context.Background(), "Admin Family", ownerID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	role, err := s.GetRole(context.Background(), ownerID, h.ID)
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

	h, err := s.Create(context.Background(), "Private Family", ownerID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = s.GetRole(context.Background(), otherID, h.ID)
	if !errors.Is(err, ErrNotMember) {
		t.Errorf("expected ErrNotMember, got %v", err)
	}
}

func TestAddMember(t *testing.T) {
	s, database := testStore(t)
	ownerID := createTestUser(t, database, "addowner@example.com")
	memberID := createTestUser(t, database, "newmember@example.com")

	h, err := s.Create(context.Background(), "Growing Family", ownerID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.AddMember(context.Background(), memberID, h.ID, "member"); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	role, err := s.GetRole(context.Background(), memberID, h.ID)
	if err != nil {
		t.Fatalf("GetRole after AddMember: %v", err)
	}
	if role != "member" {
		t.Errorf("role = %q, want member", role)
	}

	// Update role via AddMember (ON CONFLICT DO UPDATE)
	if err := s.AddMember(context.Background(), memberID, h.ID, "admin"); err != nil {
		t.Fatalf("AddMember update role: %v", err)
	}
	role, err = s.GetRole(context.Background(), memberID, h.ID)
	if err != nil {
		t.Fatalf("GetRole after role update: %v", err)
	}
	if role != "admin" {
		t.Errorf("updated role = %q, want admin", role)
	}
}

func TestSetTimezone(t *testing.T) {
	s, database := testStore(t)
	userID := createTestUser(t, database, "tz@example.com")

	h, err := s.Create(context.Background(), "TZ Family", userID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Default should be UTC
	got, err := s.GetByID(context.Background(), h.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Timezone != "UTC" {
		t.Errorf("default timezone = %q, want UTC", got.Timezone)
	}

	// Set valid timezone
	if err := s.SetTimezone(context.Background(), h.ID, "America/Denver"); err != nil {
		t.Fatalf("SetTimezone: %v", err)
	}
	got, err = s.GetByID(context.Background(), h.ID)
	if err != nil {
		t.Fatalf("GetByID after set: %v", err)
	}
	if got.Timezone != "America/Denver" {
		t.Errorf("timezone = %q, want America/Denver", got.Timezone)
	}
}

func TestSetTimezone_Invalid(t *testing.T) {
	s, database := testStore(t)
	userID := createTestUser(t, database, "badtz@example.com")

	h, err := s.Create(context.Background(), "Bad TZ Family", userID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	err = s.SetTimezone(context.Background(), h.ID, "Not/A/Timezone")
	if err == nil {
		t.Error("expected error for invalid timezone, got nil")
	}
}

func TestUpdateName(t *testing.T) {
	s, database := testStore(t)
	userID := createTestUser(t, database, "rename@example.com")

	h, err := s.Create(context.Background(), "Old Name", userID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.UpdateName(context.Background(), h.ID, "New Name"); err != nil {
		t.Fatalf("UpdateName: %v", err)
	}

	got, err := s.GetByID(context.Background(), h.ID)
	if err != nil {
		t.Fatalf("GetByID after rename: %v", err)
	}
	if got.Name != "New Name" {
		t.Errorf("name = %q, want New Name", got.Name)
	}
}

func TestRemoveMember(t *testing.T) {
	s, database := testStore(t)
	ownerID := createTestUser(t, database, "owner-rm@example.com")
	memberID := createTestUser(t, database, "member-rm@example.com")

	hh, err := s.Create(context.Background(), "Remove Test", ownerID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.AddMember(context.Background(), memberID, hh.ID, "admin"); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	count, err := s.MemberCount(context.Background(), hh.ID)
	if err != nil {
		t.Fatalf("MemberCount: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 members, got %d", count)
	}

	if err := s.RemoveMember(context.Background(), memberID, hh.ID); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}

	count, err = s.MemberCount(context.Background(), hh.ID)
	if err != nil {
		t.Fatalf("MemberCount after remove: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 member after remove, got %d", count)
	}
}

func TestMemberCount(t *testing.T) {
	s, database := testStore(t)
	ownerID := createTestUser(t, database, "owner-cnt@example.com")

	hh, err := s.Create(context.Background(), "Count Test", ownerID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	count, err := s.MemberCount(context.Background(), hh.ID)
	if err != nil {
		t.Fatalf("MemberCount: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1, got %d", count)
	}
}

func TestStore_Delete(t *testing.T) {
	s, database := testStore(t)
	ctx := context.Background()
	userID := createTestUser(t, database, "delete-owner@example.com")
	t.Cleanup(func() {
		_, _ = database.DB.Exec(`DELETE FROM users WHERE id = $1`, userID)
	})

	h, err := s.Create(ctx, "Delete Me Family", userID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Insert a line and device via raw SQL
	var lineID int
	err = database.DB.QueryRow(
		`INSERT INTO lines (number, name, household_id) VALUES ($1, $2, $3) RETURNING id`,
		"store-delete-test-"+h.ID, "Test Line", h.ID,
	).Scan(&lineID)
	if err != nil {
		t.Fatalf("insert line: %v", err)
	}

	var deviceID int
	err = database.DB.QueryRow(
		`INSERT INTO devices (line_id, device_id) VALUES ($1, $2) RETURNING id`,
		lineID, "test-device-hw",
	).Scan(&deviceID)
	if err != nil {
		t.Fatalf("insert device: %v", err)
	}

	if err := s.Delete(ctx, h.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Household should be gone
	var count int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM households WHERE id = $1`, h.ID).Scan(&count); err != nil {
		t.Fatalf("count households: %v", err)
	}
	if count != 0 {
		t.Errorf("household still exists after Delete")
	}

	// Members should be gone (CASCADE)
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM household_members WHERE household_id = $1`, h.ID).Scan(&count); err != nil {
		t.Fatalf("count members: %v", err)
	}
	if count != 0 {
		t.Errorf("household_members still exist after Delete")
	}

	// Lines should be gone (CASCADE)
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM lines WHERE id = $1`, lineID).Scan(&count); err != nil {
		t.Fatalf("count lines: %v", err)
	}
	if count != 0 {
		t.Errorf("line still exists after Delete")
	}

	// Devices should be gone (CASCADE via lines)
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE id = $1`, deviceID).Scan(&count); err != nil {
		t.Fatalf("count devices: %v", err)
	}
	if count != 0 {
		t.Errorf("device still exists after Delete")
	}
}

func TestStore_Delete_NotFound(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()

	err := s.Delete(ctx, "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestGetMembersWithUsers(t *testing.T) {
	s, database := testStore(t)
	ctx := context.Background()
	ownerID := createTestUser(t, database, "members-owner@example.com")
	memberID := createTestUser(t, database, "members-member@example.com")

	h, err := s.Create(ctx, "Members Family", ownerID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.AddMember(ctx, memberID, h.ID, "member"); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	members, err := s.GetMembersWithUsers(ctx, h.ID)
	if err != nil {
		t.Fatalf("GetMembersWithUsers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}

	byEmail := make(map[string]MemberWithUser, len(members))
	for _, m := range members {
		byEmail[m.Email] = m
	}
	owner, ok := byEmail["members-owner@example.com"]
	if !ok {
		t.Fatal("owner missing from members")
	}
	if owner.Role != "admin" {
		t.Errorf("owner role = %q, want admin", owner.Role)
	}
	if owner.UserID != ownerID {
		t.Errorf("owner UserID = %q, want %q", owner.UserID, ownerID)
	}
	if member, ok := byEmail["members-member@example.com"]; !ok || member.Role != "member" {
		t.Errorf("member entry = %+v, ok=%v, want role member", member, ok)
	}
}

func TestIsMemberByEmail(t *testing.T) {
	s, database := testStore(t)
	ctx := context.Background()
	ownerID := createTestUser(t, database, "Mixed.Case@Example.com")

	h, err := s.Create(ctx, "Email Family", ownerID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Lookup is case-insensitive on both sides.
	ok, err := s.IsMemberByEmail(ctx, h.ID, "mixed.case@example.com")
	if err != nil {
		t.Fatalf("IsMemberByEmail: %v", err)
	}
	if !ok {
		t.Error("expected case-insensitive match for existing member")
	}

	ok, err = s.IsMemberByEmail(ctx, h.ID, "stranger@example.com")
	if err != nil {
		t.Fatalf("IsMemberByEmail: %v", err)
	}
	if ok {
		t.Error("expected non-member to report false")
	}
}

func TestCountHouseholds(t *testing.T) {
	s, database := testStore(t)
	ctx := context.Background()

	before, err := s.CountHouseholds(ctx)
	if err != nil {
		t.Fatalf("CountHouseholds: %v", err)
	}

	ownerID := createTestUser(t, database, "count-owner@example.com")
	if _, err := s.Create(ctx, "Count A", ownerID); err != nil {
		t.Fatalf("Create A: %v", err)
	}
	if _, err := s.Create(ctx, "Count B", ownerID); err != nil {
		t.Fatalf("Create B: %v", err)
	}

	after, err := s.CountHouseholds(ctx)
	if err != nil {
		t.Fatalf("CountHouseholds: %v", err)
	}
	if after != before+2 {
		t.Errorf("CountHouseholds = %d, want %d", after, before+2)
	}
}

func TestSetCallHistoryEnabled(t *testing.T) {
	s, database := testStore(t)
	ctx := context.Background()
	ownerID := createTestUser(t, database, "callhist-owner@example.com")

	h, err := s.Create(ctx, "Call History Family", ownerID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	readFlag := func() bool {
		t.Helper()
		got, err := s.GetByID(ctx, h.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		return got.CallHistoryEnabled
	}

	if err := s.SetCallHistoryEnabled(ctx, h.ID, false); err != nil {
		t.Fatalf("SetCallHistoryEnabled(false): %v", err)
	}
	if readFlag() {
		t.Error("expected call_history_enabled to be false")
	}

	if err := s.SetCallHistoryEnabled(ctx, h.ID, true); err != nil {
		t.Fatalf("SetCallHistoryEnabled(true): %v", err)
	}
	if !readFlag() {
		t.Error("expected call_history_enabled to be true")
	}
}
