package calls

import (
	"context"
	"sync"
	"testing"
	"time"
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

	// Sanity: ring holds ringCapacity samples and latest() returns one.
	win := s.Window(1, "A")
	if len(win) != ringCapacity {
		t.Fatalf("window size: got %d want %d", len(win), ringCapacity)
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
