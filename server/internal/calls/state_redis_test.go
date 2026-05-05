package calls

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestCallState(t *testing.T) *CallState {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewCallState(client)
}

func TestCallStateInitiateAndBusy(t *testing.T) {
	cs := newTestCallState(t)
	ctx := context.Background()

	if cs.Busy(ctx, "5551111") {
		t.Fatal("expected not busy before any calls")
	}

	cs.OnCallInitiated(ctx, 1, "5551111", "5552222")

	if !cs.Busy(ctx, "5551111") {
		t.Fatal("expected caller to be busy")
	}
	if !cs.Busy(ctx, "5552222") {
		t.Fatal("expected callee to be busy")
	}
	if cs.Busy(ctx, "5553333") {
		t.Fatal("expected unrelated number to not be busy")
	}
}

func TestCallStateOnCallEnded(t *testing.T) {
	cs := newTestCallState(t)
	ctx := context.Background()

	cs.OnCallInitiated(ctx, 1, "5551111", "5552222")
	cs.OnCallEnded(ctx, "5551111", "5552222")

	if cs.Busy(ctx, "5551111") {
		t.Fatal("expected caller to not be busy after end")
	}
	if cs.Busy(ctx, "5552222") {
		t.Fatal("expected callee to not be busy after end")
	}
}

func TestCallStatePeerOf(t *testing.T) {
	cs := newTestCallState(t)
	ctx := context.Background()

	cs.OnCallInitiated(ctx, 1, "5551111", "5552222")

	if peer := cs.PeerOf(ctx, "5551111"); peer != "5552222" {
		t.Fatalf("PeerOf(caller) = %q, want %q", peer, "5552222")
	}
	if peer := cs.PeerOf(ctx, "5552222"); peer != "5551111" {
		t.Fatalf("PeerOf(callee) = %q, want %q", peer, "5551111")
	}
	if peer := cs.PeerOf(ctx, "5553333"); peer != "" {
		t.Fatalf("PeerOf(unrelated) = %q, want empty", peer)
	}
}

func TestCallStateAllPeersOf(t *testing.T) {
	cs := newTestCallState(t)
	ctx := context.Background()

	cs.OnCallInitiated(ctx, 1, "5551111", "5552222")
	cs.OnCallInitiated(ctx, 2, "5551111", "5553333")

	peers := cs.AllPeersOf(ctx, "5551111")
	if len(peers) != 2 {
		t.Fatalf("AllPeersOf got %d peers, want 2", len(peers))
	}

	got := make(map[string]bool)
	for _, p := range peers {
		got[p] = true
	}
	if !got["5552222"] || !got["5553333"] {
		t.Fatalf("AllPeersOf = %v, want [5552222, 5553333]", peers)
	}
}

func TestCallStateInCall(t *testing.T) {
	cs := newTestCallState(t)
	ctx := context.Background()

	cs.OnCallInitiated(ctx, 1, "5551111", "5552222")

	if !cs.InCall(ctx, "5551111", "5552222") {
		t.Fatal("InCall(caller, callee) should be true")
	}
	if !cs.InCall(ctx, "5552222", "5551111") {
		t.Fatal("InCall(callee, caller) should be true (reverse)")
	}
	if cs.InCall(ctx, "5551111", "5553333") {
		t.Fatal("InCall with unrelated number should be false")
	}
}

func TestCallStateCallIDFor(t *testing.T) {
	cs := newTestCallState(t)
	ctx := context.Background()

	cs.OnCallInitiated(ctx, 42, "5551111", "5552222")

	id, ok := cs.CallIDFor(ctx, "5551111")
	if !ok || id != 42 {
		t.Fatalf("CallIDFor(caller) = (%d, %v), want (42, true)", id, ok)
	}

	id, ok = cs.CallIDFor(ctx, "5552222")
	if !ok || id != 42 {
		t.Fatalf("CallIDFor(callee) = (%d, %v), want (42, true)", id, ok)
	}

	id, ok = cs.CallIDFor(ctx, "5553333")
	if ok || id != 0 {
		t.Fatalf("CallIDFor(unrelated) = (%d, %v), want (0, false)", id, ok)
	}
}

func TestCallStateCallIDForPair(t *testing.T) {
	cs := newTestCallState(t)
	ctx := context.Background()

	cs.OnCallInitiated(ctx, 99, "5551111", "5552222")

	if id := cs.CallIDForPair(ctx, "5551111", "5552222"); id != 99 {
		t.Fatalf("CallIDForPair(forward) = %d, want 99", id)
	}
	if id := cs.CallIDForPair(ctx, "5552222", "5551111"); id != 99 {
		t.Fatalf("CallIDForPair(reverse) = %d, want 99", id)
	}
	if id := cs.CallIDForPair(ctx, "5551111", "5553333"); id != 0 {
		t.Fatalf("CallIDForPair(unrelated) = %d, want 0", id)
	}
}

func TestCallStateCanAddAsHost(t *testing.T) {
	cs := newTestCallState(t)
	ctx := context.Background()

	cs.OnCallInitiated(ctx, 1, "5551111", "5552222")

	// Caller with exactly 1 call: can add
	if !cs.CanAddAsHost(ctx, "5551111") {
		t.Fatal("CanAddAsHost should be true for caller with 1 call")
	}

	// Callee: cannot add
	if cs.CanAddAsHost(ctx, "5552222") {
		t.Fatal("CanAddAsHost should be false for callee")
	}

	// Caller with 2 calls: cannot add
	cs.OnCallInitiated(ctx, 2, "5551111", "5553333")
	if cs.CanAddAsHost(ctx, "5551111") {
		t.Fatal("CanAddAsHost should be false for caller with 2 calls")
	}
}

func TestCallStateClearByNumber(t *testing.T) {
	cs := newTestCallState(t)
	ctx := context.Background()

	cs.OnCallInitiated(ctx, 1, "5551111", "5552222")
	cs.OnCallInitiated(ctx, 2, "5551111", "5553333")

	cs.ClearByNumber(ctx, "5551111")

	if cs.Busy(ctx, "5551111") {
		t.Fatal("cleared number should not be busy")
	}
	if cs.Busy(ctx, "5552222") {
		t.Fatal("peer should not be busy after clear")
	}
	if cs.Busy(ctx, "5553333") {
		t.Fatal("peer should not be busy after clear")
	}
}

func TestCallStateActive(t *testing.T) {
	cs := newTestCallState(t)
	ctx := context.Background()

	cs.OnCallInitiated(ctx, 1, "5551111", "5552222")
	cs.OnCallInitiated(ctx, 2, "5553333", "5554444")

	calls := cs.Active(ctx)
	if len(calls) != 2 {
		t.Fatalf("Active() returned %d calls, want 2", len(calls))
	}

	ids := make(map[int64]bool)
	for _, c := range calls {
		ids[c.ID] = true
	}
	if !ids[1] || !ids[2] {
		t.Fatalf("Active() IDs = %v, want [1, 2]", ids)
	}
}
