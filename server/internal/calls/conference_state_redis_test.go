package calls

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func newTestConfState(t *testing.T) *ConfState {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewConfState(client)
}

func TestConfStateCreateAndIsBusy(t *testing.T) {
	cs := newTestConfState(t)
	ctx := context.Background()

	if cs.IsBusy(ctx, "5551111") {
		t.Fatal("expected not busy before any conference")
	}

	confID := uuid.New()
	members := []string{"5551111", "5552222", "5553333"}
	cs.Create(ctx, confID, "5551111", 42, members)

	for _, m := range members {
		if !cs.IsBusy(ctx, m) {
			t.Fatalf("expected %s to be busy after Create", m)
		}
	}
	if cs.IsBusy(ctx, "5554444") {
		t.Fatal("expected unrelated number to not be busy")
	}
}

func TestConfStateConferenceByPhone(t *testing.T) {
	cs := newTestConfState(t)
	ctx := context.Background()

	confID := uuid.New()
	members := []string{"5551111", "5552222", "5553333"}
	cs.Create(ctx, confID, "5551111", 99, members)

	conf := cs.ConferenceByPhone(ctx, "5552222")
	if conf == nil {
		t.Fatal("expected conference for member")
	}
	if conf.ID != confID {
		t.Fatalf("expected confID %s, got %s", confID, conf.ID)
	}
	if conf.Host != "5551111" {
		t.Fatalf("expected host 5551111, got %s", conf.Host)
	}
	if conf.OriginatingCallID != 99 {
		t.Fatalf("expected originating call ID 99, got %d", conf.OriginatingCallID)
	}
	if conf.State != ConferenceStateActive {
		t.Fatal("expected state to be active")
	}
	if len(conf.Members) != 3 {
		t.Fatalf("expected 3 members, got %d", len(conf.Members))
	}
	if conf.Members["5551111"].Role != ConferenceRoleHost {
		t.Fatal("expected host role for 5551111")
	}
	if conf.Members["5552222"].Role != ConferenceRoleAdded {
		t.Fatal("expected added role for 5552222")
	}

	if cs.ConferenceByPhone(ctx, "5559999") != nil {
		t.Fatal("expected nil for unknown phone")
	}
}

func TestConfStateContains(t *testing.T) {
	cs := newTestConfState(t)
	ctx := context.Background()

	confID := uuid.New()
	members := []string{"5551111", "5552222", "5553333"}
	cs.Create(ctx, confID, "5551111", 1, members)

	if !cs.Contains(ctx, confID, "5551111", "5552222") {
		t.Fatal("expected Contains to return true for two members")
	}
	if !cs.Contains(ctx, confID, "5551111", "5553333") {
		t.Fatal("expected Contains to return true for host + added")
	}
	if cs.Contains(ctx, confID, "5551111", "5559999") {
		t.Fatal("expected Contains to return false when one phone is not a member")
	}
	if cs.Contains(ctx, uuid.New(), "5551111", "5552222") {
		t.Fatal("expected Contains to return false for unknown conference")
	}
}

func TestConfStateRemoveMember(t *testing.T) {
	cs := newTestConfState(t)
	ctx := context.Background()

	confID := uuid.New()
	members := []string{"5551111", "5552222", "5553333"}
	cs.Create(ctx, confID, "5551111", 1, members)

	cs.RemoveMember(ctx, confID, "5552222")

	if cs.IsBusy(ctx, "5552222") {
		t.Fatal("expected removed member to not be busy")
	}
	if !cs.IsBusy(ctx, "5551111") {
		t.Fatal("expected remaining member to still be busy")
	}
}

func TestConfStateEnd(t *testing.T) {
	cs := newTestConfState(t)
	ctx := context.Background()

	confID := uuid.New()
	members := []string{"5551111", "5552222", "5553333"}
	cs.Create(ctx, confID, "5551111", 1, members)

	cs.End(ctx, confID, members)

	for _, m := range members {
		if cs.IsBusy(ctx, m) {
			t.Fatalf("expected %s to not be busy after End", m)
		}
	}
	if cs.ConferenceByPhone(ctx, "5551111") != nil {
		t.Fatal("expected nil conference after End")
	}
}
