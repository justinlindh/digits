//go:build integration

package household

import (
	"os"
	"testing"

	_ "github.com/lib/pq"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/db"
)

func TestE2EHouseholdLinkFlow(t *testing.T) {
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
	householdStore := NewStore(database.DB)
	linkStore := NewLinkStore(database.DB)

	// Create two users + households
	userA, err := authStore.CreateUser("e2e-link-a@example.com", "Parent A", nil)
	if err != nil {
		t.Fatalf("create user A: %v", err)
	}
	userB, err := authStore.CreateUser("e2e-link-b@example.com", "Parent B", nil)
	if err != nil {
		t.Fatalf("create user B: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM household_links WHERE invited_by_user_id = $1 OR accepted_by_user_id = $1", userA.ID)
		_, _ = database.DB.Exec("DELETE FROM household_links WHERE invited_by_user_id = $1 OR accepted_by_user_id = $1", userB.ID)
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE user_id IN ($1, $2)", userA.ID, userB.ID)
		_, _ = database.DB.Exec("DELETE FROM sessions WHERE user_id IN ($1, $2)", userA.ID, userB.ID)
		_, _ = database.DB.Exec("DELETE FROM users WHERE id IN ($1, $2)", userA.ID, userB.ID)
	})

	hhA, err := householdStore.Create("Family A", userA.ID)
	if err != nil {
		t.Fatalf("create household A: %v", err)
	}
	hhB, err := householdStore.Create("Family B", userB.ID)
	if err != nil {
		t.Fatalf("create household B: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM households WHERE id IN ($1, $2)", hhA.ID, hhB.ID)
	})

	// Step 1: Create invite from household A
	link, err := linkStore.CreateInvite(hhA.ID, userA.ID)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if len(link.InviteCode) != 8 {
		t.Fatalf("invite code length = %d, want 8", len(link.InviteCode))
	}
	if link.Status != "pending" {
		t.Fatalf("status = %q, want pending", link.Status)
	}
	t.Logf("Invite code: %s", link.InviteCode)

	// Step 2: Not linked yet
	linked, err := linkStore.AreLinked(hhA.ID, hhB.ID)
	if err != nil {
		t.Fatalf("AreLinked before accept: %v", err)
	}
	if linked {
		t.Fatal("should not be linked before accept")
	}

	// Step 3: Accept invite
	accepted, err := linkStore.AcceptInvite(link.InviteCode, userB.ID, hhB.ID)
	if err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}
	if accepted.Status != "active" {
		t.Fatalf("accepted status = %q, want active", accepted.Status)
	}

	// Step 4: Now linked
	linked, err = linkStore.AreLinked(hhA.ID, hhB.ID)
	if err != nil {
		t.Fatalf("AreLinked after accept: %v", err)
	}
	if !linked {
		t.Fatal("should be linked after accept")
	}

	// Step 5: Revoke
	err = linkStore.RevokeLink(accepted.ID, userA.ID)
	if err != nil {
		t.Fatalf("RevokeLink: %v", err)
	}

	// Step 6: No longer linked
	linked, err = linkStore.AreLinked(hhA.ID, hhB.ID)
	if err != nil {
		t.Fatalf("AreLinked after revoke: %v", err)
	}
	if linked {
		t.Fatal("should not be linked after revoke")
	}
}
