package calls

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
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
// for 3-way per-edge samples.
type endpointKey struct {
	From string // phone that emitted the sample
	Peer string // remote endpoint the sample describes; "" for 2-party
}

// RingCapacity is the per-endpoint in-memory sample retention.
// At the default 2s reporting cadence this holds 2 minutes of history.
// Readback and ReadbackEdge callers use this constant for the limit argument.
const RingCapacity = 60

// sessionRings holds per-endpoint bounded sample rings and last-flushed
// timestamps used by the DB flusher. All state is guarded by mu.
type sessionRings struct {
	mu          sync.Mutex
	byEndpoint  map[endpointKey]*ring
	subscribers map[*subscriber]struct{} // nil until first Subscribe
	// lastActivity is the wall-clock time of the most recent init, sample
	// append, or subscribe. The idle sweep in Run evicts sessions whose
	// lastActivity is older than idleSessionTTL and that have no live
	// subscribers, bounding memory when an end event never reaches this
	// pod (it may have been handled by a different replica).
	lastActivity time.Time
}

type subscriber struct {
	ch      chan Event
	dropped uint64 // events dropped due to full buffer; logged every 32
}

type ring struct {
	samples     []Sample // length up to RingCapacity
	lastFlushed time.Time
}

func (r *ring) append(s Sample) {
	if len(r.samples) < RingCapacity {
		r.samples = append(r.samples, s)
		return
	}
	copy(r.samples, r.samples[1:])
	r.samples[RingCapacity-1] = s
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
//
// Multi-replica operation: sessions and rings are pod-local memory. When
// Redis is configured via SetRedis, every locally ingested sample and every
// locally triggered evict/disconnect is also published on a shared channel;
// RunRedis applies events from other pods to the local rings and
// subscribers. This keeps the live observation deck and the in-memory
// windows complete on every pod even though each phone's WebSocket (and
// therefore its link_health ingest) lands on exactly one pod.
type HealthStore struct {
	db            *sql.DB
	mu            sync.Mutex
	sessions      map[SessionKey]*sessionRings
	flushDisabled bool
	now           func() time.Time // injectable for idle-sweep tests

	rmu    sync.Mutex
	client redisPublisher
	podID  string
}

// HealthStoreOption configures a HealthStore at construction time.
type HealthStoreOption func(*HealthStore)

// WithFlushDisabled causes HealthStore.Run to skip periodic DB flushes
// and final shutdown flush. Ingest (Record/Init/Evict) and in-memory
// reads (Latest/Window) remain fully operational. Intended for DB
// maintenance windows; the in-memory rings still bound memory usage.
func WithFlushDisabled() HealthStoreOption {
	return func(s *HealthStore) { s.flushDisabled = true }
}

// NewHealthStore creates a HealthStore. Pass a nil database to operate in
// memory-only mode (no DB flushes). Functional options adjust behavior; see
// WithFlushDisabled.
func NewHealthStore(d *db.Database, opts ...HealthStoreOption) *HealthStore {
	s := &HealthStore{
		db:       unwrapDB(d),
		sessions: make(map[SessionKey]*sessionRings),
		now:      time.Now,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// getOrCreateSession returns the rings entry for key, creating it if absent,
// and stamps lastActivity. Sessions are created lazily because the
// "call started" hook (Tracker.StartCall -> Init) runs only on the pod that
// handled the caller's signaling message; samples and subscribers on the
// other replicas must not depend on it. The idle sweep bounds the lifetime
// of sessions whose end event never reaches this pod.
func (s *HealthStore) getOrCreateSession(key SessionKey) *sessionRings {
	s.mu.Lock()
	defer s.mu.Unlock()
	sr, ok := s.sessions[key]
	if !ok {
		sr = &sessionRings{byEndpoint: make(map[endpointKey]*ring)}
		s.sessions[key] = sr
	}
	sr.mu.Lock()
	sr.lastActivity = s.now()
	sr.mu.Unlock()
	return sr
}

// recordSession appends a sample for the given session key and endpoint
// pair, creating the session if this pod has not seen it yet. remote marks
// samples applied from the Redis fan-out: they update the rings and local
// subscribers but advance lastFlushed so only the ingesting pod writes the
// sample to the database. Advancing on every remote sample is safe because
// a given endpoint's samples come from a single publisher (the pod holding
// that phone's WebSocket), and Redis pub/sub preserves per-publisher order;
// the unique indexes behind writeSample's ON CONFLICT are the backstop.
//
// Race note: between releasing the top-level map lock and acquiring the
// per-session lock, a concurrent evictSession can remove the sessionRings
// entry from the map. The captured *sessionRings reference remains valid
// (the struct is not freed) but is no longer reachable from the map; the
// sample appended to it will be silently dropped -- never flushed, eventually
// GC'd. This is intentional telemetry loss on a racing session-end.
func (s *HealthStore) recordSession(key SessionKey, from, peer string, sample Sample, remote bool) {
	sr := s.getOrCreateSession(key)

	epKey := endpointKey{From: from, Peer: peer}
	sr.mu.Lock()
	r, ok := sr.byEndpoint[epKey]
	if !ok {
		r = &ring{}
		sr.byEndpoint[epKey] = r
	}
	r.append(sample)
	if remote && sample.TS.After(r.lastFlushed) {
		r.lastFlushed = sample.TS
	}
	sr.broadcastLocked(Event{Kind: SampleKind, Endpoint: from, Peer: peer, Sample: sample})
	sr.mu.Unlock()
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

// evictSession drops all in-memory state for a session. Broadcasts EndedKind
// to every live subscriber and closes their channels. Idempotent. Pod-local:
// the public Evict methods fan the eviction out to other pods; remote
// applies and the idle sweep call this directly.
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

// subscribeSession opens an event stream for a session, creating the
// session if this pod has not seen it yet. Callers gate on the call being
// active (the SSE handlers 404 ended calls before subscribing), so lazy
// creation cannot resurrect a finished call for longer than the idle sweep
// allows.
func (s *HealthStore) subscribeSession(key SessionKey) *Subscription {
	sr := s.getOrCreateSession(key)

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
// OnCallInitiated so subscribers attaching before the first sample find a
// live session. Record and Subscribe also create sessions lazily, so Init
// is an optimization, not a precondition. Safe to call multiple times;
// idempotent.
func (s *HealthStore) Init(callID int64) {
	s.getOrCreateSession(SessionKey{CallID: callID})
}

// Record appends a sample for the given call and endpoint, creating the
// session if this pod has not seen the call (with multiple replicas, call
// setup usually ran on a different pod). Safe for concurrent use. A sample
// racing the call's eviction can recreate the session; the idle sweep in
// Run bounds the lifetime of such stragglers.
func (s *HealthStore) Record(callID int64, endpoint string, sample Sample) {
	key := SessionKey{CallID: callID}
	s.recordSession(key, endpoint, "", sample, false)
	s.publishSample(key, endpoint, "", sample)
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
	key := SessionKey{CallID: callID}
	s.publishLifecycle(wireKindEvict, key, "")
	s.evictSession(key)
}

// InitConference registers a conference for in-memory sample retention.
// Mirrors Init for 2-party calls. Safe to call multiple times.
func (s *HealthStore) InitConference(confID uuid.UUID) {
	s.getOrCreateSession(SessionKey{ConfID: confID})
}

// RecordEdge appends a per-edge sample for a conference. from is the
// phone that emitted the sample; peer is the remote endpoint the sample
// describes. Creates the session lazily, mirroring Record.
func (s *HealthStore) RecordEdge(confID uuid.UUID, from, peer string, sample Sample) {
	key := SessionKey{ConfID: confID}
	s.recordSession(key, from, peer, sample, false)
	s.publishSample(key, from, peer, sample)
}

// WindowEdge returns a copy of the retained sample ring for a conference
// edge, oldest first. Empty if unknown.
func (s *HealthStore) WindowEdge(confID uuid.UUID, from, peer string) []Sample {
	return s.windowSession(SessionKey{ConfID: confID}, from, peer)
}

// EvictConference drops in-memory state for a conference and broadcasts
// EndedKind to subscribers.
func (s *HealthStore) EvictConference(confID uuid.UUID) {
	key := SessionKey{ConfID: confID}
	s.publishLifecycle(wireKindEvict, key, "")
	s.evictSession(key)
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

// Subscribe opens a stream of telemetry events for a call, creating the
// session if this pod has not seen the call yet. Callers must gate on the
// call being active first (the SSE handlers 404 ended calls before
// subscribing); the idle sweep reaps sessions a stray subscriber created
// for a dead call.
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
	s.publishLifecycle(wireKindDisconnect, key, endedBy)
	s.notifyDisconnectedSession(key, endedBy)
}

// NotifyDisconnectedConference broadcasts a DisconnectKind event to every
// subscriber of a conference session. Used by the kick endpoint so
// observer decks flip to a "Conference ended by <name>." terminal state
// before the Evict cascade closes the channel.
//
// No-op for unknown conference IDs.
func (s *HealthStore) NotifyDisconnectedConference(confID uuid.UUID, endedBy string) {
	key := SessionKey{ConfID: confID}
	s.publishLifecycle(wireKindDisconnect, key, endedBy)
	s.notifyDisconnectedSession(key, endedBy)
}

// notifyDisconnectedSession broadcasts DisconnectKind to local subscribers.
// Pod-local: the public Notify methods fan out to other pods first. Unlike
// Record/Subscribe this does NOT lazily create the session: a disconnect
// only matters where a subscriber (hence a session) already exists.
func (s *HealthStore) notifyDisconnectedSession(key SessionKey, endedBy string) {
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

// flushInterval is how often the background flusher runs.
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
			slog.ErrorContext(ctx, "link-health flush failed", "session_key", k, "err", err)
			errs = append(errs, fmt.Errorf("session %v: %w", k, err))
		}
	}
	return errors.Join(errs...)
}

func (s *HealthStore) flushSession(ctx context.Context, key SessionKey, sr *sessionRings) error {
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
		if err := s.writeSample(ctx, key, p.epKey, p.sample); err != nil {
			return fmt.Errorf("write sample (%v,%v): %w", key, p.epKey, err)
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

// writeSample inserts a single link-health sample for either a 2-party call
// or a 3-way conference, depending on key.IsConf(). ON CONFLICT DO NOTHING
// (no conflict target) handles either of the two partial unique indexes
// introduced by the v20 migration, so idempotency works for both kinds.
func (s *HealthStore) writeSample(ctx context.Context, key SessionKey, ep endpointKey, sample Sample) error {
	if key.IsConf() && ep.Peer == "" {
		return fmt.Errorf("writeSample: conference session requires non-empty peer (from=%q)", ep.From)
	}
	var (
		callID sql.NullInt64
		confID sql.NullString
		peer   sql.NullString
	)
	if key.IsConf() {
		confID = sql.NullString{String: key.ConfID.String(), Valid: true}
		peer = sql.NullString{String: ep.Peer, Valid: true}
	} else {
		callID = sql.NullInt64{Int64: key.CallID, Valid: key.CallID != 0}
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO call_link_health
		   (call_id, conference_id, endpoint, peer, ts,
		    loss_pct, jitter_ms, rtt_ms, conn_type, bytes_in, bytes_out)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 ON CONFLICT DO NOTHING`,
		callID, confID, ep.From, peer, sample.TS,
		nullablePtr(sample.LossPct),
		nullablePtr(sample.JitterMs),
		nullablePtr(sample.RttMs),
		nullableString(sample.ConnType),
		nullablePtr(sample.BytesIn),
		nullablePtr(sample.BytesOut),
	)
	return err
}

func nullablePtr[T any](p *T) any {
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

// Run blocks until ctx is canceled, flushing and sweeping every
// flushInterval. On ctx cancellation it runs one final flush before
// returning (bounded at 2s so a graceful shutdown doesn't hang). If
// flushDisabled is true (or the store is memory-only), DB writes are
// skipped but the idle sweep still runs so lazily created sessions cannot
// accumulate.
func (s *HealthStore) Run(ctx context.Context) {
	flush := s.db != nil && !s.flushDisabled
	t := time.NewTicker(flushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			if flush {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_ = s.FlushOnce(shutdownCtx)
				cancel()
			}
			return
		case <-t.C:
			if flush {
				_ = s.FlushOnce(ctx)
			}
			s.sweepIdleSessions()
		}
	}
}

// idleSessionTTL bounds how long a session with no new samples and no live
// subscribers survives in memory. End events normally evict promptly; the
// TTL covers stragglers recreated by late samples and sessions whose end
// event predates this pod's subscription (e.g. a pod restart mid-call).
const idleSessionTTL = 15 * time.Minute

// subscribedSessionTTL is the self-heal bound for sessions that hold live
// subscribers but see no samples. The SSE handlers re-check call liveness
// after subscribing, so a phantom "live" session on an ended call should
// not occur; if one slips through anyway, the sweep closes its streams
// with EndedKind after this much silence. Real calls report every couple
// of seconds, so an hour of total silence means the deck is dead weight.
const subscribedSessionTTL = 4 * idleSessionTTL

// sweepIdleSessions evicts sessions idle for longer than idleSessionTTL
// that have no live subscribers, and sessions idle past
// subscribedSessionTTL regardless of subscribers. Sweep evictions are
// pod-local: every pod runs its own sweep, so nothing is published.
func (s *HealthStore) sweepIdleSessions() {
	now := s.now()
	cutoff := now.Add(-idleSessionTTL)
	stuckCutoff := now.Add(-subscribedSessionTTL)

	s.mu.Lock()
	keys := make([]SessionKey, 0, len(s.sessions))
	for k := range s.sessions {
		keys = append(keys, k)
	}
	s.mu.Unlock()

	for _, k := range keys {
		s.mu.Lock()
		sr := s.sessions[k]
		s.mu.Unlock()
		if sr == nil {
			continue
		}
		sr.mu.Lock()
		idle := sr.lastActivity.Before(cutoff) && len(sr.subscribers) == 0
		stuck := sr.lastActivity.Before(stuckCutoff)
		sr.mu.Unlock()
		if idle || stuck {
			s.evictSession(k)
		}
	}
}

// Readback returns the last `limit` samples for a 2-party call+endpoint from
// the DB, oldest first. Used when in-memory state is empty (ended call,
// post-restart).
func (s *HealthStore) Readback(ctx context.Context, callID int64, endpoint string, limit int) ([]Sample, error) {
	if s.db == nil {
		return nil, nil
	}
	return s.readbackSession(ctx,
		`WHERE call_id = $1 AND endpoint = $2`,
		[]any{callID, endpoint},
		limit,
	)
}

// ReadbackEdge returns the last `limit` samples for a conference edge
// (from -> peer) from the DB, oldest first. Mirrors Readback for the
// conference path.
func (s *HealthStore) ReadbackEdge(ctx context.Context, confID uuid.UUID, from, peer string, limit int) ([]Sample, error) {
	if s.db == nil {
		return nil, nil
	}
	return s.readbackSession(ctx,
		`WHERE conference_id = $1 AND endpoint = $2 AND peer = $3`,
		[]any{confID, from, peer},
		limit,
	)
}

// readbackSession runs a parameterized readback query against call_link_health
// with a caller-supplied WHERE fragment. `where` must begin with "WHERE " and
// its placeholders must be numbered $1..$N in order of `args`. Returns samples
// oldest-first.
func (s *HealthStore) readbackSession(ctx context.Context, where string, args []any, limit int) ([]Sample, error) {
	// Compute $N before appending limit so the index is correct; swapping
	// these two lines would misalign the placeholder.
	limitPlaceholder := "$" + strconv.Itoa(len(args)+1)
	args = append(args, limit)
	sqlStr := `SELECT ts, loss_pct, jitter_ms, rtt_ms, conn_type, bytes_in, bytes_out
		 FROM call_link_health ` + where + `
		 ORDER BY ts DESC
		 LIMIT ` + limitPlaceholder

	rows, err := s.db.QueryContext(ctx, sqlStr, args...)
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
