package calls

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/dbutil"
)

type Call struct {
	ID         int64
	Caller     string
	Callee     string
	Status     string
	StartedAt  time.Time
	AnsweredAt *time.Time
	EndedAt    *time.Time
	DurationS  int
}

type activeCall struct {
	ID        int64
	Caller    string
	Callee    string
	StartedAt time.Time
}

// healthLifecycle is the subset of *HealthStore that Tracker drives for
// per-call lifecycle. Init is called at call start; Evict at call end.
type healthLifecycle interface {
	Init(callID int64)
	Evict(callID int64)
}

type Tracker struct {
	db     *db.Database
	mu     sync.Mutex
	active map[string]*activeCall // "caller→callee" → call
	health healthLifecycle
}

func New(d *db.Database) *Tracker {
	return &Tracker{
		db:     d,
		active: make(map[string]*activeCall),
	}
}

// SetHealthStore registers an optional health store for per-call lifecycle
// management. Safe to call once at startup; subsequent calls overwrite.
func (t *Tracker) SetHealthStore(h healthLifecycle) {
	t.mu.Lock()
	t.health = h
	t.mu.Unlock()
}

func callKey(a, b string) string {
	return a + "→" + b
}

func (t *Tracker) OnCallInitiated(from, to string) (int64, error) {
	var id int64
	err := t.db.DB.QueryRow(
		"INSERT INTO calls (caller, callee, status) VALUES ($1, $2, 'initiated') RETURNING id",
		from, to,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("track call: %w", err)
	}

	t.mu.Lock()
	t.active[callKey(from, to)] = &activeCall{
		ID:        id,
		Caller:    from,
		Callee:    to,
		StartedAt: time.Now(),
	}
	h := t.health
	t.mu.Unlock()

	if h != nil {
		h.Init(id)
	}
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
	var id int64
	if c, ok := t.active[key1]; ok {
		id = c.ID
	} else if c, ok := t.active[key2]; ok {
		id = c.ID
	}
	delete(t.active, key1)
	delete(t.active, key2)
	h := t.health
	t.mu.Unlock()

	if h != nil && id != 0 {
		h.Evict(id)
	}

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
	t.mu.Unlock()

	if h != nil {
		for _, id := range evictIDs {
			if id != 0 {
				h.Evict(id)
			}
		}
	}

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
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, c := range t.active {
		if c.Caller == number || c.Callee == number {
			return true
		}
	}
	return false
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

// CallIDFor returns the active call id for an endpoint phone number.
// Returns (0, false) if the number is not currently in a call.
func (t *Tracker) CallIDFor(number string) (int64, bool) {
	t.mu.Lock()
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
		`SELECT id, caller, callee, status, started_at, answered_at, ended_at, duration_s
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
			&c.StartedAt, &c.AnsweredAt, &c.EndedAt, &c.DurationS); err != nil {
			return nil, err
		}
		calls = append(calls, c)
	}
	return calls, rows.Err()
}

// GetCall returns the call row by id. Returns zero-value Call and nil error
// if not found (callers should test Call.ID == 0).
func (t *Tracker) GetCall(id int64) (Call, error) {
	var c Call
	var answered, ended sql.NullTime
	err := t.db.DB.QueryRow(
		`SELECT id, caller, callee, status, started_at, answered_at, ended_at, duration_s
		 FROM calls WHERE id = $1`, id,
	).Scan(&c.ID, &c.Caller, &c.Callee, &c.Status, &c.StartedAt, &answered, &ended, &c.DurationS)
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
	return c, nil
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
	query := fmt.Sprintf(`SELECT id, caller, callee, status, started_at, answered_at, ended_at, duration_s
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
			&c.StartedAt, &c.AnsweredAt, &c.EndedAt, &c.DurationS); err != nil {
			return nil, err
		}
		calls = append(calls, c)
	}
	return calls, rows.Err()
}
