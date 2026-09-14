// Package admin provides the read-only, cross-household aggregate queries
// behind the operator's /admin page. Everything here is a plain SELECT over
// the existing schema; the package owns no tables.
package admin

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// Store runs the admin aggregate queries against Postgres.
type Store struct {
	db *sql.DB
}

// NewStore wraps an existing *sql.DB.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Totals is the fleet-wide headline count set. Calls counts rows started at
// or after the window start passed to Totals.
type Totals struct {
	Users         int
	Households    int
	Lines         int
	PairedDevices int
	Calls         int
}

// Totals returns the headline counts. since bounds the call count.
func (s *Store) Totals(ctx context.Context, since time.Time) (Totals, error) {
	var t Totals
	err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM users),
			(SELECT COUNT(*) FROM households),
			(SELECT COUNT(*) FROM lines),
			(SELECT COUNT(*) FROM devices WHERE paired_at IS NOT NULL),
			(SELECT COUNT(*) FROM calls WHERE started_at >= $1)`,
		since,
	).Scan(&t.Users, &t.Households, &t.Lines, &t.PairedDevices, &t.Calls)
	if err != nil {
		return Totals{}, fmt.Errorf("admin totals: %w", err)
	}
	return t, nil
}

// Account is one user row with the names of the households they belong to.
type Account struct {
	ID          string
	Email       string
	Name        string
	CreatedAt   time.Time
	LastLoginAt *time.Time
	DisabledAt  *time.Time
	Households  []string
}

// Accounts lists every user, newest first.
func (s *Store) Accounts(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.email, u.name, u.created_at, u.last_login_at, u.disabled_at,
			COALESCE((
				SELECT array_agg(h.name ORDER BY h.name)
				FROM household_members m JOIN households h ON h.id = m.household_id
				WHERE m.user_id = u.id
			), '{}')
		FROM users u
		ORDER BY u.created_at DESC, u.email`)
	if err != nil {
		return nil, fmt.Errorf("admin accounts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.Email, &a.Name, &a.CreatedAt, &a.LastLoginAt, &a.DisabledAt, pq.Array(&a.Households)); err != nil {
			return nil, fmt.Errorf("admin accounts scan: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin accounts rows: %w", err)
	}
	return out, nil
}

// Household is one household with its membership, line numbers, paired
// device count, and the number of calls in the window that touched any of
// its lines as caller or callee. Calls is always zero for a household that
// has call history turned off: the operator view is a subset of what the
// household chose to keep for itself, never more.
type Household struct {
	ID                 string
	Name               string
	CreatedAt          time.Time
	Members            []string
	Lines              []string
	PairedDevices      int
	CallHistoryEnabled bool
	Calls              int
}

// Households lists every household, oldest first. since bounds the per
// household call count.
func (s *Store) Households(ctx context.Context, since time.Time) ([]Household, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT h.id, h.name, h.created_at,
			COALESCE((
				SELECT array_agg(u.email ORDER BY u.email)
				FROM household_members m JOIN users u ON u.id = m.user_id
				WHERE m.household_id = h.id
			), '{}'),
			COALESCE((
				SELECT array_agg(l.number ORDER BY l.number)
				FROM lines l WHERE l.household_id = h.id
			), '{}'),
			(SELECT COUNT(*) FROM devices d JOIN lines l ON l.id = d.line_id
				WHERE l.household_id = h.id AND d.paired_at IS NOT NULL),
			h.call_history_enabled,
			CASE WHEN h.call_history_enabled THEN
				(SELECT COUNT(*) FROM calls c
					WHERE c.started_at >= $1
					AND (c.caller IN (SELECT number FROM lines WHERE household_id = h.id)
						OR c.callee IN (SELECT number FROM lines WHERE household_id = h.id)))
			ELSE 0 END
		FROM households h
		ORDER BY h.created_at, h.name`,
		since,
	)
	if err != nil {
		return nil, fmt.Errorf("admin households: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Household
	for rows.Next() {
		var h Household
		if err := rows.Scan(&h.ID, &h.Name, &h.CreatedAt, pq.Array(&h.Members), pq.Array(&h.Lines), &h.PairedDevices, &h.CallHistoryEnabled, &h.Calls); err != nil {
			return nil, fmt.Errorf("admin households scan: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin households rows: %w", err)
	}
	return out, nil
}

// DayCount is one calendar day's call activity. Day is midnight in the
// location passed to CallsPerDay.
type DayCount struct {
	Day       time.Time
	Calls     int
	Answered  int
	DurationS int
}

// CallsPerDay buckets calls started in [since, until] by calendar day in loc.
// Every day from since through until appears in the result, zero-filled, so
// the caller can render a fixed-width table without gap handling.
func (s *Store) CallsPerDay(ctx context.Context, since, until time.Time, loc *time.Location) ([]DayCount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT (started_at AT TIME ZONE $2)::date AS day,
			COUNT(*), COUNT(answered_at), COALESCE(SUM(duration_s), 0)
		FROM calls
		WHERE started_at >= $1 AND started_at <= $3
		GROUP BY day`,
		since, loc.String(), until,
	)
	if err != nil {
		return nil, fmt.Errorf("admin calls per day: %w", err)
	}
	defer func() { _ = rows.Close() }()

	counts := map[string]DayCount{}
	for rows.Next() {
		var day time.Time
		var dc DayCount
		if err := rows.Scan(&day, &dc.Calls, &dc.Answered, &dc.DurationS); err != nil {
			return nil, fmt.Errorf("admin calls per day scan: %w", err)
		}
		// The date column comes back as a timestamp at UTC midnight; key on
		// its calendar date only.
		counts[day.Format("2006-01-02")] = dc
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin calls per day rows: %w", err)
	}

	start := since.In(loc)
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)
	end := until.In(loc)
	end = time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, loc)
	var out []DayCount
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		dc := counts[d.Format("2006-01-02")]
		dc.Day = d
		out = append(out, dc)
	}
	return out, nil
}
