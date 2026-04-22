package calls

import (
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

func TestHealthStoreConcurrentWriters(t *testing.T) {
	s := NewHealthStore(nil)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			callID := int64(w/2 + 1) // calls 1-4, two goroutines each
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
