package calls

import (
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
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

// withTx runs fn inside a database transaction. Commits on success,
// rolls back on error or panic.
func withTx(d *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	committed = true
	return nil
}

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
}

type activeCall struct {
	Caller    string
	Callee    string
	StartedAt time.Time
	callID    int64
}

type Tracker struct {
	db          *db.Database
	mu          sync.Mutex
	active      map[string]*activeCall // "caller→callee" → call
	conferences *ConferenceTracker
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

func callKey(a, b string) string {
	return a + "→" + b
}

func (t *Tracker) OnCallInitiated(from, to string) (int64, error) {
	var id int64
	if err := t.db.DB.QueryRow(
		"INSERT INTO calls (caller, callee, status) VALUES ($1, $2, 'initiated') RETURNING id",
		from, to,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("insert call: %w", err)
	}
	t.mu.Lock()
	t.active[callKey(from, to)] = &activeCall{Caller: from, Callee: to, StartedAt: time.Now(), callID: id}
	t.mu.Unlock()
	return id, nil
}

func (t *Tracker) OnCallAnswered(caller, callee string) error {
	_, err := t.db.DB.Exec(
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

func (t *Tracker) OnCallEnded(caller, callee string) error {
	// Try both directions since either side can hang up
	key1 := callKey(caller, callee)
	key2 := callKey(callee, caller)

	t.mu.Lock()
	delete(t.active, key1)
	delete(t.active, key2)
	t.mu.Unlock()

	_, err := t.db.DB.Exec(
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
	return err
}

// ClearByNumber removes all active calls involving the given number and ends
// them in the database. Used when a WebSocket disconnects unexpectedly.
func (t *Tracker) ClearByNumber(number string) {
	t.mu.Lock()
	var toDelete []string
	for key, c := range t.active {
		if c.Caller == number || c.Callee == number {
			toDelete = append(toDelete, key)
		}
	}
	for _, key := range toDelete {
		delete(t.active, key)
	}
	t.mu.Unlock()

	// End any open calls in the database
	if _, err := t.db.DB.Exec(
		`UPDATE calls SET status = 'ended', ended_at = CURRENT_TIMESTAMP,
		 duration_s = EXTRACT(EPOCH FROM (CURRENT_TIMESTAMP - COALESCE(answered_at, started_at)))::INT
		 WHERE (caller = $1 OR callee = $1)
		 AND status IN ('initiated', 'ringing', 'connected')`,
		number,
	); err != nil {
		slog.Warn("clear calls on disconnect failed", "number", number, "err", err)
	}
}

func (t *Tracker) Busy(number string) bool {
	if t.conferences.IsBusy(number) {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, c := range t.active {
		if c.Caller == number || c.Callee == number {
			return true
		}
	}
	return false
}

// AllPeersOf returns all remote parties that number has active 2-party calls
// with. Empty if number has no active calls.
func (t *Tracker) AllPeersOf(number string) []string {
	t.mu.Lock()
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

// CallIDFor returns the database call ID for an active call between a and b,
// or 0 if no such call exists.
func (t *Tracker) CallIDFor(a, b string) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if c, ok := t.active[callKey(a, b)]; ok {
		return c.callID
	}
	if c, ok := t.active[callKey(b, a)]; ok {
		return c.callID
	}
	return 0
}

func (t *Tracker) InCall(a, b string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, fwd := t.active[callKey(a, b)]
	_, rev := t.active[callKey(b, a)]
	return fwd || rev
}

func (t *Tracker) Active() []activeCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	calls := make([]activeCall, 0, len(t.active))
	for _, c := range t.active {
		calls = append(calls, *c)
	}
	return calls
}

func (t *Tracker) Recent(limit int) ([]Call, error) {
	rows, err := t.db.DB.Query(
		`SELECT id, caller, callee, status, started_at, answered_at, ended_at, duration_s,
		        end_reason, originating_conference_id
		 FROM calls ORDER BY started_at DESC LIMIT $1`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var calls []Call
	for rows.Next() {
		var c Call
		if err := rows.Scan(&c.ID, &c.Caller, &c.Callee, &c.Status,
			&c.StartedAt, &c.AnsweredAt, &c.EndedAt, &c.DurationS,
			&c.EndReason, &c.OriginatingConferenceID); err != nil {
			return nil, err
		}
		calls = append(calls, c)
	}
	return calls, rows.Err()
}

// CreateConferencePersistent creates an in-memory conference and writes it to
// the database atomically. All pre-merge 2-party calls involving conference
// members are marked ended with end_reason='merged_to_conference' in the same
// transaction so they are excluded from call history.
func (t *Tracker) CreateConferencePersistent(host string, originatingCallID int64, addedMembers []string) (*Conference, error) {
	// Collect the call IDs for every added member (A↔C, A↔D, …) before
	// creating the conference so the active map is still intact.
	addedCallIDs := make([]int64, 0, len(addedMembers))
	for _, member := range addedMembers {
		cid := t.CallIDFor(host, member)
		if cid == 0 {
			return nil, fmt.Errorf("no active call between %s and %s", host, member)
		}
		addedCallIDs = append(addedCallIDs, cid)
	}

	conf, err := t.conferences.CreateConference(host, originatingCallID, addedMembers)
	if err != nil {
		return nil, err
	}

	txErr := withTx(t.db.DB, func(tx *sql.Tx) error {
		if _, err := tx.Exec(
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
			if _, err := tx.Exec(
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
			if _, err := tx.Exec(
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
	t.mu.Unlock()

	return conf, nil
}

// EndConferencePersistent ends the in-memory conference and writes the final
// state to the database atomically.
func (t *Tracker) EndConferencePersistent(confID uuid.UUID, reason string) error {
	if _, err := t.conferences.EndConference(confID, reason); err != nil {
		return err
	}
	return withTx(t.db.DB, func(tx *sql.Tx) error {
		if _, err := tx.Exec(
			`UPDATE conferences SET state = 'ended', ended_at = NOW(), end_reason = $1 WHERE id = $2`,
			reason, confID,
		); err != nil {
			return fmt.Errorf("update conference: %w", err)
		}
		if _, err := tx.Exec(
			`UPDATE conference_members SET left_at = NOW(), left_reason = $1
			 WHERE conference_id = $2 AND left_at IS NULL`,
			reason, confID,
		); err != nil {
			return fmt.Errorf("update members: %w", err)
		}
		return nil
	})
}

// DropMemberPersistent drops a single member from an active conference, ends the
// conference (v1 caps at 3; any drop to 2 terminates), persists all state changes,
// and (for the 2 surviving members) inserts a fresh calls row stamped with
// originating_conference_id so Busy() and call history stay consistent after the
// conference ends but the surviving pair's PC continues as a regular 2-party call.
func (t *Tracker) DropMemberPersistent(confID uuid.UUID, phone, reason string) (remaining []string, ended bool, err error) {
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
	if txErr := withTx(t.db.DB, func(tx *sql.Tx) error {
		if _, err := tx.Exec(
			`UPDATE conference_members SET left_at = NOW(), left_reason = $1
			 WHERE conference_id = $2 AND phone = $3`,
			reason, confID, phone,
		); err != nil {
			return fmt.Errorf("update dropped member: %w", err)
		}
		if _, err := tx.Exec(
			`UPDATE conferences SET state = 'ended', ended_at = NOW(), end_reason = 'member_left'
			 WHERE id = $1`,
			confID,
		); err != nil {
			return fmt.Errorf("end conference: %w", err)
		}
		if _, err := tx.Exec(
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
			if err := tx.QueryRow(
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
	if len(remaining) == 2 {
		a, b := remaining[0], remaining[1]
		if b < a {
			a, b = b, a
		}
		t.mu.Lock()
		t.active[callKey(a, b)] = &activeCall{Caller: a, Callee: b, StartedAt: time.Now(), callID: continuationCallID}
		t.mu.Unlock()
	}

	return remaining, ended, nil
}

// RecentForPhones returns the most recent calls where either caller or callee
// matches one of the given phone numbers.
func (t *Tracker) RecentForPhones(phoneNumbers []string, limit int) ([]Call, error) {
	if len(phoneNumbers) == 0 {
		return nil, nil
	}

	// Build IN clause — reuse same $1..$N placeholders for both caller and callee
	n := len(phoneNumbers)
	ph := dbutil.Placeholders(n, 0)
	query := fmt.Sprintf(
		`SELECT id, caller, callee, status, started_at, answered_at, ended_at, duration_s,
		        end_reason, originating_conference_id
		 FROM calls WHERE caller IN (%s) OR callee IN (%s) ORDER BY started_at DESC LIMIT $%d`,
		ph, ph, n+1)
	args := make([]interface{}, 0, n+1)
	for _, num := range phoneNumbers {
		args = append(args, num)
	}
	args = append(args, limit)

	rows, err := t.db.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var calls []Call
	for rows.Next() {
		var c Call
		if err := rows.Scan(&c.ID, &c.Caller, &c.Callee, &c.Status,
			&c.StartedAt, &c.AnsweredAt, &c.EndedAt, &c.DurationS,
			&c.EndReason, &c.OriginatingConferenceID); err != nil {
			return nil, err
		}
		calls = append(calls, c)
	}
	return calls, rows.Err()
}
