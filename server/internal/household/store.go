package household

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/justinlindh/digits/server/internal/dbutil"
)

// Household represents a family/household group.
type Household struct {
	ID                 string
	Name               string
	CallHistoryEnabled bool
	DoNotDisturb       bool
	Timezone           string
	CreatedAt          time.Time
}

// Store provides household persistence backed by Postgres.
type Store struct {
	db *sql.DB
}

// NewStore wraps an existing *sql.DB.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Create inserts a new household and adds ownerUserID as an admin member in a single transaction.
func (s *Store) Create(ctx context.Context, name, ownerUserID string) (*Household, error) {
	h := &Household{}
	if err := dbutil.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO households (name) VALUES ($1) RETURNING id, name, timezone, created_at`,
			name,
		).Scan(&h.ID, &h.Name, &h.Timezone, &h.CreatedAt); err != nil {
			return fmt.Errorf("insert household: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO household_members (user_id, household_id, role) VALUES ($1, $2, 'admin')`,
			ownerUserID, h.ID,
		); err != nil {
			return fmt.Errorf("insert owner member: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return h, nil
}

// GetByID retrieves a household by its UUID.
func (s *Store) GetByID(ctx context.Context, id string) (*Household, error) {
	h := &Household{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, call_history_enabled, do_not_disturb, timezone, created_at FROM households WHERE id = $1`,
		id,
	).Scan(&h.ID, &h.Name, &h.CallHistoryEnabled, &h.DoNotDisturb, &h.Timezone, &h.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("household not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get household: %w", err)
	}
	return h, nil
}

// GetForUser returns all households the given user belongs to.
func (s *Store) GetForUser(ctx context.Context, userID string) ([]*Household, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT h.id, h.name, h.call_history_enabled, h.do_not_disturb, h.timezone, h.created_at
		 FROM households h
		 JOIN household_members m ON m.household_id = h.id
		 WHERE m.user_id = $1
		 ORDER BY h.created_at`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("get households for user: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var households []*Household
	for rows.Next() {
		h := &Household{}
		if err := rows.Scan(&h.ID, &h.Name, &h.CallHistoryEnabled, &h.DoNotDisturb, &h.Timezone, &h.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan household: %w", err)
		}
		households = append(households, h)
	}
	return households, rows.Err()
}

// GetRole returns the role of a user in a given household, or an error if not a member.
func (s *Store) GetRole(ctx context.Context, userID, householdID string) (string, error) {
	var role string
	err := s.db.QueryRowContext(ctx,
		`SELECT role FROM household_members WHERE user_id = $1 AND household_id = $2`,
		userID, householdID,
	).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("user is not a member of this household")
	}
	if err != nil {
		return "", fmt.Errorf("get role: %w", err)
	}
	return role, nil
}

// AddMember adds a user to a household with the given role. If the user is already
// a member, their role is updated.
func (s *Store) AddMember(ctx context.Context, userID, householdID, role string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO household_members (user_id, household_id, role)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, household_id) DO UPDATE SET role = EXCLUDED.role`,
		userID, householdID, role,
	)
	if err != nil {
		return fmt.Errorf("add member: %w", err)
	}
	return nil
}

// MemberWithUser includes user profile data alongside membership info.
type MemberWithUser struct {
	UserID string
	Email  string
	Name   string
	Role   string
}

// GetMembersWithUsers returns all members of a household with their user profile data.
func (s *Store) GetMembersWithUsers(ctx context.Context, householdID string) ([]MemberWithUser, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT hm.user_id, u.email, u.name, hm.role
		 FROM household_members hm
		 JOIN users u ON u.id = hm.user_id
		 WHERE hm.household_id = $1`,
		householdID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var members []MemberWithUser
	for rows.Next() {
		var m MemberWithUser
		if err := rows.Scan(&m.UserID, &m.Email, &m.Name, &m.Role); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// IsMemberByEmail checks if a user with the given email is a member of the household.
func (s *Store) IsMemberByEmail(ctx context.Context, householdID, email string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM household_members hm
		 JOIN users u ON u.id = hm.user_id
		 WHERE hm.household_id = $1 AND lower(u.email) = lower($2)`,
		householdID, email,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check member by email: %w", err)
	}
	return count > 0, nil
}

// CountHouseholds returns the total number of households.
func (s *Store) CountHouseholds(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM households`).Scan(&count)
	return count, err
}

// UpdateName changes the name of a household.
func (s *Store) UpdateName(ctx context.Context, householdID, name string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE households SET name = $1 WHERE id = $2`,
		name, householdID,
	)
	if err != nil {
		return fmt.Errorf("update household name: %w", err)
	}
	return nil
}

// Location returns the parsed *time.Location for this household's timezone.
// Falls back to time.UTC if h is nil or the timezone string is invalid, so
// callers without a household can still format timestamps without a guard.
func (h *Household) Location() *time.Location {
	if h == nil {
		return time.UTC
	}
	loc, err := time.LoadLocation(h.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// RemoveMember removes a user from a household.
func (s *Store) RemoveMember(ctx context.Context, userID, householdID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM household_members WHERE user_id = $1 AND household_id = $2`,
		userID, householdID,
	)
	if err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("user is not a member of this household")
	}
	return nil
}

// MemberCount returns the number of members in a household.
func (s *Store) MemberCount(ctx context.Context, householdID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM household_members WHERE household_id = $1`,
		householdID,
	).Scan(&count)
	return count, err
}

// SetCallHistoryEnabled toggles call history for a household.
func (s *Store) SetCallHistoryEnabled(ctx context.Context, householdID string, enabled bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE households SET call_history_enabled = $1 WHERE id = $2`,
		enabled, householdID,
	)
	if err != nil {
		return fmt.Errorf("set call history: %w", err)
	}
	return nil
}

// SetDoNotDisturb toggles the household-wide do-not-disturb flag. When true,
// the server treats every paired line as silent regardless of its per-line
// silent_mode flag.
func (s *Store) SetDoNotDisturb(ctx context.Context, householdID string, enabled bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE households SET do_not_disturb = $1 WHERE id = $2`,
		enabled, householdID,
	)
	if err != nil {
		return fmt.Errorf("set do not disturb: %w", err)
	}
	return nil
}

// Delete removes a household and all its associated records (members, invites,
// links, lines, and devices) via CASCADE foreign keys.
func (s *Store) Delete(ctx context.Context, householdID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM households WHERE id = $1`, householdID)
	if err != nil {
		return fmt.Errorf("delete household: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("household not found")
	}
	return nil
}

// SetTimezone updates the IANA timezone for a household.
func (s *Store) SetTimezone(ctx context.Context, householdID, tz string) error {
	if _, err := time.LoadLocation(tz); err != nil {
		return fmt.Errorf("invalid timezone %q: %w", tz, err)
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE households SET timezone = $1 WHERE id = $2`,
		tz, householdID,
	)
	if err != nil {
		return fmt.Errorf("set timezone: %w", err)
	}
	return nil
}
