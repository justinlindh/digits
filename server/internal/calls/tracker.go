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
	"github.com/lib/pq"

	"github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/dbutil"
)

// role strings written to the conference_members table. Must match the DB
// CHECK constraint defined in db.go and the wire constants in
// server/internal/signaling (signaling imports calls, so we can't share directly).
const (
	roleHost  = "host"
	roleAdded = "added"
)

type Call struct {
	ID                      int64
	Caller                  string
	Callee                  string
	Status                  string
	StartedAt               time.Time
	AnsweredAt              *time.Time
	EndedAt                 *time.Time
	DurationS               int
	EndReason               *string
	OriginatingConferenceID *uuid.UUID
	ForceEndedBy            *string // UUID of user who force-ended, nil if peer-initiated
}

type activeCall struct {
	ID        int64
	Caller    string
	Callee    string
	StartedAt time.Time
}

// healthLifecycle is the subset of *HealthStore that Tracker drives for
// per-session lifecycle. Init/Evict handle 2-party calls; InitConference/
// EvictConference handle 3-way conferences.
type healthLifecycle interface {
	Init(callID int64)
	Evict(callID int64)
	InitConference(confID uuid.UUID)
	EvictConference(confID uuid.UUID)
}

// dashNotifier is the subset of *dashboard/events.Broadcaster the Tracker
// uses to wake dashboard SSE subscribers when active-call count changes.
// Optional; nil disables notifications.
type dashNotifier interface {
	Notify()
}

// callEndObserver is notified after a 2-party call ends (either via
// OnCallEnded or ClearByNumber). Used by the relay to trigger pending
// call-return retries.
type callEndObserver interface {
	OnCallEndedNotify(ctx context.Context, caller, callee string)
}

type Tracker struct {
	db          *db.Database
	mu          sync.Mutex
	active      map[string]*activeCall // "caller→callee" → call
	conferences *ConferenceTracker
	health      healthLifecycle
	dashEvents  dashNotifier
	callEndObs  callEndObserver
	state       *CallState
}

func New(d *db.Database) *Tracker {
	return &Tracker{
		db:          d,
		active:      make(map[string]*activeCall),
		conferences: NewConferenceTracker(),
	}
}

// Conferences returns the conference tracker embedded in this Tracker.
func (t *Tracker) Conferences() *ConferenceTracker {
	return t.conferences
}

// SetHealthStore registers an optional health store for per-call lifecycle
// management. Safe to call once at startup; subsequent calls overwrite.
func (t *Tracker) SetHealthStore(h healthLifecycle) {
	t.mu.Lock()
	t.health = h
	t.mu.Unlock()
}

// SetDashboardEvents registers an optional broadcaster that is signalled
// whenever the active-call count changes. Wakes dashboard SSE subscribers.
// Safe to call once at startup; subsequent calls overwrite.
func (t *Tracker) SetDashboardEvents(b dashNotifier) {
	t.mu.Lock()
	t.dashEvents = b
	t.mu.Unlock()
}

// SetCallState registers an optional Redis-backed call state store for
// cluster-wide call queries. Safe to call once at startup; subsequent calls
// overwrite.
func (t *Tracker) SetCallState(cs *CallState) {
	t.mu.Lock()
	t.state = cs
	t.mu.Unlock()
}

// SetCallEndObserver registers an optional observer that is notified when a
// 2-party call ends. Safe to call once at startup; subsequent calls overwrite.
func (t *Tracker) SetCallEndObserver(obs callEndObserver) {
	t.mu.Lock()
	t.callEndObs = obs
	t.mu.Unlock()
}

func callKey(a, b string) string {
	return a + "→" + b
}

func (t *Tracker) OnCallInitiated(ctx context.Context, from, to string) (int64, error) {
	var id int64
	if err := t.db.DB.QueryRowContext(ctx,
		"INSERT INTO calls (caller, callee, status) VALUES ($1, $2, 'initiated') RETURNING id",
		from, to,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("insert call: %w", err)
	}

	t.mu.Lock()
	t.active[callKey(from, to)] = &activeCall{
		ID:        id,
		Caller:    from,
		Callee:    to,
		StartedAt: time.Now(),
	}
	h := t.health
	d := t.dashEvents
	s := t.state
	t.mu.Unlock()

	if s != nil {
		s.OnCallInitiated(ctx, id, from, to)
	}
	if h != nil {
		h.Init(id)
	}
	if d != nil {
		d.Notify()
	}
	return id, nil
}

func (t *Tracker) OnCallAnswered(ctx context.Context, caller, callee string) error {
	_, err := t.db.DB.ExecContext(ctx,
		`UPDATE calls SET status = 'connected', answered_at = CURRENT_TIMESTAMP
		 WHERE id = (
		   SELECT id FROM calls
		   WHERE caller = $1 AND callee = $2 AND status IN ('initiated', 'ringing')
		   ORDER BY started_at DESC LIMIT 1
		 )`,
		caller, callee,
	)
	return err
}

func (t *Tracker) OnCallEnded(ctx context.Context, caller, callee string) error {
	// Try both directions since either side can hang up
	key1 := callKey(caller, callee)
	key2 := callKey(callee, caller)

	t.mu.Lock()
	var id int64
	if c, ok := t.active[key1]; ok {
		id = c.ID
	} else if c, ok := t.active[key2]; ok {
		id = c.ID
	}
	removed := id != 0
	delete(t.active, key1)
	delete(t.active, key2)
	h := t.health
	d := t.dashEvents
	s := t.state
	obs := t.callEndObs
	t.mu.Unlock()

	if s != nil {
		s.OnCallEnded(ctx, caller, callee)
	}
	if h != nil && id != 0 {
		h.Evict(id)
	}
	if d != nil && removed {
		d.Notify()
	}

	_, err := t.db.DB.ExecContext(ctx,
		`UPDATE calls SET status = 'ended', ended_at = CURRENT_TIMESTAMP,
		 duration_s = EXTRACT(EPOCH FROM (CURRENT_TIMESTAMP - COALESCE(answered_at, started_at)))::INT
		 WHERE id = (
		   SELECT id FROM calls
		   WHERE ((caller = $1 AND callee = $2) OR (caller = $3 AND callee = $4))
		   AND status IN ('initiated', 'ringing', 'connected')
		   ORDER BY started_at DESC LIMIT 1
		 )`,
		caller, callee, callee, caller,
	)
	if obs != nil {
		obs.OnCallEndedNotify(ctx, caller, callee)
	}
	return err
}

// ClearByNumber removes all active calls involving the given number and ends
// them in the database. Used when a WebSocket disconnects unexpectedly.
func (t *Tracker) ClearByNumber(ctx context.Context, number string) {
	t.mu.Lock()
	var toDelete []string
	var evictIDs []int64
	for key, c := range t.active {
		if c.Caller == number || c.Callee == number {
			toDelete = append(toDelete, key)
			evictIDs = append(evictIDs, c.ID)
		}
	}
	for _, key := range toDelete {
		delete(t.active, key)
	}
	h := t.health
	d := t.dashEvents
	s := t.state
	obs := t.callEndObs
	removedAny := len(toDelete) > 0
	t.mu.Unlock()

	if s != nil {
		s.ClearByNumber(ctx, number)
	}
	if h != nil {
		for _, id := range evictIDs {
			if id != 0 {
				h.Evict(id)
			}
		}
	}
	if d != nil && removedAny {
		d.Notify()
	}

	// End any open calls in the database
	if _, err := t.db.DB.ExecContext(ctx,
		`UPDATE calls SET status = 'ended', ended_at = CURRENT_TIMESTAMP,
		 duration_s = EXTRACT(EPOCH FROM (CURRENT_TIMESTAMP - COALESCE(answered_at, started_at)))::INT
		 WHERE (caller = $1 OR callee = $1)
		 AND status IN ('initiated', 'ringing', 'connected')`,
		number,
	); err != nil {
		slog.Warn("clear calls on disconnect failed", "number", number, "err", err)
	}
	if obs != nil {
		obs.OnCallEndedNotify(ctx, number, "")
	}
}

// RenameNumber updates all call history records from oldNumber to newNumber
// in a single transaction.
func (t *Tracker) RenameNumber(ctx context.Context, oldNumber, newNumber string) error {
	tx, err := t.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rename number: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := []string{
		`UPDATE calls SET caller = $1 WHERE caller = $2`,
		`UPDATE calls SET callee = $1 WHERE callee = $2`,
		`UPDATE conferences SET host_phone = $1 WHERE host_phone = $2`,
		`UPDATE conference_members SET phone = $1 WHERE phone = $2`,
		`UPDATE conference_kicks SET kicked_phone = $1 WHERE kicked_phone = $2`,
	}
	for _, q := range queries {
		if _, err := tx.ExecContext(ctx, q, newNumber, oldNumber); err != nil {
			return fmt.Errorf("rename number in call history: %w", err)
		}
	}
	return tx.Commit()
}

func (t *Tracker) Busy(number string) bool {
	if t.conferences.IsBusy(number) {
		return true
	}
	t.mu.Lock()
	s := t.state
	if s != nil {
		t.mu.Unlock()
		return s.Busy(context.Background(), number)
	}
	defer t.mu.Unlock()
	for _, c := range t.active {
		if c.Caller == number || c.Callee == number {
			return true
		}
	}
	return false
}

// CanAddAsHost reports whether number may initiate a second 2-party call while
// already busy, as part of the party-line add-third-party flow. True only when:
//   - number is the caller (host) of exactly one active 2-party call
//   - number is not the callee of any active 2-party call
//   - number is not already a member of an active conference (3-party cap)
//
// Together with the normal Busy(to) check, this lets the 5ESS-style three-way
// flow work without allowing arbitrary multi-call spam.
func (t *Tracker) CanAddAsHost(number string) bool {
	if t.conferences.IsBusy(number) {
		return false
	}
	t.mu.Lock()
	s := t.state
	if s != nil {
		t.mu.Unlock()
		return s.CanAddAsHost(context.Background(), number)
	}
	defer t.mu.Unlock()
	callerCount := 0
	for _, c := range t.active {
		if c.Callee == number {
			return false
		}
		if c.Caller == number {
			callerCount++
		}
	}
	return callerCount == 1
}

// AllPeersOf returns all remote parties that number has active 2-party calls
// with. Empty if number has no active calls.
func (t *Tracker) AllPeersOf(number string) []string {
	t.mu.Lock()
	s := t.state
	if s != nil {
		t.mu.Unlock()
		return s.AllPeersOf(context.Background(), number)
	}
	defer t.mu.Unlock()
	var peers []string
	for _, c := range t.active {
		if c.Caller == number {
			peers = append(peers, c.Callee)
		} else if c.Callee == number {
			peers = append(peers, c.Caller)
		}
	}
	return peers
}

// PeerOf returns the other party in an active call involving number,
// or "" if number is not in any active call.
func (t *Tracker) PeerOf(number string) string {
	t.mu.Lock()
	s := t.state
	if s != nil {
		t.mu.Unlock()
		return s.PeerOf(context.Background(), number)
	}
	defer t.mu.Unlock()
	for _, c := range t.active {
		if c.Caller == number {
			return c.Callee
		}
		if c.Callee == number {
			return c.Caller
		}
	}
	return ""
}

// CallIDForPair returns the database call ID for an active call between a and
// b, or 0 if no such call exists. Used by conference setup to find the
// originating 2-party call id before migrating to mesh.
func (t *Tracker) CallIDForPair(a, b string) int64 {
	t.mu.Lock()
	s := t.state
	if s != nil {
		t.mu.Unlock()
		return s.CallIDForPair(context.Background(), a, b)
	}
	defer t.mu.Unlock()
	if c, ok := t.active[callKey(a, b)]; ok {
		return c.ID
	}
	if c, ok := t.active[callKey(b, a)]; ok {
		return c.ID
	}
	return 0
}

// CallIDFor returns the active call id for an endpoint phone number.
// Returns (0, false) if the number is not currently in a call.
func (t *Tracker) CallIDFor(number string) (int64, bool) {
	t.mu.Lock()
	s := t.state
	if s != nil {
		t.mu.Unlock()
		return s.CallIDFor(context.Background(), number)
	}
	defer t.mu.Unlock()
	for _, c := range t.active {
		if c.Caller == number || c.Callee == number {
			return c.ID, true
		}
	}
	return 0, false
}

func (t *Tracker) InCall(a, b string) bool {
	t.mu.Lock()
	s := t.state
	if s != nil {
		t.mu.Unlock()
		return s.InCall(context.Background(), a, b)
	}
	defer t.mu.Unlock()
	_, fwd := t.active[callKey(a, b)]
	_, rev := t.active[callKey(b, a)]
	return fwd || rev
}

func (t *Tracker) Active() []activeCall {
	t.mu.Lock()
	s := t.state
	if s != nil {
		t.mu.Unlock()
		return s.Active(context.Background())
	}
	defer t.mu.Unlock()
	calls := make([]activeCall, 0, len(t.active))
	for _, c := range t.active {
		calls = append(calls, *c)
	}
	return calls
}

// callColumns is the SELECT list for queries that scan into a Call via
// scanCallRows. Keep the order in sync with the scan there.
const callColumns = `id, caller, callee, status, started_at, answered_at, ended_at, duration_s,
	end_reason, originating_conference_id, force_ended_by`

func scanCallRows(rows *sql.Rows) ([]Call, error) {
	var calls []Call
	for rows.Next() {
		var c Call
		var feb sql.NullString
		if err := rows.Scan(&c.ID, &c.Caller, &c.Callee, &c.Status,
			&c.StartedAt, &c.AnsweredAt, &c.EndedAt, &c.DurationS,
			&c.EndReason, &c.OriginatingConferenceID, &feb); err != nil {
			return nil, err
		}
		if feb.Valid {
			s := feb.String
			c.ForceEndedBy = &s
		}
		calls = append(calls, c)
	}
	return calls, rows.Err()
}

// MarkForceEnded records which user force-ended a call. Returns nil error
// even if no rows matched (idempotent against racing peer hangups).
func (t *Tracker) MarkForceEnded(ctx context.Context, callID int64, userID string) error {
	_, err := t.db.DB.ExecContext(ctx,
		`UPDATE calls SET force_ended_by = $1 WHERE id = $2`,
		userID, callID,
	)
	if err != nil {
		return fmt.Errorf("mark force ended: %w", err)
	}
	return nil
}

// CreateConferencePersistent creates an in-memory conference and writes it to
// the database atomically. All pre-merge 2-party calls involving conference
// members are marked ended with end_reason='merged_to_conference' in the same
// transaction so they are excluded from call history.
func (t *Tracker) CreateConferencePersistent(ctx context.Context, host string, originatingCallID int64, addedMembers []string) (*Conference, error) {
	// Collect the call IDs for every added member (A↔C, A↔D, …) before
	// creating the conference so the active map is still intact.
	addedCallIDs := make([]int64, 0, len(addedMembers))
	for _, member := range addedMembers {
		cid := t.CallIDForPair(host, member)
		if cid == 0 {
			return nil, fmt.Errorf("no active call between %s and %s", host, member)
		}
		addedCallIDs = append(addedCallIDs, cid)
	}

	conf, err := t.conferences.CreateConference(host, originatingCallID, addedMembers)
	if err != nil {
		return nil, err
	}

	txErr := dbutil.WithTx(ctx, t.db.DB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO conferences (id, host_phone, originating_call_id, state) VALUES ($1, $2, $3, 'active')`,
			conf.ID, conf.Host, conf.OriginatingCallID,
		); err != nil {
			return fmt.Errorf("insert conference: %w", err)
		}
		for _, m := range conf.Members {
			role := roleAdded
			if m.Role == ConferenceRoleHost {
				role = roleHost
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO conference_members (conference_id, phone, role) VALUES ($1, $2, $3)`,
				conf.ID, m.Phone, role,
			); err != nil {
				return fmt.Errorf("insert member %s: %w", m.Phone, err)
			}
		}
		// Mark ALL pre-merge calls (originating + every add-leg) as ended with
		// end_reason='merged_to_conference' so they are excluded from call history.
		allPreMergeIDs := append([]int64{conf.OriginatingCallID}, addedCallIDs...)
		for _, cid := range allPreMergeIDs {
			if _, err := tx.ExecContext(ctx,
				`UPDATE calls SET status = 'ended', end_reason = 'merged_to_conference',
				 ended_at = CURRENT_TIMESTAMP WHERE id = $1`,
				cid,
			); err != nil {
				return fmt.Errorf("mark call %d ended: %w", cid, err)
			}
		}
		return nil
	})
	if txErr != nil {
		_, _ = t.conferences.EndConference(conf.ID, "db_error")
		return nil, txErr
	}

	// Remove conference members from the 2-party active map. Membership is
	// now tracked exclusively through the conference tracker.
	t.mu.Lock()
	for k, ac := range t.active {
		if _, ok := conf.Members[ac.Caller]; ok {
			delete(t.active, k)
			continue
		}
		if _, ok := conf.Members[ac.Callee]; ok {
			delete(t.active, k)
		}
	}
	h := t.health
	s := t.state
	t.mu.Unlock()

	if s != nil {
		for _, member := range addedMembers {
			s.OnCallEnded(ctx, host, member)
		}
	}
	if h != nil {
		h.InitConference(conf.ID)
	}
	slog.Info("conference: persisted", "conf_id", conf.ID.String(), "host", host, "originating_call_id", originatingCallID, "added_members", addedMembers)
	return conf, nil
}

// EndConferencePersistent ends the in-memory conference and writes the final
// state to the database atomically.
func (t *Tracker) EndConferencePersistent(ctx context.Context, confID uuid.UUID, reason string) error {
	if _, err := t.conferences.EndConference(confID, reason); err != nil {
		return err
	}
	t.mu.Lock()
	h := t.health
	t.mu.Unlock()

	if err := dbutil.WithTx(ctx, t.db.DB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE conferences SET state = 'ended', ended_at = NOW(), end_reason = $1 WHERE id = $2`,
			reason, confID,
		); err != nil {
			return fmt.Errorf("update conference: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE conference_members SET left_at = NOW(), left_reason = $1
			 WHERE conference_id = $2 AND left_at IS NULL`,
			reason, confID,
		); err != nil {
			return fmt.Errorf("update members: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	if h != nil {
		h.EvictConference(confID)
	}
	slog.Info("conference: end persisted", "conf_id", confID.String(), "reason", reason)
	return nil
}

// RecordKick writes one audit row to conference_kicks. Append-only; no
// update path. userID is the authenticated user who triggered the kick.
//
// Callers audit BEFORE invoking the kick so a failed teardown still
// records the attempt; an orphaned audit row for a failed kick is the
// intentional trade-off. Conference cascade delete cleans orphans when
// the parent conference row is eventually removed.
func (t *Tracker) RecordKick(ctx context.Context, confID uuid.UUID, kickedPhone, userID string) error {
	_, err := t.db.DB.ExecContext(ctx,
		`INSERT INTO conference_kicks (conference_id, kicked_phone, kicked_by_user_id) VALUES ($1, $2, $3)`,
		confID, kickedPhone, userID,
	)
	if err != nil {
		return fmt.Errorf("record kick: %w", err)
	}
	slog.Info("conference: kick audited", "conf_id", confID.String(), "kicked", kickedPhone, "by_user", userID)
	return nil
}

// GetConferenceByID returns a conference summary from the DB by ID. Returns
// (nil, nil) if not found. Handles both active and ended conferences.
func (t *Tracker) GetConferenceByID(ctx context.Context, confID uuid.UUID) (*ConferenceSummary, error) {
	const query = `
		SELECT c.id, c.host_phone, c.originating_call_id, c.created_at, c.ended_at,
		       c.end_reason,
		       COALESCE(EXTRACT(EPOCH FROM (c.ended_at - c.created_at))::INT, 0) AS duration_s,
		       array_agg(m.phone ORDER BY CASE WHEN m.phone = c.host_phone THEN 0 ELSE 1 END, m.phone) AS members
		FROM conferences c
		JOIN conference_members m ON m.conference_id = c.id
		WHERE c.id = $1
		GROUP BY c.id, c.host_phone, c.originating_call_id, c.created_at, c.ended_at, c.end_reason`

	var cs ConferenceSummary
	var endReason *string
	var members pq.StringArray
	err := t.db.DB.QueryRowContext(ctx, query, confID).Scan(
		&cs.ID, &cs.Host, &cs.OriginatingCallID, &cs.CreatedAt, &cs.EndedAt,
		&endReason, &cs.DurationS, &members,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get conference %s: %w", confID, err)
	}
	if endReason != nil {
		cs.EndReason = *endReason
	}
	cs.Members = []string(members)
	return &cs, nil
}

// DropMemberPersistent drops a single member from an active conference, ends the
// conference (v1 caps at 3; any drop to 2 terminates), persists all state changes,
// and (for the 2 surviving members) inserts a fresh calls row stamped with
// originating_conference_id so Busy() and call history stay consistent after the
// conference ends but the surviving pair's PC continues as a regular 2-party call.
func (t *Tracker) DropMemberPersistent(ctx context.Context, confID uuid.UUID, phone, reason string) (remaining []string, ended bool, err error) {
	// Bail early with a useful error if the phone has no active conference.
	if t.conferences.ConferenceByPhone(phone) == nil {
		return nil, false, fmt.Errorf("phone %s is not in any active conference", phone)
	}

	remaining, ended, err = t.conferences.DropMember(confID, phone, reason)
	if err != nil {
		return nil, false, err
	}

	var continuationCallID int64
	// DB failure past this point does not roll back the in-memory state
	// (symmetric with EndConferencePersistent).
	if txErr := dbutil.WithTx(ctx, t.db.DB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE conference_members SET left_at = NOW(), left_reason = $1
			 WHERE conference_id = $2 AND phone = $3`,
			reason, confID, phone,
		); err != nil {
			return fmt.Errorf("update dropped member: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE conferences SET state = 'ended', ended_at = NOW(), end_reason = 'member_left'
			 WHERE id = $1`,
			confID,
		); err != nil {
			return fmt.Errorf("end conference: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE conference_members SET left_at = NOW(), left_reason = 'conference_ended'
			 WHERE conference_id = $1 AND left_at IS NULL`,
			confID,
		); err != nil {
			return fmt.Errorf("end remaining members: %w", err)
		}
		if len(remaining) == 2 {
			// Sort for stable caller/callee ordering.
			a, b := remaining[0], remaining[1]
			if b < a {
				a, b = b, a
			}
			if err := tx.QueryRowContext(ctx,
				`INSERT INTO calls (caller, callee, status, originating_conference_id)
				 VALUES ($1, $2, 'connected', $3) RETURNING id`,
				a, b, confID,
			).Scan(&continuationCallID); err != nil {
				return fmt.Errorf("insert continuation call: %w", err)
			}
		}
		return nil
	}); txErr != nil {
		return nil, false, txErr
	}

	// Register the surviving pair in the 2-party active map so Busy() and
	// InCall() continue to work after the conference ends. The conference
	// tracker already removed them from its own memberIndex in DropMember.
	t.mu.Lock()
	if len(remaining) == 2 {
		a, b := remaining[0], remaining[1]
		if b < a {
			a, b = b, a
		}
		t.active[callKey(a, b)] = &activeCall{ID: continuationCallID, Caller: a, Callee: b, StartedAt: time.Now()}
	}
	h := t.health
	s := t.state
	t.mu.Unlock()

	if s != nil && len(remaining) == 2 {
		a, b := remaining[0], remaining[1]
		if b < a {
			a, b = b, a
		}
		s.OnCallInitiated(ctx, continuationCallID, a, b)
	}
	if h != nil && ended {
		h.EvictConference(confID)
	}
	slog.Info("conference: drop persisted", "conf_id", confID.String(), "dropped", phone, "reason", reason, "remaining", remaining, "ended", ended)
	return remaining, ended, nil
}

// GetCall returns the call row by id. Returns zero-value Call and nil error
// if not found (callers should test Call.ID == 0).
func (t *Tracker) GetCall(ctx context.Context, id int64) (Call, error) {
	var c Call
	var answered, ended sql.NullTime
	var forceEndedBy sql.NullString
	err := t.db.DB.QueryRowContext(ctx,
		`SELECT id, caller, callee, status, started_at, answered_at, ended_at, duration_s,
		        end_reason, originating_conference_id, force_ended_by
		 FROM calls WHERE id = $1`, id,
	).Scan(&c.ID, &c.Caller, &c.Callee, &c.Status, &c.StartedAt, &answered, &ended, &c.DurationS,
		&c.EndReason, &c.OriginatingConferenceID, &forceEndedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return Call{}, nil
	}
	if err != nil {
		return Call{}, fmt.Errorf("get call: %w", err)
	}
	if answered.Valid {
		c.AnsweredAt = &answered.Time
	}
	if ended.Valid {
		c.EndedAt = &ended.Time
	}
	if forceEndedBy.Valid {
		s := forceEndedBy.String
		c.ForceEndedBy = &s
	}
	return c, nil
}

// LastInboundCaller returns the caller number of the most recent call delivered
// to number, excluding conference-merge bookkeeping rows. Returns ("", nil) if
// no qualifying call exists.
func (t *Tracker) LastInboundCaller(ctx context.Context, number string) (string, error) {
	var caller string
	err := t.db.DB.QueryRowContext(ctx,
		`SELECT caller FROM calls
		 WHERE callee = $1
		   AND end_reason IS DISTINCT FROM 'merged_to_conference'
		 ORDER BY started_at DESC
		 LIMIT 1`,
		number,
	).Scan(&caller)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("last inbound caller: %w", err)
	}
	return caller, nil
}

// RecentForPhones returns the most recent calls where either caller or callee
// matches one of the given phone numbers.
func (t *Tracker) RecentForPhones(ctx context.Context, phoneNumbers []string, limit int) ([]Call, error) {
	if len(phoneNumbers) == 0 {
		return nil, nil
	}

	// Build IN clause — reuse same $1..$N placeholders for both caller and callee
	n := len(phoneNumbers)
	ph := dbutil.Placeholders(n)
	query := fmt.Sprintf(
		`SELECT `+callColumns+
			` FROM calls WHERE caller IN (%s) OR callee IN (%s) ORDER BY started_at DESC LIMIT $%d`,
		ph, ph, n+1)
	args := make([]interface{}, 0, n+1)
	for _, num := range phoneNumbers {
		args = append(args, num)
	}
	args = append(args, limit)

	rows, err := t.db.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanCallRows(rows)
}
