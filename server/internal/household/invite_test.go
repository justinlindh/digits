//go:build integration

package household

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestInviteStore_CreateAndGet(t *testing.T) {
	store, database := testStore(t)
	invStore := NewInviteStore(database.DB)
	userID := createTestUser(t, database, "inviter@example.com")
	hh, err := store.Create(context.Background(), "Test Family", userID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}

	inv, err := invStore.CreateInvite(context.Background(), hh.ID, "INVITED@Example.com", userID)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if inv.Email != "invited@example.com" {
		t.Errorf("email not lowercased: got %q", inv.Email)
	}
	if inv.Status != InviteStatusPending {
		t.Errorf("expected status pending, got %q", inv.Status)
	}

	got, err := invStore.GetByToken(context.Background(), inv.Token)
	if err != nil {
		t.Fatalf("get by token: %v", err)
	}
	if got.ID != inv.ID {
		t.Errorf("ID mismatch: %s vs %s", got.ID, inv.ID)
	}
}

func TestInviteStore_AcceptInvite(t *testing.T) {
	store, database := testStore(t)
	invStore := NewInviteStore(database.DB)
	userID := createTestUser(t, database, "inviter2@example.com")
	hh, err := store.Create(context.Background(), "Accept Family", userID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}

	inv, err := invStore.CreateInvite(context.Background(), hh.ID, "joiner@example.com", userID)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	accepted, err := invStore.AcceptInvite(context.Background(), inv.Token)
	if err != nil {
		t.Fatalf("accept invite: %v", err)
	}
	if accepted.Status != "accepted" {
		t.Errorf("expected accepted, got %q", accepted.Status)
	}
	if accepted.AcceptedAt == nil {
		t.Error("accepted_at should be set")
	}

	_, err = invStore.AcceptInvite(context.Background(), inv.Token)
	if err == nil {
		t.Error("expected error on double accept")
	}
}

func TestInviteStore_CancelInvite(t *testing.T) {
	store, database := testStore(t)
	invStore := NewInviteStore(database.DB)
	userID := createTestUser(t, database, "inviter3@example.com")
	hh, err := store.Create(context.Background(), "Cancel Family", userID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}

	inv, err := invStore.CreateInvite(context.Background(), hh.ID, "cancel@example.com", userID)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	if err := invStore.CancelInvite(context.Background(), inv.ID); err != nil {
		t.Fatalf("cancel invite: %v", err)
	}

	got, err := invStore.GetByToken(context.Background(), inv.Token)
	if err != nil {
		t.Fatalf("get after cancel: %v", err)
	}
	if got.Status != "cancelled" {
		t.Errorf("expected cancelled, got %q", got.Status)
	}
}

func TestInviteStore_DuplicatePendingBlocked(t *testing.T) {
	store, database := testStore(t)
	invStore := NewInviteStore(database.DB)
	userID := createTestUser(t, database, "inviter4@example.com")
	hh, err := store.Create(context.Background(), "Dupe Family", userID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}

	_, err = invStore.CreateInvite(context.Background(), hh.ID, "same@example.com", userID)
	if err != nil {
		t.Fatalf("first invite: %v", err)
	}
	_, err = invStore.CreateInvite(context.Background(), hh.ID, "same@example.com", userID)
	if err == nil {
		t.Error("expected error on duplicate pending invite")
	}
}

func TestInviteStore_GetPendingForHousehold(t *testing.T) {
	store, database := testStore(t)
	invStore := NewInviteStore(database.DB)
	userID := createTestUser(t, database, "inviter5@example.com")
	hh, err := store.Create(context.Background(), "List Family", userID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}

	_, err = invStore.CreateInvite(context.Background(), hh.ID, "a@example.com", userID)
	if err != nil {
		t.Fatalf("invite a: %v", err)
	}
	bInv, err := invStore.CreateInvite(context.Background(), hh.ID, "b@example.com", userID)
	if err != nil {
		t.Fatalf("invite b: %v", err)
	}
	if err := invStore.CancelInvite(context.Background(), bInv.ID); err != nil {
		t.Fatalf("cancel b: %v", err)
	}

	pending, err := invStore.GetPendingForHousehold(context.Background(), hh.ID)
	if err != nil {
		t.Fatalf("get pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}
	if pending[0].Email != "a@example.com" {
		t.Errorf("expected a@example.com, got %q", pending[0].Email)
	}
}

func TestStore_RedeemInviteCancelledDoesNotAddMember(t *testing.T) {
	store, database := testStore(t)
	invStore := NewInviteStore(database.DB)
	ownerID := createTestUser(t, database, "cancelled-owner@example.com")
	joinerID := createTestUser(t, database, "cancelled-joiner@example.com")
	hh, err := store.Create(context.Background(), "Cancelled Family", ownerID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	inv, err := invStore.CreateInvite(context.Background(), hh.ID, "cancelled-joiner@example.com", ownerID)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if err := invStore.CancelInvite(context.Background(), inv.ID); err != nil {
		t.Fatalf("cancel invite: %v", err)
	}

	_, err = store.RedeemInvite(context.Background(), inv.Token, joinerID, "cancelled-joiner@example.com")
	if !errors.Is(err, ErrInviteExpiredOrUsed) {
		t.Fatalf("RedeemInvite error = %v, want ErrInviteExpiredOrUsed", err)
	}
	if _, err := store.GetRole(context.Background(), joinerID, hh.ID); !errors.Is(err, ErrNotMember) {
		t.Fatalf("GetRole error = %v, want ErrNotMember", err)
	}
}

func TestStore_RedeemInviteAcceptsMatchingEmail(t *testing.T) {
	store, database := testStore(t)
	invStore := NewInviteStore(database.DB)
	ownerID := createTestUser(t, database, "success-owner@example.com")
	joinerID := createTestUser(t, database, "success-joiner@example.com")
	hh, err := store.Create(context.Background(), "Success Family", ownerID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	inv, err := invStore.CreateInvite(context.Background(), hh.ID, "Success-Joiner@Example.com", ownerID)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	accepted, err := store.RedeemInvite(context.Background(), inv.Token, joinerID, "SUCCESS-JOINER@example.com")
	if err != nil {
		t.Fatalf("RedeemInvite: %v", err)
	}
	if accepted.Status != "accepted" || accepted.AcceptedAt == nil {
		t.Fatalf("accepted invite = %+v, want accepted status and timestamp", accepted)
	}
	role, err := store.GetRole(context.Background(), joinerID, hh.ID)
	if err != nil {
		t.Fatalf("GetRole: %v", err)
	}
	if role != "admin" {
		t.Fatalf("role = %q, want admin", role)
	}
	if _, ok := store.hasHousehold.Load(joinerID); !ok {
		t.Fatal("successful redemption did not update household cache")
	}
}

func TestStore_RedeemInviteRejectsExpiredAndWrongEmail(t *testing.T) {
	tests := []struct {
		name      string
		userEmail string
		expire    bool
		wantErr   error
	}{
		{name: "expired", userEmail: "invitee@example.com", expire: true, wantErr: ErrInviteExpiredOrUsed},
		{name: "wrong email", userEmail: "other@example.com", wantErr: ErrInviteEmailMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, database := testStore(t)
			invStore := NewInviteStore(database.DB)
			ownerID := createTestUser(t, database, tt.name+"-owner@example.com")
			joinerID := createTestUser(t, database, tt.name+"-joiner@example.com")
			hh, err := store.Create(context.Background(), tt.name+" Family", ownerID)
			if err != nil {
				t.Fatalf("create household: %v", err)
			}
			inv, err := invStore.CreateInvite(context.Background(), hh.ID, "invitee@example.com", ownerID)
			if err != nil {
				t.Fatalf("create invite: %v", err)
			}
			if tt.expire {
				if _, err := database.DB.Exec(`UPDATE household_invites SET expires_at = NOW() - INTERVAL '1 second' WHERE id = $1`, inv.ID); err != nil {
					t.Fatalf("expire invite: %v", err)
				}
			}

			_, err = store.RedeemInvite(context.Background(), inv.Token, joinerID, tt.userEmail)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RedeemInvite error = %v, want %v", err, tt.wantErr)
			}
			if _, err := store.GetRole(context.Background(), joinerID, hh.ID); !errors.Is(err, ErrNotMember) {
				t.Fatalf("GetRole error = %v, want ErrNotMember", err)
			}
		})
	}
}

func TestStore_RedeemInviteMembershipFailureRollsBack(t *testing.T) {
	store, database := testStore(t)
	invStore := NewInviteStore(database.DB)
	ownerID := createTestUser(t, database, "membership-failure-owner@example.com")
	hh, err := store.Create(context.Background(), "Membership Failure Family", ownerID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	inv, err := invStore.CreateInvite(context.Background(), hh.ID, "missing-user@example.com", ownerID)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	missingUserID := "00000000-0000-0000-0000-000000000001"

	if _, err := store.RedeemInvite(context.Background(), inv.Token, missingUserID, "missing-user@example.com"); err == nil {
		t.Fatal("RedeemInvite succeeded for missing user")
	}
	assertInvitePendingAndNoMember(t, store, invStore, inv.Token, missingUserID, hh.ID)
	if _, ok := store.hasHousehold.Load(missingUserID); ok {
		t.Fatal("failed redemption updated household cache")
	}
}

func TestStore_RedeemInviteConsumptionFailureRollsBackMembership(t *testing.T) {
	store, database := testStore(t)
	invStore := NewInviteStore(database.DB)
	ownerID := createTestUser(t, database, "consumption-failure-owner@example.com")
	joinerID := createTestUser(t, database, "consumption-failure-joiner@example.com")
	hh, err := store.Create(context.Background(), "Consumption Failure Family", ownerID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	inv, err := invStore.CreateInvite(context.Background(), hh.ID, "consumption-failure-joiner@example.com", ownerID)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	triggerName := "fail_invite_consumption_" + inv.ID[:8]
	functionName := triggerName + "_fn"
	if _, err := database.DB.Exec(fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'forced invite consumption failure'; END $$;
		CREATE TRIGGER %s BEFORE UPDATE ON household_invites
		FOR EACH ROW WHEN (NEW.id = '%s') EXECUTE FUNCTION %s()
	`, functionName, triggerName, inv.ID, functionName)); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec(fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON household_invites", triggerName))
		_, _ = database.DB.Exec(fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", functionName))
	})

	if _, err := store.RedeemInvite(context.Background(), inv.Token, joinerID, "consumption-failure-joiner@example.com"); err == nil {
		t.Fatal("RedeemInvite succeeded despite forced consumption failure")
	}
	assertInvitePendingAndNoMember(t, store, invStore, inv.Token, joinerID, hh.ID)
	if _, ok := store.hasHousehold.Load(joinerID); ok {
		t.Fatal("rolled-back redemption updated household cache")
	}
}

func TestStore_RedeemInviteConcurrentDoubleRedemption(t *testing.T) {
	store, database := testStore(t)
	invStore := NewInviteStore(database.DB)
	ownerID := createTestUser(t, database, "double-owner@example.com")
	joinerID := createTestUser(t, database, "double-joiner@example.com")
	hh, err := store.Create(context.Background(), "Double Family", ownerID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	inv, err := invStore.CreateInvite(context.Background(), hh.ID, "double-joiner@example.com", ownerID)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := store.RedeemInvite(context.Background(), inv.Token, joinerID, "double-joiner@example.com")
			errs <- err
		}()
	}
	close(start)
	var successes, rejected int
	for range 2 {
		err := <-errs
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrInviteExpiredOrUsed):
			rejected++
		default:
			t.Fatalf("unexpected redemption error: %v", err)
		}
	}
	if successes != 1 || rejected != 1 {
		t.Fatalf("successes = %d, rejected = %d, want 1 each", successes, rejected)
	}
}

func TestStore_RedeemInviteConcurrentCancel(t *testing.T) {
	store, database := testStore(t)
	invStore := NewInviteStore(database.DB)
	ownerID := createTestUser(t, database, "race-owner@example.com")
	joinerID := createTestUser(t, database, "race-joiner@example.com")
	hh, err := store.Create(context.Background(), "Race Family", ownerID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	inv, err := invStore.CreateInvite(context.Background(), hh.ID, "race-joiner@example.com", ownerID)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	start := make(chan struct{})
	var redeemErr, cancelErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, redeemErr = store.RedeemInvite(context.Background(), inv.Token, joinerID, "race-joiner@example.com")
	}()
	go func() {
		defer wg.Done()
		<-start
		cancelErr = invStore.CancelInvite(context.Background(), inv.ID)
	}()
	close(start)
	wg.Wait()

	got, err := invStore.GetByToken(context.Background(), inv.Token)
	if err != nil {
		t.Fatalf("GetByToken: %v", err)
	}
	_, roleErr := store.GetRole(context.Background(), joinerID, hh.ID)
	switch got.Status {
	case "accepted":
		if redeemErr != nil || !errors.Is(cancelErr, ErrInviteNotPending) || roleErr != nil {
			t.Fatalf("accepted outcome: redeemErr=%v cancelErr=%v roleErr=%v", redeemErr, cancelErr, roleErr)
		}
	case "cancelled":
		if !errors.Is(redeemErr, ErrInviteExpiredOrUsed) || cancelErr != nil || !errors.Is(roleErr, ErrNotMember) {
			t.Fatalf("cancelled outcome: redeemErr=%v cancelErr=%v roleErr=%v", redeemErr, cancelErr, roleErr)
		}
	default:
		t.Fatalf("invite status = %q, want accepted or cancelled", got.Status)
	}
}

func assertInvitePendingAndNoMember(t *testing.T, store *Store, invStore *InviteStore, token, userID, householdID string) {
	t.Helper()
	inv, err := invStore.GetByToken(context.Background(), token)
	if err != nil {
		t.Fatalf("GetByToken: %v", err)
	}
	if inv.Status != InviteStatusPending {
		t.Fatalf("invite status = %q, want pending", inv.Status)
	}
	if _, err := store.GetRole(context.Background(), userID, householdID); !errors.Is(err, ErrNotMember) {
		t.Fatalf("GetRole error = %v, want ErrNotMember", err)
	}
}
