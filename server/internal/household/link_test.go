//go:build integration

package household

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// testDB opens a database and runs migrations for testing.
func testLinkDB(t *testing.T) *sql.DB {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Run the minimal schema needed for tests
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			email TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL DEFAULT '',
			google_id TEXT UNIQUE,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			last_login_at TIMESTAMPTZ
		)`,
		`CREATE TABLE IF NOT EXISTS households (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name TEXT NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS household_links (
			id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			household_a_id   UUID NOT NULL REFERENCES households(id) ON DELETE CASCADE,
			household_b_id   UUID REFERENCES households(id) ON DELETE CASCADE,
			status           TEXT NOT NULL DEFAULT 'pending',
			invite_code      TEXT NOT NULL UNIQUE,
			invited_by       UUID NOT NULL REFERENCES users(id),
			accepted_by      UUID REFERENCES users(id),
			created_at       TIMESTAMPTZ DEFAULT NOW(),
			accepted_at      TIMESTAMPTZ,
			revoked_at       TIMESTAMPTZ,
			revoked_by       UUID REFERENCES users(id),
			CHECK (household_b_id IS NULL OR household_a_id < household_b_id)
		)`,
	}
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			t.Fatalf("migration failed: %v\nSQL: %s", err, m)
		}
	}
	return db
}

// createLinkTestUser inserts a test user and returns its ID.
func createLinkTestUser(t *testing.T, db *sql.DB, suffix string) string {
	t.Helper()
	var id string
	email := fmt.Sprintf("linktest-%s-%d@example.com", suffix, time.Now().UnixNano())
	err := db.QueryRow(
		`INSERT INTO users (email, name) VALUES ($1, $2) RETURNING id`,
		email, "Test User",
	).Scan(&id)
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return id
}

// createLinkTestHousehold inserts a test household and returns its ID.
func createLinkTestHousehold(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	var id string
	uniqName := fmt.Sprintf("%s-%d", name, time.Now().UnixNano())
	err := db.QueryRow(
		`INSERT INTO households (name) VALUES ($1) RETURNING id`,
		uniqName,
	).Scan(&id)
	if err != nil {
		t.Fatalf("create test household: %v", err)
	}
	return id
}

func TestCreateInvite_ReturnsValidLink(t *testing.T) {
	db := testLinkDB(t)
	store := NewLinkStore(db)

	userID := createLinkTestUser(t, db, "create-invite")
	householdID := createLinkTestHousehold(t, db, "Household A")

	link, err := store.CreateInvite(context.Background(), householdID, userID)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if link.ID == "" {
		t.Error("expected non-empty ID")
	}
	if link.InviteCode == "" || len(link.InviteCode) != inviteCodeLength {
		t.Errorf("expected %d-char invite code, got %q", inviteCodeLength, link.InviteCode)
	}
	if link.Status != "pending" {
		t.Errorf("expected status 'pending', got %q", link.Status)
	}
	if link.HouseholdAID != householdID {
		t.Errorf("expected household_a_id = %q, got %q", householdID, link.HouseholdAID)
	}
	if link.HouseholdBID != nil {
		t.Errorf("expected nil household_b_id for pending invite, got %q", *link.HouseholdBID)
	}
}

func TestAcceptInvite_ActivatesLink(t *testing.T) {
	db := testLinkDB(t)
	store := NewLinkStore(db)

	userA := createLinkTestUser(t, db, "accept-a")
	userB := createLinkTestUser(t, db, "accept-b")
	hA := createLinkTestHousehold(t, db, "Accept Household A")
	hB := createLinkTestHousehold(t, db, "Accept Household B")

	invite, err := store.CreateInvite(context.Background(), hA, userA)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}

	link, err := store.AcceptInvite(context.Background(), invite.InviteCode, userB, hB)
	if err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}
	if link.Status != "active" {
		t.Errorf("expected status 'active', got %q", link.Status)
	}
	if link.HouseholdBID == nil {
		t.Fatal("expected non-nil household_b_id after accept")
	}
	if link.AcceptedBy == nil || *link.AcceptedBy != userB {
		t.Errorf("expected accepted_by = %q", userB)
	}
	if link.AcceptedAt == nil {
		t.Error("expected non-nil accepted_at")
	}
	// Verify normalization: a < b
	if link.HouseholdAID >= *link.HouseholdBID {
		t.Errorf("expected household_a_id < household_b_id, got %q >= %q", link.HouseholdAID, *link.HouseholdBID)
	}
}

func TestAcceptInvite_FailsOnInvalidCode(t *testing.T) {
	db := testLinkDB(t)
	store := NewLinkStore(db)

	userB := createLinkTestUser(t, db, "invalid-code")
	hB := createLinkTestHousehold(t, db, "Invalid Code Household")

	_, err := store.AcceptInvite(context.Background(), "NOTVALID", userB, hB)
	if err == nil {
		t.Fatal("expected error for invalid invite code, got nil")
	}
}

func TestAreLinked_TrueAfterAccept(t *testing.T) {
	db := testLinkDB(t)
	store := NewLinkStore(db)

	userA := createLinkTestUser(t, db, "linked-a")
	userB := createLinkTestUser(t, db, "linked-b")
	hA := createLinkTestHousehold(t, db, "Linked Household A")
	hB := createLinkTestHousehold(t, db, "Linked Household B")

	invite, err := store.CreateInvite(context.Background(), hA, userA)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if _, err := store.AcceptInvite(context.Background(), invite.InviteCode, userB, hB); err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}

	linked, err := store.AreLinked(context.Background(), hA, hB)
	if err != nil {
		t.Fatalf("AreLinked: %v", err)
	}
	if !linked {
		t.Error("expected households to be linked")
	}

	// Also test reverse order
	linked, err = store.AreLinked(context.Background(), hB, hA)
	if err != nil {
		t.Fatalf("AreLinked (reverse): %v", err)
	}
	if !linked {
		t.Error("expected households to be linked (reverse order)")
	}
}

func TestRevokeLink_ChangesStatus(t *testing.T) {
	db := testLinkDB(t)
	store := NewLinkStore(db)

	userA := createLinkTestUser(t, db, "revoke-a")
	userB := createLinkTestUser(t, db, "revoke-b")
	hA := createLinkTestHousehold(t, db, "Revoke Household A")
	hB := createLinkTestHousehold(t, db, "Revoke Household B")

	invite, err := store.CreateInvite(context.Background(), hA, userA)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	link, err := store.AcceptInvite(context.Background(), invite.InviteCode, userB, hB)
	if err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}

	if err := store.RevokeLink(context.Background(), link.ID, userA); err != nil {
		t.Fatalf("RevokeLink: %v", err)
	}

	updated, err := store.GetByID(context.Background(), link.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.Status != "revoked" {
		t.Errorf("expected status 'revoked', got %q", updated.Status)
	}
	if updated.RevokedAt == nil {
		t.Error("expected non-nil revoked_at")
	}
	if updated.RevokedBy == nil || *updated.RevokedBy != userA {
		t.Errorf("expected revoked_by = %q", userA)
	}
}

func TestCreateInvite_NoDuplicateLinks(t *testing.T) {
	db := testLinkDB(t)
	store := NewLinkStore(db)

	userA := createLinkTestUser(t, db, "nodup-a")
	userB := createLinkTestUser(t, db, "nodup-b")
	hA := createLinkTestHousehold(t, db, "NoDup Household A")
	hB := createLinkTestHousehold(t, db, "NoDup Household B")

	// Create and accept a link between hA and hB
	invite, err := store.CreateInvite(context.Background(), hA, userA)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if _, err := store.AcceptInvite(context.Background(), invite.InviteCode, userB, hB); err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}

	// Now hA tries to create another invite — hB then tries to accept from a different direction
	// The real duplicate prevention is in AcceptInvite via AreLinked check.
	// Create a new invite from hB (now it can create a pending one since no pending exists for hB)
	invite2, err := store.CreateInvite(context.Background(), hB, userB)
	if err != nil {
		t.Fatalf("CreateInvite from hB: %v", err)
	}

	// Trying to accept with hA should fail (already linked)
	_, err = store.AcceptInvite(context.Background(), invite2.InviteCode, userA, hA)
	if err == nil {
		t.Fatal("expected error when accepting invite between already-linked households, got nil")
	}
}
