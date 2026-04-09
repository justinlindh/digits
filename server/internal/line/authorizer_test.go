package line

import (
	"os"
	"testing"

	"github.com/justinlindh/digits/server/internal/db"
)

// testAuthorizer creates an Authorizer connected to the test database.
// Tests are skipped if TEST_DATABASE_URL is not set.
func testAuthorizer(t *testing.T) (*Authorizer, *db.Database) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping DB tests")
	}
	database, err := db.Open(dsn)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM devices")
		_, _ = database.DB.Exec("DELETE FROM lines")
		_, _ = database.DB.Exec("DELETE FROM household_links")
		_, _ = database.DB.Exec("DELETE FROM household_members")
		_, _ = database.DB.Exec("DELETE FROM households")
		_, _ = database.DB.Exec("DELETE FROM sessions")
		_, _ = database.DB.Exec("DELETE FROM magic_links")
		_, _ = database.DB.Exec("DELETE FROM users")
		_ = database.Close()
	})
	return NewAuthorizer(database), database
}

// createAuthTestUser inserts a minimal user and returns its UUID.
func createAuthTestUser(t *testing.T, database *db.Database) string {
	t.Helper()
	var id string
	err := database.DB.QueryRow(
		`INSERT INTO users (email, name) VALUES ('authtest-' || gen_random_uuid() || '@example.com', 'Test User') RETURNING id`,
	).Scan(&id)
	if err != nil {
		t.Fatalf("createAuthTestUser: %v", err)
	}
	return id
}

// linkHouseholds creates an active household_link between two households.
// It handles the household_a_id < household_b_id constraint automatically.
func linkHouseholds(t *testing.T, database *db.Database, houseA, houseB, userID string) {
	t.Helper()
	a, b := houseA, houseB
	if a > b {
		a, b = b, a
	}
	_, err := database.DB.Exec(
		`INSERT INTO household_links (household_a_id, household_b_id, status, invite_code, invited_by, accepted_by, accepted_at)
		 VALUES ($1, $2, 'active', gen_random_uuid()::text, $3, $3, NOW())`,
		a, b, userID,
	)
	if err != nil {
		t.Fatalf("linkHouseholds: %v", err)
	}
}

func TestCanCall_SameHousehold(t *testing.T) {
	auth, database := testAuthorizer(t)
	householdID := createTestHousehold(t, database)

	store := NewStore(database)
	if _, err := store.Add("1000001", "Alice", householdID); err != nil {
		t.Fatalf("Add Alice: %v", err)
	}
	if _, err := store.Add("1000002", "Bob", householdID); err != nil {
		t.Fatalf("Add Bob: %v", err)
	}

	allowed, err := auth.CanCall("1000001", "1000002")
	if err != nil {
		t.Fatalf("CanCall: %v", err)
	}
	if !allowed {
		t.Error("expected call within same household to be allowed")
	}

	// Verify the reverse direction works too
	allowed, err = auth.CanCall("1000002", "1000001")
	if err != nil {
		t.Fatalf("CanCall (reverse): %v", err)
	}
	if !allowed {
		t.Error("expected reverse call within same household to be allowed")
	}
}

func TestCanCall_LinkedHouseholds(t *testing.T) {
	auth, database := testAuthorizer(t)
	houseA := createTestHousehold(t, database)
	houseB := createTestHousehold(t, database)
	userID := createAuthTestUser(t, database)

	store := NewStore(database)
	if _, err := store.Add("2000001", "Alice", houseA); err != nil {
		t.Fatalf("Add Alice: %v", err)
	}
	if _, err := store.Add("2000002", "Bob", houseB); err != nil {
		t.Fatalf("Add Bob: %v", err)
	}

	linkHouseholds(t, database, houseA, houseB, userID)

	allowed, err := auth.CanCall("2000001", "2000002")
	if err != nil {
		t.Fatalf("CanCall: %v", err)
	}
	if !allowed {
		t.Error("expected call between linked households to be allowed")
	}

	// Verify the reverse direction works too
	allowed, err = auth.CanCall("2000002", "2000001")
	if err != nil {
		t.Fatalf("CanCall (reverse): %v", err)
	}
	if !allowed {
		t.Error("expected reverse call between linked households to be allowed")
	}
}

func TestCanCall_UnlinkedHouseholds(t *testing.T) {
	auth, database := testAuthorizer(t)
	houseA := createTestHousehold(t, database)
	houseB := createTestHousehold(t, database)

	store := NewStore(database)
	if _, err := store.Add("3000001", "Alice", houseA); err != nil {
		t.Fatalf("Add Alice: %v", err)
	}
	if _, err := store.Add("3000002", "Bob", houseB); err != nil {
		t.Fatalf("Add Bob: %v", err)
	}

	allowed, err := auth.CanCall("3000001", "3000002")
	if err != nil {
		t.Fatalf("CanCall: %v", err)
	}
	if allowed {
		t.Error("expected call between unlinked households to be denied")
	}
}

func TestCanCall_UnknownCaller(t *testing.T) {
	auth, database := testAuthorizer(t)
	householdID := createTestHousehold(t, database)

	store := NewStore(database)
	if _, err := store.Add("4000001", "Alice", householdID); err != nil {
		t.Fatalf("Add Alice: %v", err)
	}

	allowed, err := auth.CanCall("9999999", "4000001")
	if err != nil {
		t.Fatalf("CanCall: %v", err)
	}
	if allowed {
		t.Error("expected call from unknown number to be denied")
	}
}

func TestCanCall_UnknownCallee(t *testing.T) {
	auth, database := testAuthorizer(t)
	householdID := createTestHousehold(t, database)

	store := NewStore(database)
	if _, err := store.Add("4100001", "Alice", householdID); err != nil {
		t.Fatalf("Add Alice: %v", err)
	}

	allowed, err := auth.CanCall("4100001", "9999999")
	if err != nil {
		t.Fatalf("CanCall: %v", err)
	}
	if allowed {
		t.Error("expected call to unknown number to be denied")
	}
}

func TestCanCall_BothUnknown(t *testing.T) {
	auth, _ := testAuthorizer(t)

	allowed, err := auth.CanCall("9999998", "9999999")
	if err != nil {
		t.Fatalf("CanCall: %v", err)
	}
	if allowed {
		t.Error("expected call between two unknown numbers to be denied")
	}
}

func TestCanCall_RevokedLink(t *testing.T) {
	auth, database := testAuthorizer(t)
	houseA := createTestHousehold(t, database)
	houseB := createTestHousehold(t, database)
	userID := createAuthTestUser(t, database)

	store := NewStore(database)
	if _, err := store.Add("5000001", "Alice", houseA); err != nil {
		t.Fatalf("Add Alice: %v", err)
	}
	if _, err := store.Add("5000002", "Bob", houseB); err != nil {
		t.Fatalf("Add Bob: %v", err)
	}

	// Create a revoked link (not active)
	a, b := houseA, houseB
	if a > b {
		a, b = b, a
	}
	_, err := database.DB.Exec(
		`INSERT INTO household_links (household_a_id, household_b_id, status, invite_code, invited_by, accepted_by, accepted_at, revoked_at, revoked_by)
		 VALUES ($1, $2, 'revoked', gen_random_uuid()::text, $3, $3, NOW(), NOW(), $3)`,
		a, b, userID,
	)
	if err != nil {
		t.Fatalf("insert revoked link: %v", err)
	}

	allowed, err := auth.CanCall("5000001", "5000002")
	if err != nil {
		t.Fatalf("CanCall: %v", err)
	}
	if allowed {
		t.Error("expected call between households with revoked link to be denied")
	}
}

func TestCanCall_PendingLink(t *testing.T) {
	auth, database := testAuthorizer(t)
	houseA := createTestHousehold(t, database)
	houseB := createTestHousehold(t, database)
	userID := createAuthTestUser(t, database)

	store := NewStore(database)
	if _, err := store.Add("6000001", "Alice", houseA); err != nil {
		t.Fatalf("Add Alice: %v", err)
	}
	if _, err := store.Add("6000002", "Bob", houseB); err != nil {
		t.Fatalf("Add Bob: %v", err)
	}

	// Create a pending link (household_b_id is NULL for pending invites)
	_, err := database.DB.Exec(
		`INSERT INTO household_links (household_a_id, household_b_id, status, invite_code, invited_by)
		 VALUES ($1, NULL, 'pending', gen_random_uuid()::text, $2)`,
		houseA, userID,
	)
	if err != nil {
		t.Fatalf("insert pending link: %v", err)
	}

	allowed, err := auth.CanCall("6000001", "6000002")
	if err != nil {
		t.Fatalf("CanCall: %v", err)
	}
	if allowed {
		t.Error("expected call between households with only a pending link to be denied")
	}
}

func TestCanCall_SameNumber(t *testing.T) {
	auth, database := testAuthorizer(t)
	householdID := createTestHousehold(t, database)

	store := NewStore(database)
	if _, err := store.Add("7000001", "Alice", householdID); err != nil {
		t.Fatalf("Add Alice: %v", err)
	}

	// Calling yourself: same household so it passes the same-household check
	allowed, err := auth.CanCall("7000001", "7000001")
	if err != nil {
		t.Fatalf("CanCall: %v", err)
	}
	if !allowed {
		t.Error("expected self-call to be allowed (same household)")
	}
}
