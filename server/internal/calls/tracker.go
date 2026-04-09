package calls

import (
	"fmt"
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
	Caller string
	Callee string
}

type Tracker struct {
	db     *db.Database
	mu     sync.Mutex
	active map[string]*activeCall // "caller→callee" → call
}

func New(d *db.Database) *Tracker {
	return &Tracker{
		db:     d,
		active: make(map[string]*activeCall),
	}
}

func callKey(a, b string) string {
	return a + "→" + b
}

func (t *Tracker) OnCallInitiated(from, to string) error {
	_, err := t.db.DB.Exec(
		"INSERT INTO calls (caller, callee, status) VALUES ($1, $2, 'initiated')",
		from, to,
	)
	if err != nil {
		return fmt.Errorf("track call: %w", err)
	}
	t.mu.Lock()
	t.active[callKey(from, to)] = &activeCall{Caller: from, Callee: to}
	t.mu.Unlock()
	return nil
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
	defer rows.Close()

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
	defer rows.Close()

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
