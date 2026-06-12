//go:build integration

package household

import (
	"context"
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
