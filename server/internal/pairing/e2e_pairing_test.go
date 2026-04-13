//go:build integration

package pairing

import (
	"errors"
	"os"
	"testing"

	_ "github.com/lib/pq"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/household"
)

// TestE2EPairingFlow exercises the full pairing lifecycle:
// generate code → claim → verify paired → reject reuse → reject duplicate number
func TestE2EPairingFlow(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	database, err := db.Open(dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = database.Close() }()

	authStore := auth.NewStoreFromDB(database.DB)
	householdStore := household.NewStore(database.DB)
	pairingStore := NewStore(database.DB)

	// Create test user
	user, err := authStore.CreateUser("e2e-pair@example.com", "E2E Parent", nil)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM devices WHERE hardware_id LIKE 'e2e-test-%'")
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number IN ('5559876', '5559877')")
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE user_id = $1", user.ID)
		_, _ = database.DB.Exec("DELETE FROM sessions WHERE user_id = $1", user.ID)
		_, _ = database.DB.Exec("DELETE FROM users WHERE id = $1", user.ID)
		// households cascade from members
	})

	// Create household
	hh, err := householdStore.Create("E2E Test Family", user.ID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM households WHERE id = $1", hh.ID)
	})

	// Step 1: Generate pairing code (simulates phone connecting via WebSocket)
	code, err := pairingStore.GenerateCode("e2e-test-hw-001")
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("code length = %d, want 6", len(code))
	}
	t.Logf("Generated pairing code: %s", code)

	// Step 2: Verify not yet paired
	paired, err := pairingStore.IsPaired("e2e-test-hw-001")
	if err != nil {
		t.Fatalf("IsPaired before claim: %v", err)
	}
	if paired {
		t.Fatal("phone should not be paired yet")
	}

	// Step 3: Claim phone (parent enters code in dashboard)
	token, hwID, err := pairingStore.ClaimDevice(code, "5559876", "Kitchen Phone", hh.ID)
	if err != nil {
		t.Fatalf("ClaimDevice: %v", err)
	}
	if token == "" {
		t.Fatal("ClaimDevice should return non-empty token")
	}
	if hwID != "e2e-test-hw-001" {
		t.Fatalf("ClaimDevice hwID = %q, want e2e-test-hw-001", hwID)
	}

	// Step 4: Verify paired
	paired, err = pairingStore.IsPaired("e2e-test-hw-001")
	if err != nil {
		t.Fatalf("IsPaired after claim: %v", err)
	}
	if !paired {
		t.Fatal("phone should be paired after claim")
	}

	// Step 5: Reuse same code — should fail
	_, _, err = pairingStore.ClaimDevice(code, "5559877", "Reuse Attempt", hh.ID)
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("expected ErrInvalidCode on code reuse, got: %v", err)
	}

	// Step 6: Generate code for second phone
	code2, err := pairingStore.GenerateCode("e2e-test-hw-002")
	if err != nil {
		t.Fatalf("GenerateCode 2: %v", err)
	}

	// Step 7: Try to claim with duplicate number — should fail
	_, _, err = pairingStore.ClaimDevice(code2, "5559876", "Dupe Number", hh.ID)
	if !errors.Is(err, ErrNumberTaken) {
		t.Fatalf("expected ErrNumberTaken for duplicate number, got: %v", err)
	}

	// Step 8: Claim second phone with unique number — should succeed
	_, _, err = pairingStore.ClaimDevice(code2, "5559877", "Living Room Phone", hh.ID)
	if err != nil {
		t.Fatalf("ClaimDevice (second phone): %v", err)
	}

	// Step 9: Verify both phones paired
	paired1, _ := pairingStore.IsPaired("e2e-test-hw-001")
	paired2, _ := pairingStore.IsPaired("e2e-test-hw-002")
	if !paired1 || !paired2 {
		t.Fatalf("both phones should be paired: hw-001=%v, hw-002=%v", paired1, paired2)
	}

	// Step 10: Verify household membership
	role, err := householdStore.GetRole(user.ID, hh.ID)
	if err != nil {
		t.Fatalf("GetRole: %v", err)
	}
	if role != "admin" {
		t.Fatalf("expected admin role, got %q", role)
	}
}
