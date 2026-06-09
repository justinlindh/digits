package calls

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// twoStores wires two HealthStores ("pods") to one miniredis and starts
// both RunRedis loops. The returned cancel stops the loops.
func twoStores(t *testing.T) (a, b *HealthStore) {
	t.Helper()
	mr := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = clientA.Close(); _ = clientB.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	a = NewHealthStore(nil)
	a.SetRedis(clientA, "pod-a")
	go a.RunRedis(ctx)

	b = NewHealthStore(nil)
	b.SetRedis(clientB, "pod-b")
	go b.RunRedis(ctx)

	// Give both subscriptions time to attach before tests publish.
	time.Sleep(100 * time.Millisecond)
	return a, b
}

// waitForWindow polls until the store's window for (callID, endpoint) is
// non-empty or the deadline passes.
func waitForWindow(t *testing.T, s *HealthStore, callID int64, endpoint string) []Sample {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if w := s.Window(callID, endpoint); len(w) > 0 {
			return w
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for window on call %d endpoint %s", callID, endpoint)
	return nil
}

func TestHealthStoreRedisFanOutSample(t *testing.T) {
	a, b := twoStores(t)

	loss := float32(1.5)
	ts := time.Now().Truncate(time.Millisecond)
	a.Record(7, "555-1111", Sample{TS: ts, LossPct: &loss, ConnType: "srflx"})

	w := waitForWindow(t, b, 7, "555-1111")
	if len(w) != 1 {
		t.Fatalf("remote window len = %d, want 1", len(w))
	}
	got := w[0]
	if !got.TS.Equal(ts) || got.LossPct == nil || *got.LossPct != loss || got.ConnType != "srflx" {
		t.Fatalf("remote sample mismatch: %+v", got)
	}
}

func TestHealthStoreRedisFanOutToSubscriber(t *testing.T) {
	a, b := twoStores(t)

	// Viewer subscribes on pod B before pod A has ingested anything.
	sub := b.Subscribe(9)
	defer sub.Close()

	jit := float32(3)
	a.Record(9, "555-2222", Sample{TS: time.Now(), JitterMs: &jit})

	select {
	case ev, ok := <-sub.C:
		if !ok {
			t.Fatal("subscription closed before delivering the remote sample")
		}
		if ev.Kind != SampleKind || ev.Endpoint != "555-2222" {
			t.Fatalf("got event %+v, want remote SampleKind from 555-2222", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for cross-pod sample event")
	}
}

func TestHealthStoreRedisFanOutEvictClosesRemoteSubscribers(t *testing.T) {
	a, b := twoStores(t)

	sub := b.Subscribe(11)
	defer sub.Close()

	a.Evict(11)

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-sub.C:
			if !ok {
				return // channel closed by the remote-applied evict: success
			}
			if ev.Kind == EndedKind {
				continue // EndedKind precedes the close
			}
			t.Fatalf("unexpected event before close: %+v", ev)
		case <-deadline:
			t.Fatal("timeout waiting for cross-pod evict to close the subscription")
		}
	}
}

func TestHealthStoreRedisFanOutDisconnectNotify(t *testing.T) {
	a, b := twoStores(t)

	sub := b.Subscribe(13)
	defer sub.Close()

	a.NotifyDisconnected(13, "Justin")

	select {
	case ev, ok := <-sub.C:
		if !ok {
			t.Fatal("subscription closed unexpectedly")
		}
		if ev.Kind != DisconnectKind || ev.EndedBy != "Justin" {
			t.Fatalf("got event %+v, want DisconnectKind by Justin", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for cross-pod disconnect event")
	}
}

func TestHealthStoreRedisFanOutConferenceEdge(t *testing.T) {
	a, b := twoStores(t)

	confID := uuid.New()
	rtt := float32(40)
	a.RecordEdge(confID, "555-1111", "555-2222", Sample{TS: time.Now(), RttMs: &rtt})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if w := b.WindowEdge(confID, "555-1111", "555-2222"); len(w) == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timeout waiting for cross-pod conference edge sample")
}

func TestHealthStoreRedisSkipsOwnEvents(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s := NewHealthStore(nil)
	s.SetRedis(client, "pod-a")
	go s.RunRedis(ctx)
	time.Sleep(100 * time.Millisecond)

	loss := float32(0.5)
	s.Record(21, "555-1111", Sample{TS: time.Now(), LossPct: &loss})
	// Give the subscriber loop a moment; a self-applied echo would append
	// a duplicate sample to the ring.
	time.Sleep(200 * time.Millisecond)
	if w := s.Window(21, "555-1111"); len(w) != 1 {
		t.Fatalf("window len = %d, want 1 (own published event must be skipped)", len(w))
	}
}
