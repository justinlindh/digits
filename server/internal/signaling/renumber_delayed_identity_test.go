package signaling

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type identityFenceLineStore struct {
	ids map[string]int64
}

func (s *identityFenceLineStore) EffectiveLineSettings(context.Context, string) (*LineSettings, error) {
	return nil, nil
}
func (s *identityFenceLineStore) EffectiveLineSettingsForLine(ctx context.Context, number string, _ int64) (*LineSettings, error) {
	return s.EffectiveLineSettings(ctx, number)
}

func (s *identityFenceLineStore) LineIdentifiers(_ context.Context, number string) (int64, string, error) {
	return s.ids[number], "household", nil
}

func (s *identityFenceLineStore) WithRenumberReadFence(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func TestPendingCallReturnRejectsReusedRequesterIdentity(t *testing.T) {
	hub := NewHub()
	requester := &Conn{LineID: 99, HardwareID: "new-owner", Send: make(chan []byte, 1)}
	if err := hub.Register("3140001", requester); err != nil {
		t.Fatal(err)
	}
	target := &Conn{LineID: 20, HardwareID: "target", Send: make(chan []byte, 1)}
	if err := hub.Register("3140002", target); err != nil {
		t.Fatal(err)
	}
	tracker := newMockTracker()
	relay := NewRelay(hub, tracker, nil, &identityFenceLineStore{ids: map[string]int64{
		"3140001": 99,
		"3140002": 20,
	}})
	relay.pendingReturns["3140001"] = &pendingCallReturn{
		Target:          "3140002",
		RequesterLineID: 10,
		TargetLineID:    20,
		ExpiresAt:       time.Now().Add(time.Minute),
	}

	relay.checkPendingReturn(context.Background(), "3140001")
	if _, ok := relay.pendingReturns["3140001"]; ok {
		t.Fatal("stale pending return survived requester number reuse")
	}
	select {
	case <-requester.Send:
		t.Fatal("stale pending return rang the new requester owner")
	default:
	}
}

type blockingIdentityFenceStore struct {
	entered chan struct{}
	release chan struct{}
}

func (s *blockingIdentityFenceStore) EffectiveLineSettings(context.Context, string) (*LineSettings, error) {
	return nil, nil
}
func (s *blockingIdentityFenceStore) EffectiveLineSettingsForLine(ctx context.Context, number string, _ int64) (*LineSettings, error) {
	return s.EffectiveLineSettings(ctx, number)
}
func (s *blockingIdentityFenceStore) LineIdentifiers(context.Context, string) (int64, string, error) {
	return 1, "household", nil
}
func (s *blockingIdentityFenceStore) WithRenumberReadFence(ctx context.Context, fn func(context.Context) error) error {
	close(s.entered)
	<-s.release
	return fn(ctx)
}

func TestOnConnClosedValidatesAndCleansUpInsideRenumberFence(t *testing.T) {
	hub := NewHub()
	tracker := newMockTracker()
	store := &blockingIdentityFenceStore{entered: make(chan struct{}), release: make(chan struct{})}
	relay := NewRelay(hub, tracker, nil, store)
	var bindingCurrent atomic.Bool
	bindingCurrent.Store(true)
	conn := &Conn{LineID: 1, HardwareID: "old-owner", Send: make(chan []byte, 1), ValidateBinding: bindingCurrent.Load}
	if err := hub.Register("3140001", conn); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		relay.OnConnClosed(context.Background(), conn)
		close(done)
	}()
	select {
	case <-store.entered:
	case <-done:
		t.Fatal("disconnect cleanup bypassed renumber fence")
	case <-time.After(time.Second):
		t.Fatal("disconnect cleanup did not enter renumber fence")
	}
	bindingCurrent.Store(false)
	close(store.release)
	<-done
	if got := tracker.clearedNumbers(); len(got) != 0 {
		t.Fatalf("stale disconnect cleared replacement state: %v", got)
	}
}
