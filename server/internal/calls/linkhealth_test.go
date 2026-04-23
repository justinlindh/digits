package calls

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func sample(ts int64, loss float32) Sample {
	l := loss
	return Sample{TS: time.Unix(0, ts*int64(time.Millisecond)), LossPct: &l}
}

func TestHealthStoreRecordAndLatest(t *testing.T) {
	s := NewHealthStore(nil) // nil DB => flusher disabled (flusher not implemented in T3 but constructor must accept nil cleanly)
	s.Init(1)
	s.Record(1, "A", sample(100, 0.5))
	s.Record(1, "A", sample(200, 0.7))
	s.Record(1, "B", sample(150, 0.2))

	callerLatest, calleeLatest := s.Latest(1, "A", "B")
	if callerLatest == nil || *callerLatest.LossPct != 0.7 {
		t.Fatalf("Latest A: got %+v want 0.7", callerLatest)
	}
	if calleeLatest == nil || *calleeLatest.LossPct != 0.2 {
		t.Fatalf("Latest B: got %+v want 0.2", calleeLatest)
	}
}

func TestHealthStoreRingWraparound(t *testing.T) {
	s := NewHealthStore(nil)
	s.Init(1)
	for i := 0; i < 80; i++ {
		s.Record(1, "A", sample(int64(i), float32(i)))
	}
	win := s.Window(1, "A")
	if len(win) != 60 {
		t.Fatalf("window size: got %d want 60", len(win))
	}
	// Oldest retained should be sample 20 (80 - 60).
	if *win[0].LossPct != 20.0 {
		t.Fatalf("oldest retained: got %v want 20", *win[0].LossPct)
	}
	if *win[59].LossPct != 79.0 {
		t.Fatalf("newest retained: got %v want 79", *win[59].LossPct)
	}
}

func TestHealthStoreEvict(t *testing.T) {
	s := NewHealthStore(nil)
	s.Init(1)
	s.Record(1, "A", sample(100, 0.5))
	s.Evict(1)
	if win := s.Window(1, "A"); len(win) != 0 {
		t.Fatalf("post-evict window: got %d want 0", len(win))
	}
	a, b := s.Latest(1, "A", "B")
	if a != nil || b != nil {
		t.Fatalf("post-evict latest: got (%v,%v)", a, b)
	}
}

func TestHealthStoreConcurrentWritersSameRing(t *testing.T) {
	// Multiple goroutines hammer the SAME (callID, endpoint) ring to
	// exercise in-ring contention under the per-call mutex. This is the
	// contract Record documents: safe for concurrent use.
	s := NewHealthStore(nil)
	s.Init(1)
	var wg sync.WaitGroup
	const writers = 4
	const samplesPerWriter = 500
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < samplesPerWriter; i++ {
				s.Record(1, "A", sample(int64(w)*100000+int64(i), float32(i)))
			}
		}(w)
	}
	wg.Wait()

	// Sanity: ring holds RingCapacity samples and latest() returns one.
	win := s.Window(1, "A")
	if len(win) != RingCapacity {
		t.Fatalf("window size: got %d want %d", len(win), RingCapacity)
	}
	a, _ := s.Latest(1, "A", "B")
	if a == nil {
		t.Fatalf("Latest returned nil after %d concurrent samples", writers*samplesPerWriter)
	}
}

func TestHealthStoreConcurrentCallsAndEndpoints(t *testing.T) {
	// Spread goroutines across calls and endpoints so every callID in
	// [1,4] ends up with samples on BOTH "A" and "B". Exercises top-level
	// map races + per-call-map-creation races.
	s := NewHealthStore(nil)
	for id := int64(1); id <= 4; id++ {
		s.Init(id)
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			callID := int64(w/2 + 1) // w=0,1->1 ; 2,3->2 ; 4,5->3 ; 6,7->4
			endpoint := "A"
			if w%2 == 1 {
				endpoint = "B"
			}
			for i := 0; i < 500; i++ {
				s.Record(callID, endpoint, sample(int64(i), float32(i)))
			}
		}(w)
	}
	wg.Wait()
	for id := int64(1); id <= 4; id++ {
		if a, b := s.Latest(id, "A", "B"); a == nil || b == nil {
			t.Fatalf("call %d missing samples: %v %v", id, a, b)
		}
	}
}

func TestHealthStoreRecordIsNoOpAfterEvict(t *testing.T) {
	s := NewHealthStore(nil)
	s.Init(1)
	s.Record(1, "A", sample(100, 0.5))
	s.Evict(1)
	s.Record(1, "A", sample(200, 0.9)) // should NOT resurrect map entry
	if win := s.Window(1, "A"); len(win) != 0 {
		t.Fatalf("post-evict Record must not create entry; got window len %d", len(win))
	}
	a, b := s.Latest(1, "A", "B")
	if a != nil || b != nil {
		t.Fatalf("post-evict Record must not resurrect rings; got (%v,%v)", a, b)
	}
}

func TestHealthStoreFlushDisabledOption(t *testing.T) {
	// With FlushDisabled, Run returns when ctx is canceled without
	// attempting any DB writes. Since NewHealthStore(nil, ...) is also
	// a valid construction (nil DB), use that to avoid needing a DB
	// in this unit test.
	s := NewHealthStore(nil, WithFlushDisabled(true))
	if !s.flushDisabled {
		t.Fatal("expected flushDisabled == true")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx)
	}()
	cancel()
	select {
	case <-done:
		// good
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestHealthStoreFlushDisabledDefaultsFalse(t *testing.T) {
	s := NewHealthStore(nil)
	if s.flushDisabled {
		t.Fatal("expected flushDisabled default false")
	}
}

func TestHealthStoreSubscribeReceivesSamples(t *testing.T) {
	s := NewHealthStore(nil)
	s.Init(1)
	sub := s.Subscribe(1)
	defer sub.Close()

	loss := float32(0.5)
	s.Record(1, "A", Sample{TS: time.Now(), LossPct: &loss})

	select {
	case ev := <-sub.C:
		if ev.Kind != SampleKind {
			t.Fatalf("kind: got %v want SampleKind", ev.Kind)
		}
		if ev.Endpoint != "A" {
			t.Fatalf("endpoint: got %q want A", ev.Endpoint)
		}
		if ev.Sample.LossPct == nil || *ev.Sample.LossPct != 0.5 {
			t.Fatalf("sample loss: %+v", ev.Sample)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for sample event")
	}
}

func TestHealthStoreSubscribeDropsOnFullBuffer(t *testing.T) {
	s := NewHealthStore(nil)
	s.Init(1)
	sub := s.Subscribe(1)
	defer sub.Close()

	loss := float32(0.5)
	for i := 0; i < 64; i++ {
		s.Record(1, "A", Sample{TS: time.Now(), LossPct: &loss})
	}

	received := 0
	for {
		select {
		case <-sub.C:
			received++
		default:
			goto done
		}
	}
done:
	if received != subscriberBufferSize {
		t.Fatalf("received count: got %d want %d (full buffer)", received, subscriberBufferSize)
	}
}

func TestHealthStoreEvictClosesSubscribers(t *testing.T) {
	s := NewHealthStore(nil)
	s.Init(1)
	sub := s.Subscribe(1)

	s.Evict(1)

	deadline := time.After(time.Second)
	sawEnded := false
	for {
		select {
		case ev, ok := <-sub.C:
			if !ok {
				if !sawEnded {
					t.Fatal("channel closed without EndedKind event")
				}
				return
			}
			if ev.Kind == EndedKind {
				sawEnded = true
			}
		case <-deadline:
			t.Fatal("timeout waiting for EndedKind + close")
		}
	}
}

func TestHealthStoreSubscribeOnMissingCallReturnsClosedChannel(t *testing.T) {
	s := NewHealthStore(nil)
	sub := s.Subscribe(999)
	defer sub.Close()
	select {
	case _, ok := <-sub.C:
		if ok {
			t.Fatal("expected closed channel, got event")
		}
	case <-time.After(time.Second):
		t.Fatal("expected closed channel, got block")
	}
}

func TestHealthStoreCloseSubscriptionIsIdempotent(t *testing.T) {
	s := NewHealthStore(nil)
	s.Init(1)
	sub := s.Subscribe(1)
	sub.Close()
	sub.Close() // must not panic
	loss := float32(0.5)
	s.Record(1, "A", Sample{TS: time.Now(), LossPct: &loss})
}

func TestHealthStoreCloseAfterEvict(t *testing.T) {
	s := NewHealthStore(nil)
	s.Init(1)
	sub := s.Subscribe(1)
	s.Evict(1)
	sub.Close() // must not panic
}

func TestHealthStoreNotifyDisconnectedBroadcasts(t *testing.T) {
	s := NewHealthStore(nil)
	s.Init(1)
	sub := s.Subscribe(1)
	defer sub.Close()

	s.NotifyDisconnected(1, "Alice")

	select {
	case ev := <-sub.C:
		if ev.Kind != DisconnectKind {
			t.Fatalf("kind: got %v want DisconnectKind", ev.Kind)
		}
		if ev.EndedBy != "Alice" {
			t.Fatalf("ended by: got %q want Alice", ev.EndedBy)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for DisconnectKind")
	}
}

func TestHealthStoreNotifyDisconnectedOnUnknownCallIsNoOp(t *testing.T) {
	s := NewHealthStore(nil)
	s.NotifyDisconnected(999, "Alice") // must not panic, must not allocate
}

func TestHealthStoreNotifyDisconnectedDoesNotCrossCalls(t *testing.T) {
	s := NewHealthStore(nil)
	s.Init(1)
	s.Init(2)
	sub1 := s.Subscribe(1)
	sub2 := s.Subscribe(2)
	defer sub1.Close()
	defer sub2.Close()

	s.NotifyDisconnected(1, "Alice")

	select {
	case ev := <-sub1.C:
		if ev.Kind != DisconnectKind {
			t.Fatalf("sub1: got %v want DisconnectKind", ev.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("sub1 timeout")
	}

	select {
	case ev := <-sub2.C:
		t.Fatalf("sub2 should not have received an event, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// good
	}
}

func TestSessionKeyEquality(t *testing.T) {
	a := SessionKey{CallID: 42}
	b := SessionKey{CallID: 42}
	if a != b {
		t.Fatal("equal SessionKeys must be ==")
	}
	u := uuid.New()
	c := SessionKey{ConfID: u}
	d := SessionKey{ConfID: u}
	if c != d {
		t.Fatal("equal conference SessionKeys must be ==")
	}
	if a == c {
		t.Fatal("call and conf keys must not be equal")
	}

	m := map[SessionKey]int{a: 1, c: 2}
	if m[b] != 1 || m[d] != 2 {
		t.Fatalf("SessionKey not usable as map key: %v", m)
	}
}

func TestSessionKeyIsConf(t *testing.T) {
	if (SessionKey{CallID: 1}).IsConf() {
		t.Fatal("2-party SessionKey should not be conf")
	}
	if !(SessionKey{ConfID: uuid.New()}).IsConf() {
		t.Fatal("conference SessionKey should be conf")
	}
}

func TestSessionKeyIsConfConfIDWins(t *testing.T) {
	// Guard against malformed double-populated keys. ConfID != uuid.Nil
	// is the authoritative signal, even if CallID is also set. No code
	// path produces such a key today; this test documents the tiebreak
	// semantics so a future change to IsConf doesn't silently drift.
	k := SessionKey{CallID: 1, ConfID: uuid.New()}
	if !k.IsConf() {
		t.Fatal("double-populated SessionKey should report IsConf() == true (ConfID wins)")
	}
}

func TestHealthStoreConferenceRoundTrip(t *testing.T) {
	s := NewHealthStore(nil)
	confID := uuid.New()
	s.InitConference(confID)

	loss := float32(2.5)
	sample := Sample{TS: time.Unix(0, 1), LossPct: &loss, ConnType: "host"}
	s.RecordEdge(confID, "A", "B", sample)

	w := s.WindowEdge(confID, "A", "B")
	if len(w) != 1 {
		t.Fatalf("WindowEdge len: got %d want 1", len(w))
	}
	if w[0].LossPct == nil || *w[0].LossPct != 2.5 {
		t.Fatalf("sample LossPct not preserved")
	}

	latest := s.LatestEdge(confID, "A", "B")
	if latest == nil {
		t.Fatal("LatestEdge nil")
	}
	if latest.LossPct == nil || *latest.LossPct != 2.5 {
		t.Fatalf("LatestEdge LossPct not preserved")
	}

	s.EvictConference(confID)
	if w2 := s.WindowEdge(confID, "A", "B"); len(w2) != 0 {
		t.Fatalf("after EvictConference WindowEdge should be empty, got %d", len(w2))
	}
}

func TestHealthStoreConferenceSubscribeReceivesPeer(t *testing.T) {
	s := NewHealthStore(nil)
	confID := uuid.New()
	s.InitConference(confID)

	sub := s.SubscribeConference(confID)
	defer sub.Close()

	loss := float32(3.0)
	s.RecordEdge(confID, "A", "B", Sample{TS: time.Unix(0, 1), LossPct: &loss})

	select {
	case ev := <-sub.C:
		if ev.Kind != SampleKind {
			t.Fatalf("event kind: got %v", ev.Kind)
		}
		if ev.Endpoint != "A" {
			t.Fatalf("event Endpoint: got %q want A", ev.Endpoint)
		}
		if ev.Peer != "B" {
			t.Fatalf("event Peer: got %q want B", ev.Peer)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for SampleKind event")
	}
}

func TestHealthStoreRecordEdgeDropsIfNotInit(t *testing.T) {
	s := NewHealthStore(nil)
	confID := uuid.New() // never InitConference'd
	s.RecordEdge(confID, "A", "B", Sample{TS: time.Unix(0, 1)})
	if w := s.WindowEdge(confID, "A", "B"); len(w) != 0 {
		t.Fatalf("RecordEdge without InitConference should be a no-op: got %d", len(w))
	}
}
