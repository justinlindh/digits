package calls

import (
	"sync"
	"time"

	"github.com/justinlindh/digits/server/internal/db"
)

// Sample is one point of per-endpoint call telemetry. Pointer fields are
// nullable: nil means "not available this sample."
type Sample struct {
	TS       time.Time
	LossPct  *float32
	JitterMs *float32
	RttMs    *float32
	ConnType string
	BytesIn  *int64
	BytesOut *int64
}

// ringCapacity is the per-endpoint in-memory sample retention.
// At the default 2s reporting cadence this holds 2 minutes of history.
const ringCapacity = 60

// callRings holds two bounded rings, one per endpoint, plus last-flushed
// timestamps used by the DB flusher (added in Task 5). All state is guarded
// by mu.
type callRings struct {
	mu         sync.Mutex
	byEndpoint map[string]*ring
}

type ring struct {
	samples []Sample // length up to ringCapacity
	// lastFlushed is set by the flusher (Task 5). Unused in this task.
	lastFlushed time.Time
}

func (r *ring) append(s Sample) {
	if len(r.samples) < ringCapacity {
		r.samples = append(r.samples, s)
		return
	}
	copy(r.samples, r.samples[1:])
	r.samples[ringCapacity-1] = s
}

func (r *ring) latest() *Sample {
	if len(r.samples) == 0 {
		return nil
	}
	s := r.samples[len(r.samples)-1]
	return &s
}

// HealthStore holds per-call in-memory telemetry. The database handle is
// accepted by the constructor for use by the flusher added in Task 5; when
// nil the store operates in memory-only mode. Zero-value is NOT valid; use
// NewHealthStore.
type HealthStore struct {
	db    *db.Database
	mu    sync.Mutex
	calls map[int64]*callRings
}

func NewHealthStore(d *db.Database) *HealthStore {
	return &HealthStore{
		db:    d,
		calls: make(map[int64]*callRings),
	}
}

// Record appends a sample for the given call and endpoint. Safe for
// concurrent use.
func (s *HealthStore) Record(callID int64, endpoint string, sample Sample) {
	s.mu.Lock()
	cr, ok := s.calls[callID]
	if !ok {
		cr = &callRings{byEndpoint: make(map[string]*ring)}
		s.calls[callID] = cr
	}
	s.mu.Unlock()

	cr.mu.Lock()
	defer cr.mu.Unlock()
	r, ok := cr.byEndpoint[endpoint]
	if !ok {
		r = &ring{}
		cr.byEndpoint[endpoint] = r
	}
	r.append(sample)
}

// Latest returns the most recent sample for the caller and callee endpoints
// of the given call. Either may be nil if no samples have been recorded yet.
func (s *HealthStore) Latest(callID int64, caller, callee string) (*Sample, *Sample) {
	s.mu.Lock()
	cr, ok := s.calls[callID]
	s.mu.Unlock()
	if !ok {
		return nil, nil
	}
	cr.mu.Lock()
	defer cr.mu.Unlock()
	var a, b *Sample
	if r := cr.byEndpoint[caller]; r != nil {
		a = r.latest()
	}
	if r := cr.byEndpoint[callee]; r != nil {
		b = r.latest()
	}
	return a, b
}

// Window returns a copy of the retained sample ring for an endpoint, oldest
// first. Returns an empty slice if the call or endpoint is unknown.
func (s *HealthStore) Window(callID int64, endpoint string) []Sample {
	s.mu.Lock()
	cr, ok := s.calls[callID]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	cr.mu.Lock()
	defer cr.mu.Unlock()
	r, ok := cr.byEndpoint[endpoint]
	if !ok {
		return nil
	}
	out := make([]Sample, len(r.samples))
	copy(out, r.samples)
	return out
}

// Evict drops all in-memory state for a call. Called by Tracker on call end.
// A final flush to DB is added in Task 5.
func (s *HealthStore) Evict(callID int64) {
	s.mu.Lock()
	delete(s.calls, callID)
	s.mu.Unlock()
}
