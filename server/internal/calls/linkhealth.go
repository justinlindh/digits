package calls

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/justinlindh/digits/server/internal/db"
)

// Sample is one point of per-endpoint call telemetry. Pointer fields are
// nullable: nil means "not available this sample."
//
// Ownership: once a Sample is passed to HealthStore.Record, its pointer
// fields must not be mutated by the caller. The store retains the pointers
// as-is (no deep copy on the hot path); concurrent readers will see
// whatever the pointers point to. Callers should construct fresh
// *float32 / *int64 values per sample and not reuse backing storage.
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

// callRings holds per-endpoint bounded sample rings and last-flushed
// timestamps used by the DB flusher. All state is guarded by mu.
type callRings struct {
	mu         sync.Mutex
	byEndpoint map[string]*ring
}

type ring struct {
	samples     []Sample // length up to ringCapacity
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
func (s *HealthStore) Evict(callID int64) {
	s.mu.Lock()
	delete(s.calls, callID)
	s.mu.Unlock()
}

// flushInterval is how often the background flusher runs. See spec §2.
const flushInterval = 10 * time.Second

// FlushOnce walks every tracked call and writes one row per (endpoint) for
// any endpoint with samples newer than its lastFlushed timestamp. Exported
// for tests; production use goes through Run.
func (s *HealthStore) FlushOnce(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	// Snapshot call ids under the top-level lock to avoid holding it while
	// doing DB I/O.
	s.mu.Lock()
	ids := make([]int64, 0, len(s.calls))
	for id := range s.calls {
		ids = append(ids, id)
	}
	s.mu.Unlock()

	for _, id := range ids {
		s.mu.Lock()
		cr := s.calls[id]
		s.mu.Unlock()
		if cr == nil {
			continue
		}
		if err := s.flushCall(ctx, id, cr); err != nil {
			slog.Error("link-health flush failed", "call_id", id, "err", err)
			// Do NOT advance lastFlushed on error; next cycle retries.
			// Continue to next call; one call's DB error shouldn't stop others.
			continue
		}
	}
	return nil
}

func (s *HealthStore) flushCall(ctx context.Context, callID int64, cr *callRings) error {
	cr.mu.Lock()
	// Collect work under lock, then release before DB I/O.
	type pending struct {
		endpoint string
		sample   Sample
	}
	var todo []pending
	var advance []struct {
		endpoint string
		ts       time.Time
	}
	for ep, r := range cr.byEndpoint {
		latest := r.latest()
		if latest == nil {
			continue
		}
		if !latest.TS.After(r.lastFlushed) {
			continue // nothing new since last flush
		}
		todo = append(todo, pending{endpoint: ep, sample: *latest})
		advance = append(advance, struct {
			endpoint string
			ts       time.Time
		}{ep, latest.TS})
	}
	cr.mu.Unlock()

	if len(todo) == 0 {
		return nil
	}
	for _, p := range todo {
		if err := s.writeSample(ctx, callID, p.endpoint, p.sample); err != nil {
			return fmt.Errorf("write sample (%d,%s): %w", callID, p.endpoint, err)
		}
	}
	// On success, advance lastFlushed.
	cr.mu.Lock()
	for _, a := range advance {
		if r := cr.byEndpoint[a.endpoint]; r != nil {
			r.lastFlushed = a.ts
		}
	}
	cr.mu.Unlock()
	return nil
}

func (s *HealthStore) writeSample(ctx context.Context, callID int64, endpoint string, sample Sample) error {
	_, err := s.db.DB.ExecContext(ctx,
		`INSERT INTO call_link_health
		   (call_id, endpoint, ts, loss_pct, jitter_ms, rtt_ms, conn_type, bytes_in, bytes_out)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (call_id, endpoint, ts) DO NOTHING`,
		callID, endpoint, sample.TS,
		nullableFloat(sample.LossPct),
		nullableFloat(sample.JitterMs),
		nullableFloat(sample.RttMs),
		nullableString(sample.ConnType),
		nullableInt(sample.BytesIn),
		nullableInt(sample.BytesOut),
	)
	return err
}

func nullableFloat(p *float32) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullableInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Run blocks until ctx is canceled, flushing every flushInterval. On ctx
// cancellation it runs one final flush before returning (bounded at 2s so
// a graceful shutdown doesn't hang).
func (s *HealthStore) Run(ctx context.Context) {
	if s.db == nil {
		<-ctx.Done()
		return
	}
	t := time.NewTicker(flushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = s.FlushOnce(shutdownCtx)
			cancel()
			return
		case <-t.C:
			_ = s.FlushOnce(ctx)
		}
	}
}

// Readback returns the last `limit` samples for a call+endpoint from the DB,
// oldest first. Used when in-memory state is empty (ended call, post-restart).
func (s *HealthStore) Readback(ctx context.Context, callID int64, endpoint string, limit int) ([]Sample, error) {
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.DB.QueryContext(ctx,
		`SELECT ts, loss_pct, jitter_ms, rtt_ms, conn_type, bytes_in, bytes_out
		 FROM call_link_health
		 WHERE call_id = $1 AND endpoint = $2
		 ORDER BY ts DESC
		 LIMIT $3`,
		callID, endpoint, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("readback query: %w", err)
	}
	defer rows.Close()

	var out []Sample
	for rows.Next() {
		var ts time.Time
		var loss, jitter, rtt sql.NullFloat64
		var conn sql.NullString
		var bin, bout sql.NullInt64
		if err := rows.Scan(&ts, &loss, &jitter, &rtt, &conn, &bin, &bout); err != nil {
			return nil, fmt.Errorf("readback scan: %w", err)
		}
		sample := Sample{TS: ts}
		if loss.Valid {
			v := float32(loss.Float64)
			sample.LossPct = &v
		}
		if jitter.Valid {
			v := float32(jitter.Float64)
			sample.JitterMs = &v
		}
		if rtt.Valid {
			v := float32(rtt.Float64)
			sample.RttMs = &v
		}
		if conn.Valid {
			sample.ConnType = conn.String
		}
		if bin.Valid {
			sample.BytesIn = &bin.Int64
		}
		if bout.Valid {
			sample.BytesOut = &bout.Int64
		}
		out = append(out, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("readback rows: %w", err)
	}
	// Reverse to oldest-first to match in-memory Window ordering.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
