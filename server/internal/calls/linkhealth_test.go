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

// latestSession returns the most recent sample for a single session edge or nil.
func (s *HealthStore) latestSession(key SessionKey, from, peer string) *Sample {
	s.mu.Lock()
	sr, ok := s.sessions[key]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if r := sr.byEndpoint[endpointKey{From: from, Peer: peer}]; r != nil {
		return r.latest()
	}
	return nil
}

// latest returns the most recent samples for caller and callee on a 2-party
// call, captured under a single lock so the two values are consistent. nil
// pointers if no sample has been recorded for that endpoint yet. nil/nil if
// the call is unknown.
func (s *HealthStore) latest(callID int64, caller, callee string) (*Sample, *Sample) {
	key := SessionKey{CallID: callID}
	s.mu.Lock()
	sr, ok := s.sessions[key]
	s.mu.Unlock()
	if !ok {
		return nil, nil
	}
	sr.mu.Lock()
	defer sr.mu.Unlock()
	var a, b *Sample
	if r := sr.byEndpoint[endpointKey{From: caller}]; r != nil {
		a = r.latest()
	}
	if r := sr.byEndpoint[endpointKey{From: callee}]; r != nil {
		b = r.latest()
	}
	return a, b
}

// latestEdge returns the most recent sample for a conference edge or nil.
func (s *HealthStore) latestEdge(confID uuid.UUID, from, peer string) *Sample {
	return s.latestSession(SessionKey{ConfID: confID}, from, peer)
}

func TestHealthStoreRecordAndLatest(t *testing.T) {
	s := NewHealthStore(nil) // nil DB => flusher disabled (flusher not implemented in T3 but constructor must accept nil cleanly)
	s.Init(1)
	s.Record(1, "A", sample(100, 0.5))
	s.Record(1, "A", sample(200, 0.7))
	s.Record(1, "B", sample(150, 0.2))

	callerLatest, calleeLatest := s.latest(1, "A", "B")
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
	for i := range 80 {
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
	a, b := s.latest(1, "A", "B")
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
	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range samplesPerWriter {
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
	a, _ := s.latest(1, "A", "B")
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
	for w := range 8 {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			callID := int64(w/2 + 1) // w=0,1->1 ; 2,3->2 ; 4,5->3 ; 6,7->4
			endpoint := "A"
			if w%2 == 1 {
				endpoint = "B"
			}
			for i := range 500 {
				s.Record(callID, endpoint, sample(int64(i), float32(i)))
			}
		}(w)
	}
	wg.Wait()
	for id := int64(1); id <= 4; id++ {
		if a, b := s.latest(id, "A", "B"); a == nil || b == nil {
			t.Fatalf("call %d missing samples: %v %v", id, a, b)
		}
	}
}

func TestHealthStoreRecordAfterEvictIsReapedBySweep(t *testing.T) {
	s := NewHealthStore(nil)
	s.Init(1)
	s.Record(1, "A", sample(100, 0.5))
	s.Evict(1)
	// A late sample (e.g. in flight while another replica handled the
	// hangup) lazily recreates the session rather than vanishing.
	s.Record(1, "A", sample(200, 0.9))
	if win := s.Window(1, "A"); len(win) != 1 {
		t.Fatalf("post-evict Record should lazily recreate the session; got window len %d", len(win))
	}
	// The idle sweep reaps it once it goes quiet.
	s.now = func() time.Time { return time.Now().Add(idleSessionTTL + time.Minute) }
	s.sweepIdleSessions()
	if win := s.Window(1, "A"); len(win) != 0 {
		t.Fatalf("idle sweep should evict the recreated session; got window len %d", len(win))
	}
}

func TestHealthStoreSweepSparesActiveAndSubscribed(t *testing.T) {
	s := NewHealthStore(nil)

	s.Init(1) // fresh session: activity is now
	s.Init(2) // will go idle but holds a subscriber
	sub := s.Subscribe(2)
	defer sub.Close()

	// Both sessions sit past the TTL except session 1, which records a
	// sample "now" to refresh its activity.
	base := time.Now()
	s.now = func() time.Time { return base.Add(idleSessionTTL + time.Minute) }
	s.Record(1, "A", sample(100, 0.5))
	s.sweepIdleSessions()

	if win := s.Window(1, "A"); len(win) != 1 {
		t.Fatalf("recently active session must survive the sweep; got window len %d", len(win))
	}
	select {
	case _, ok := <-sub.C:
		if !ok {
			t.Fatal("sweep must not evict a session with live subscribers")
		}
	default:
		// no event delivered: still subscribed, as expected
	}
}

func TestHealthStoreDisableFlush(t *testing.T) {
	// With flush disabled, Run returns when ctx is canceled without
	// attempting any DB writes. NewHealthStore(nil) is also a valid
	// construction (nil DB), so use it to avoid needing a DB in this
	// unit test.
	s := NewHealthStore(nil)
	s.DisableFlush()
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
	for range 64 {
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

func TestHealthStoreSubscribeOnUnseenCallReceivesLaterSamples(t *testing.T) {
	s := NewHealthStore(nil)
	// No Init: with multiple replicas the viewer's pod may not have seen
	// any state for the call yet. Subscribe must still attach.
	sub := s.Subscribe(999)
	defer sub.Close()

	s.Record(999, "A", sample(100, 0.5))
	select {
	case ev, ok := <-sub.C:
		if !ok {
			t.Fatal("subscription closed unexpectedly")
		}
		if ev.Kind != SampleKind || ev.Endpoint != "A" {
			t.Fatalf("got event %+v, want SampleKind from A", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for sample on lazily created session")
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

func TestNullablePtr(t *testing.T) {
	if nullablePtr[float32](nil) != nil {
		t.Error("nil pointer: want nil any")
	}
	v := float32(1.5)
	if nullablePtr(&v) != float32(1.5) {
		t.Errorf("non-nil pointer: got %v, want 1.5", nullablePtr(&v))
	}
	n := int64(42)
	if nullablePtr(&n) != int64(42) {
		t.Errorf("int64 pointer: got %v, want 42", nullablePtr(&n))
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

	latest := s.latestEdge(confID, "A", "B")
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

func TestHealthStoreRecordEdgeLazilyCreatesSession(t *testing.T) {
	s := NewHealthStore(nil)
	confID := uuid.New() // never InitConference'd: ingest pod may differ from setup pod
	s.RecordEdge(confID, "A", "B", Sample{TS: time.Unix(0, 1)})
	if w := s.WindowEdge(confID, "A", "B"); len(w) != 1 {
		t.Fatalf("RecordEdge without InitConference should lazily create the session: got %d samples", len(w))
	}
}

func TestHealthStoreRecordLazilyCreatesSession(t *testing.T) {
	s := NewHealthStore(nil)
	// No Init: with multiple replicas the ingesting pod is often not the
	// pod that handled call setup.
	s.Record(42, "A", Sample{TS: time.Unix(0, 1)})
	if w := s.Window(42, "A"); len(w) != 1 {
		t.Fatalf("Record without Init should lazily create the session: got %d samples", len(w))
	}
}

func TestHealthStoreSweepReapsStuckSubscribedSession(t *testing.T) {
	s := NewHealthStore(nil)

	// Race shape from the multi-pod world: the call ended on another pod
	// (its evict already fanned out and no-op'd here), then a viewer
	// subscribed, lazily creating a session that will never receive an
	// EndedKind from the call lifecycle.
	sub := s.Subscribe(31)
	defer sub.Close()

	// Under the normal idle TTL the subscriber protects the session.
	base := time.Now()
	s.now = func() time.Time { return base.Add(idleSessionTTL + time.Minute) }
	s.sweepIdleSessions()
	select {
	case _, ok := <-sub.C:
		if !ok {
			t.Fatal("session with subscriber swept before subscribedSessionTTL")
		}
	default:
	}

	// Past the subscribed backstop the sweep closes the stream.
	s.now = func() time.Time { return base.Add(subscribedSessionTTL + time.Minute) }
	s.sweepIdleSessions()
	deadline := time.After(time.Second)
	for {
		select {
		case ev, ok := <-sub.C:
			if !ok {
				return // closed: phantom session self-healed
			}
			if ev.Kind != EndedKind {
				t.Fatalf("unexpected event before close: %+v", ev)
			}
		case <-deadline:
			t.Fatal("sweep did not close the stuck subscribed session")
		}
	}
}
