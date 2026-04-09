package household

import (
	"database/sql"
	"fmt"
	"time"
)

// Household represents a family/household group.
type Household struct {
	ID                 string
	Name               string
	CallHistoryEnabled bool
	Timezone           string
	CreatedAt          time.Time
}

// Member represents a user's membership in a household.
type Member struct {
	UserID      string
	HouseholdID string
	Role        string
	CreatedAt   time.Time
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
func (s *Store) Create(name, ownerUserID string) (*Household, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	h := &Household{}
	err = tx.QueryRow(
		`INSERT INTO households (name) VALUES ($1) RETURNING id, name, timezone, created_at`,
		name,
	).Scan(&h.ID, &h.Name, &h.Timezone, &h.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert household: %w", err)
	}

	_, err = tx.Exec(
		`INSERT INTO household_members (user_id, household_id, role) VALUES ($1, $2, 'admin')`,
		ownerUserID, h.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("insert owner member: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return h, nil
}

// GetByID retrieves a household by its UUID.
func (s *Store) GetByID(id string) (*Household, error) {
	h := &Household{}
	err := s.db.QueryRow(
		`SELECT id, name, call_history_enabled, timezone, created_at FROM households WHERE id = $1`,
		id,
	).Scan(&h.ID, &h.Name, &h.CallHistoryEnabled, &h.Timezone, &h.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("household not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get household: %w", err)
	}
	return h, nil
}

// GetForUser returns all households the given user belongs to.
func (s *Store) GetForUser(userID string) ([]*Household, error) {
	rows, err := s.db.Query(
		`SELECT h.id, h.name, h.call_history_enabled, h.timezone, h.created_at
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
		if err := rows.Scan(&h.ID, &h.Name, &h.CallHistoryEnabled, &h.Timezone, &h.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan household: %w", err)
		}
		households = append(households, h)
	}
	return households, rows.Err()
}

// GetRole returns the role of a user in a given household, or an error if not a member.
func (s *Store) GetRole(userID, householdID string) (string, error) {
	var role string
	err := s.db.QueryRow(
		`SELECT role FROM household_members WHERE user_id = $1 AND household_id = $2`,
		userID, householdID,
	).Scan(&role)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("user is not a member of this household")
	}
	if err != nil {
		return "", fmt.Errorf("get role: %w", err)
	}
	return role, nil
}

// AddMember adds a user to a household with the given role. If the user is already
// a member, their role is updated.
func (s *Store) AddMember(userID, householdID, role string) error {
	_, err := s.db.Exec(
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

// GetMembers returns all members of a household.
func (s *Store) GetMembers(householdID string) ([]Member, error) {
	rows, err := s.db.Query(
		`SELECT user_id, household_id, role FROM household_members WHERE household_id = $1`,
		householdID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var members []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.HouseholdID, &m.Role); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// CountHouseholds returns the total number of households.
func (s *Store) CountHouseholds() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM households`).Scan(&count)
	return count, err
}

// UpdateName changes the name of a household.
func (s *Store) UpdateName(householdID, name string) error {
	_, err := s.db.Exec(
		`UPDATE households SET name = $1 WHERE id = $2`,
		name, householdID,
	)
	if err != nil {
		return fmt.Errorf("update household name: %w", err)
	}
	return nil
}

// Location returns the parsed *time.Location for this household's timezone.
// Falls back to time.UTC if the timezone string is invalid.
func (h *Household) Location() *time.Location {
	loc, err := time.LoadLocation(h.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// SetCallHistoryEnabled toggles call history for a household.
func (s *Store) SetCallHistoryEnabled(householdID string, enabled bool) error {
	_, err := s.db.Exec(
		`UPDATE households SET call_history_enabled = $1 WHERE id = $2`,
		enabled, householdID,
	)
	if err != nil {
		return fmt.Errorf("set call history: %w", err)
	}
	return nil
}

// SetTimezone updates the IANA timezone for a household.
func (s *Store) SetTimezone(householdID, tz string) error {
	if _, err := time.LoadLocation(tz); err != nil {
		return fmt.Errorf("invalid timezone %q: %w", tz, err)
	}
	_, err := s.db.Exec(
		`UPDATE households SET timezone = $1 WHERE id = $2`,
		tz, householdID,
	)
	if err != nil {
		return fmt.Errorf("set timezone: %w", err)
	}
	return nil
}
