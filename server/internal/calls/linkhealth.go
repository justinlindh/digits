package calls

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/justinlindh/digits/server/internal/db"
)

// EventKind tags the type of telemetry event delivered to subscribers.
type EventKind uint8

const (
	// SampleKind carries a new per-endpoint telemetry sample.
	SampleKind EventKind = iota
	// DisconnectKind signals a user-initiated force-disconnect.
	// EndedBy names the user who triggered it.
	DisconnectKind
	// EndedKind signals the call ended (from any cause) and the
	// subscriber channel will close immediately after.
	EndedKind
)

// Event is one delivery to a HealthStore subscriber.
type Event struct {
	Kind     EventKind
	Endpoint string // phone that emitted the sample (SampleKind only)
	Peer     string // remote endpoint the sample describes; "" for 2-party (SampleKind only)
	Sample   Sample // SampleKind only
	EndedBy  string // DisconnectKind only (user display label)
}

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

// SessionKey identifies either a 2-party call or a 3-way conference. Exactly
// one of CallID / ConfID is non-zero.
type SessionKey struct {
	CallID int64     // non-zero for 2-party calls
	ConfID uuid.UUID // non-zero for 3-way conferences
}

// IsConf reports whether this key identifies a conference.
func (k SessionKey) IsConf() bool { return k.ConfID != uuid.Nil }

// endpointKey is the composite map key for per-endpoint sample rings.
// Peer is zero for 2-party samples; set to the remote endpoint phone
// for 3-way per-edge samples (populated in a later phase).
type endpointKey struct {
	From string // phone that emitted the sample
	Peer string // remote endpoint the sample describes; "" for 2-party
}

// ringCapacity is the per-endpoint in-memory sample retention.
// At the default 2s reporting cadence this holds 2 minutes of history.
const ringCapacity = 60

// sessionRings holds per-endpoint bounded sample rings and last-flushed
// timestamps used by the DB flusher. All state is guarded by mu.
type sessionRings struct {
	mu          sync.Mutex
	byEndpoint  map[endpointKey]*ring
	subscribers map[*subscriber]struct{} // nil until first Subscribe
}

type subscriber struct {
	ch      chan Event
	dropped uint64 // events dropped due to full buffer; logged every 32
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
// accepted by the constructor for use by the periodic DB flusher; when
// nil the store operates in memory-only mode. Zero-value is NOT valid; use
// NewHealthStore.
type HealthStore struct {
	db            *db.Database
	mu            sync.Mutex
	sessions      map[SessionKey]*sessionRings
	flushDisabled bool
}

// HealthStoreOption configures a HealthStore at construction time.
type HealthStoreOption func(*HealthStore)

// WithFlushDisabled causes HealthStore.Run to skip periodic DB flushes
// and final shutdown flush. Ingest (Record/Init/Evict) and in-memory
// reads (Latest/Window) remain fully operational. Intended for DB
// maintenance windows; the in-memory rings still bound memory usage.
func WithFlushDisabled(disabled bool) HealthStoreOption {
	return func(s *HealthStore) { s.flushDisabled = disabled }
}

func NewHealthStore(d *db.Database, opts ...HealthStoreOption) *HealthStore {
	s := &HealthStore{
		db:       d,
		sessions: make(map[SessionKey]*sessionRings),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// initSession creates an empty rings entry for the given session key.
// Idempotent: no-op if the key already exists.
func (s *HealthStore) initSession(key SessionKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[key]; ok {
		return
	}
	s.sessions[key] = &sessionRings{byEndpoint: make(map[endpointKey]*ring)}
}

// recordSession appends a sample for the given session key and endpoint pair.
// No-op if the session was not initialized or has been evicted.
//
// Race note: between releasing the top-level map lock and acquiring the
// per-session lock, a concurrent evictSession can remove the sessionRings
// entry from the map. The captured *sessionRings reference remains valid
// (the struct is not freed) but is no longer reachable from the map; the
// sample appended to it will be silently dropped -- never flushed, eventually
// GC'd. This is intentional telemetry loss on a racing session-end.
func (s *HealthStore) recordSession(key SessionKey, from, peer string, sample Sample) {
	s.mu.Lock()
	sr, ok := s.sessions[key]
	s.mu.Unlock()
	if !ok {
		return
	}

	epKey := endpointKey{From: from, Peer: peer}
	sr.mu.Lock()
	defer sr.mu.Unlock()
	r, ok := sr.byEndpoint[epKey]
	if !ok {
		r = &ring{}
		sr.byEndpoint[epKey] = r
	}
	r.append(sample)
	sr.broadcastLocked(Event{Kind: SampleKind, Endpoint: from, Peer: peer, Sample: sample})
}

// windowSession returns a copy of the retained sample ring for the given
// session key and endpoint pair, oldest first. Empty if unknown.
func (s *HealthStore) windowSession(key SessionKey, from, peer string) []Sample {
	s.mu.Lock()
	sr, ok := s.sessions[key]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	sr.mu.Lock()
	defer sr.mu.Unlock()
	r, ok := sr.byEndpoint[endpointKey{From: from, Peer: peer}]
	if !ok {
		return nil
	}
	out := make([]Sample, len(r.samples))
	copy(out, r.samples)
	return out
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

// evictSession drops all in-memory state for a session. Broadcasts EndedKind
// to every live subscriber and closes their channels. Idempotent.
func (s *HealthStore) evictSession(key SessionKey) {
	s.mu.Lock()
	sr, ok := s.sessions[key]
	delete(s.sessions, key)
	s.mu.Unlock()
	if !ok {
		return
	}

	sr.mu.Lock()
	defer sr.mu.Unlock()
	// Every channel op under sr.mu must be non-blocking (select/default or
	// close) -- we hold the lock that recordSession also takes on the hot path.
	for sub := range sr.subscribers {
		select {
		case sub.ch <- Event{Kind: EndedKind}:
		default:
			sub.dropped++
			if sub.dropped%32 == 0 {
				slog.Debug("link_health: dropping EndedKind on full subscriber buffer", "dropped", sub.dropped)
			}
		}
		close(sub.ch)
	}
	sr.subscribers = nil
}

// subscribeSession opens an event stream for a session. If the session is not
// currently initialized (or has been evicted), returns a Subscription whose
// channel is already closed.
func (s *HealthStore) subscribeSession(key SessionKey) *Subscription {
	s.mu.Lock()
	sr, ok := s.sessions[key]
	s.mu.Unlock()
	if !ok {
		ch := make(chan Event)
		close(ch)
		return &Subscription{C: ch, close: func() {}}
	}

	sub := &subscriber{ch: make(chan Event, subscriberBufferSize)}

	sr.mu.Lock()
	if sr.subscribers == nil {
		sr.subscribers = make(map[*subscriber]struct{})
	}
	sr.subscribers[sub] = struct{}{}
	sr.mu.Unlock()

	return &Subscription{
		C: sub.ch,
		close: func() {
			sr.mu.Lock()
			_, stillRegistered := sr.subscribers[sub]
			delete(sr.subscribers, sub)
			sr.mu.Unlock()
			if stillRegistered {
				close(sub.ch)
			}
		},
	}
}

// Init creates an empty rings entry for a call. Called by Tracker on
// OnCallInitiated so that subsequent Record calls have a place to land
// without needing auto-creation (which would resurrect evicted calls).
// Safe to call multiple times; idempotent.
func (s *HealthStore) Init(callID int64) {
	s.initSession(SessionKey{CallID: callID})
}

// Record appends a sample for the given call and endpoint. Safe for
// concurrent use. No-op if Init was not called for this callID first
// or if the call has been Evicted -- this matches the tracker-authoritative
// lifecycle and prevents post-Evict resurrection of map entries.
func (s *HealthStore) Record(callID int64, endpoint string, sample Sample) {
	s.recordSession(SessionKey{CallID: callID}, endpoint, "", sample)
}

// Latest returns the most recent samples for caller and callee on a 2-party
// call, captured under a single lock so the two values are consistent. nil
// pointers if no sample has been recorded for that endpoint yet. nil/nil if
// the call is unknown.
func (s *HealthStore) Latest(callID int64, caller, callee string) (*Sample, *Sample) {
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

// Window returns a copy of the retained sample ring for an endpoint, oldest
// first. Returns an empty slice if the call or endpoint is unknown.
func (s *HealthStore) Window(callID int64, endpoint string) []Sample {
	return s.windowSession(SessionKey{CallID: callID}, endpoint, "")
}

// Evict drops all in-memory state for a call. Broadcasts an EndedKind event
// to every live subscriber and closes their channels, then clears the ring
// map entry. Safe to call multiple times; subsequent calls are no-ops.
//
// Called by Tracker on call end via the SetHealthStore-registered interface.
func (s *HealthStore) Evict(callID int64) {
	s.evictSession(SessionKey{CallID: callID})
}

// InitConference registers a conference for in-memory sample retention.
// Mirrors Init for 2-party calls. Safe to call multiple times.
func (s *HealthStore) InitConference(confID uuid.UUID) {
	s.initSession(SessionKey{ConfID: confID})
}

// RecordEdge appends a per-edge sample for a conference. from is the
// phone that emitted the sample; peer is the remote endpoint the
// sample describes. No-op if InitConference was not called for this
// conference (mirrors Record's behavior for unknown callID).
func (s *HealthStore) RecordEdge(confID uuid.UUID, from, peer string, sample Sample) {
	s.recordSession(SessionKey{ConfID: confID}, from, peer, sample)
}

// WindowEdge returns a copy of the retained sample ring for a conference
// edge, oldest first. Empty if unknown.
func (s *HealthStore) WindowEdge(confID uuid.UUID, from, peer string) []Sample {
	return s.windowSession(SessionKey{ConfID: confID}, from, peer)
}

// LatestEdge returns the most recent sample for a conference edge or nil.
func (s *HealthStore) LatestEdge(confID uuid.UUID, from, peer string) *Sample {
	return s.latestSession(SessionKey{ConfID: confID}, from, peer)
}

// EvictConference drops in-memory state for a conference and broadcasts
// EndedKind to subscribers.
func (s *HealthStore) EvictConference(confID uuid.UUID) {
	s.evictSession(SessionKey{ConfID: confID})
}

// SubscribeConference returns an event stream for a conference's samples,
// disconnect broadcasts, and ended events. Mirrors Subscribe for 2-party.
func (s *HealthStore) SubscribeConference(confID uuid.UUID) *Subscription {
	return s.subscribeSession(SessionKey{ConfID: confID})
}

// Subscription delivers per-call telemetry events to one consumer.
// Zero value is not valid; construct via HealthStore.Subscribe.
// The consumer MUST call Close when finished to release the slot.
// Close is idempotent and safe to defer.
//
// A single goroutine should receive from C; multiple concurrent receivers
// would interleave events in undefined order.
type Subscription struct {
	C     <-chan Event
	close func()
}

// Close removes the subscription from its call's subscriber set. Idempotent.
// Safe to call after the channel has already been closed by Evict.
func (s *Subscription) Close() {
	if s == nil || s.close == nil {
		return
	}
	s.close()
}

// subscriberBufferSize is the per-subscription channel depth. At the 2s
// sample cadence with two endpoints per call, 16 slots tolerates ~16s of
// consumer stall before events drop -- generous for a well-behaved SSE
// consumer on a LAN.
const subscriberBufferSize = 16

// Subscribe opens a stream of telemetry events for a call. If the call is
// not currently Init'd (or has already been Evicted), returns a Subscription
// whose channel is already closed -- callers see this the same way as a
// mid-stream Evict and can treat it uniformly.
func (s *HealthStore) Subscribe(callID int64) *Subscription {
	return s.subscribeSession(SessionKey{CallID: callID})
}

// NotifyDisconnected broadcasts a DisconnectKind event naming the user who
// initiated a force-disconnect. Subscribers can render a terminal "ended by
// X" state BEFORE the subsequent Evict (which arrives when the teardown
// propagates through Tracker.OnCallEnded) closes the stream.
//
// No-op for unknown callIDs.
func (s *HealthStore) NotifyDisconnected(callID int64, endedBy string) {
	key := SessionKey{CallID: callID}
	s.mu.Lock()
	sr, ok := s.sessions[key]
	s.mu.Unlock()
	if !ok {
		return
	}
	sr.mu.Lock()
	defer sr.mu.Unlock()
	sr.broadcastLocked(Event{Kind: DisconnectKind, EndedBy: endedBy})
}

// broadcastLocked sends an event to every subscriber under sr.mu. Non-blocking
// per subscriber: if a channel is full, the event drops and a debug log fires
// once per 32 drops to avoid log spam under sustained slow-client pressure.
// Caller MUST hold sr.mu.
func (sr *sessionRings) broadcastLocked(ev Event) {
	for sub := range sr.subscribers {
		select {
		case sub.ch <- ev:
		default:
			sub.dropped++
			if sub.dropped%32 == 0 {
				slog.Debug("link_health: dropping events on full subscriber buffer", "dropped", sub.dropped)
			}
		}
	}
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
	// Snapshot session keys under the top-level lock to avoid holding it while
	// doing DB I/O.
	s.mu.Lock()
	keys := make([]SessionKey, 0, len(s.sessions))
	for k := range s.sessions {
		keys = append(keys, k)
	}
	s.mu.Unlock()

	var errs []error
	for _, k := range keys {
		s.mu.Lock()
		sr := s.sessions[k]
		s.mu.Unlock()
		if sr == nil {
			continue
		}
		if err := s.flushSession(ctx, k, sr); err != nil {
			slog.Error("link-health flush failed", "session_key", k, "err", err)
			errs = append(errs, fmt.Errorf("session %v: %w", k, err))
		}
	}
	return errors.Join(errs...)
}

func (s *HealthStore) flushSession(ctx context.Context, key SessionKey, sr *sessionRings) error {
	if key.IsConf() {
		// Conference flush lands in a later phase with schema support for
		// conference_id + peer columns. Until then, conference-keyed sessions
		// live in memory only; the ticker loop must not try to persist them.
		return nil
	}
	sr.mu.Lock()
	// Collect work under lock, then release before DB I/O.
	type pending struct {
		epKey  endpointKey
		sample Sample
	}
	type flushAdvance struct {
		epKey endpointKey
		ts    time.Time
	}
	var todo []pending
	var advance []flushAdvance
	for epKey, r := range sr.byEndpoint {
		latest := r.latest()
		if latest == nil {
			continue
		}
		if !latest.TS.After(r.lastFlushed) {
			continue // nothing new since last flush
		}
		todo = append(todo, pending{epKey: epKey, sample: *latest})
		advance = append(advance, flushAdvance{epKey, latest.TS})
	}
	sr.mu.Unlock()

	if len(todo) == 0 {
		return nil
	}
	for _, p := range todo {
		if err := s.writeSample(ctx, key.CallID, p.epKey.From, p.sample); err != nil {
			return fmt.Errorf("write sample (%v,%s): %w", key, p.epKey.From, err)
		}
	}
	// On success, advance lastFlushed.
	sr.mu.Lock()
	for _, a := range advance {
		if r := sr.byEndpoint[a.epKey]; r != nil {
			r.lastFlushed = a.ts
		}
	}
	sr.mu.Unlock()
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
// a graceful shutdown doesn't hang). If flushDisabled is true, Run still
// blocks until ctx is canceled but skips all DB writes (periodic and final).
func (s *HealthStore) Run(ctx context.Context) {
	if s.db == nil || s.flushDisabled {
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
	defer func() { _ = rows.Close() }()

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
