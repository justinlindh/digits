package line

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/justinlindh/digits/server/internal/db"
)

// ErrNotFound is returned when a line cannot be found.
var ErrNotFound = errors.New("line not found")

// ErrNumberTaken is returned when a line number is already in use.
var ErrNumberTaken = errors.New("line number is already in use")

var numberRegex = regexp.MustCompile(`^\d{3}-?\d{4}$`)

// ValidateNumber checks that num is exactly 7 digits, optionally formatted as NNN-NNNN.
func ValidateNumber(num string) error {
	if !numberRegex.MatchString(num) {
		return fmt.Errorf("phone number must be exactly 7 digits, got %q", num)
	}
	return nil
}

// StripNumber removes any hyphens from a phone number string.
func StripNumber(num string) string {
	return strings.ReplaceAll(num, "-", "")
}

// FormatNumber inserts a hyphen after the 3rd digit of a 7-digit number.
// Non-7-digit inputs are returned unchanged.
func FormatNumber(num string) string {
	if len(num) != 7 {
		return num
	}
	return num[:3] + "-" + num[3:]
}

// Line represents a phone number belonging to a household.
type Line struct {
	ID          int64
	Number      string
	Name        string
	HouseholdID string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Store provides CRUD operations for lines backed by Postgres.
type Store struct {
	db *sql.DB
}

// NewStore wraps a *db.Database.
func NewStore(database *db.Database) *Store {
	return &Store{db: database.DB}
}

// Add inserts a new line for the given household and returns it.
func (s *Store) Add(number, name, householdID string) (*Line, error) {
	l := &Line{}
	err := s.db.QueryRow(
		`INSERT INTO lines (number, name, household_id)
		 VALUES ($1, $2, $3)
		 RETURNING id, number, name, household_id, created_at, updated_at`,
		number, name, householdID,
	).Scan(&l.ID, &l.Number, &l.Name, &l.HouseholdID, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("add line: %w", err)
	}
	return l, nil
}

// GetByID retrieves a line by its integer ID.
func (s *Store) GetByID(id int64) (*Line, error) {
	l := &Line{}
	err := s.db.QueryRow(
		`SELECT id, number, name, household_id, created_at, updated_at
		 FROM lines WHERE id = $1`,
		id,
	).Scan(&l.ID, &l.Number, &l.Name, &l.HouseholdID, &l.CreatedAt, &l.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get line by id %d: %w", id, err)
	}
	return l, nil
}

// GetByNumber retrieves a line by its phone number.
func (s *Store) GetByNumber(number string) (*Line, error) {
	l := &Line{}
	err := s.db.QueryRow(
		`SELECT id, number, name, household_id, created_at, updated_at
		 FROM lines WHERE number = $1`,
		number,
	).Scan(&l.ID, &l.Number, &l.Name, &l.HouseholdID, &l.CreatedAt, &l.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get line by number %s: %w", number, err)
	}
	return l, nil
}

// List returns all lines ordered by number.
func (s *Store) List() ([]Line, error) {
	rows, err := s.db.Query(
		`SELECT id, number, name, household_id, created_at, updated_at
		 FROM lines ORDER BY number`,
	)
	if err != nil {
		return nil, fmt.Errorf("list lines: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var lines []Line
	for rows.Next() {
		var l Line
		if err := rows.Scan(&l.ID, &l.Number, &l.Name, &l.HouseholdID, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan line: %w", err)
		}
		lines = append(lines, l)
	}
	return lines, rows.Err()
}

// ListByHousehold returns all lines belonging to the given household, ordered by number.
func (s *Store) ListByHousehold(householdID string) ([]Line, error) {
	rows, err := s.db.Query(
		`SELECT id, number, name, household_id, created_at, updated_at
		 FROM lines WHERE household_id = $1 ORDER BY number`,
		householdID,
	)
	if err != nil {
		return nil, fmt.Errorf("list lines by household: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var lines []Line
	for rows.Next() {
		var l Line
		if err := rows.Scan(&l.ID, &l.Number, &l.Name, &l.HouseholdID, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan line: %w", err)
		}
		lines = append(lines, l)
	}
	return lines, rows.Err()
}

// ListByHouseholds returns all lines for the given household IDs, grouped by household.
func (s *Store) ListByHouseholds(householdIDs []string) (map[string][]Line, error) {
	if len(householdIDs) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT id, number, name, household_id, created_at, updated_at
		 FROM lines WHERE household_id = ANY($1) ORDER BY number`,
		pq.Array(householdIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("list lines by households: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string][]Line)
	for rows.Next() {
		var l Line
		if err := rows.Scan(&l.ID, &l.Number, &l.Name, &l.HouseholdID, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan line: %w", err)
		}
		result[l.HouseholdID] = append(result[l.HouseholdID], l)
	}
	return result, rows.Err()
}

// Update modifies the number and name of the line with the given ID.
func (s *Store) Update(id int64, number, name string) error {
	res, err := s.db.Exec(
		`UPDATE lines SET number = $1, name = $2, updated_at = NOW() WHERE id = $3`,
		number, name, id,
	)
	if err != nil {
		return fmt.Errorf("update line: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("line %d not found", id)
	}
	return nil
}

// Delete removes the line with the given ID.
func (s *Store) Delete(id int64) error {
	res, err := s.db.Exec(`DELETE FROM lines WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete line: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("line %d not found", id)
	}
	return nil
}

// NumberExists reports whether the given number is already in use.
func (s *Store) NumberExists(number string) (bool, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM lines WHERE number = $1`, number,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("number exists: %w", err)
	}
	return count > 0, nil
}

// NumberExistsExcluding reports whether the given number is in use by any line
// other than the one with excludeID. Used when validating edits.
func (s *Store) NumberExistsExcluding(number string, excludeID int64) (bool, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM lines WHERE number = $1 AND id != $2`,
		number, excludeID,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("number exists excluding: %w", err)
	}
	return count > 0, nil
}

// GetHouseholdIDByNumber returns the household UUID for the given phone number.
func (s *Store) GetHouseholdIDByNumber(number string) (string, error) {
	var householdID string
	err := s.db.QueryRow(
		`SELECT household_id FROM lines WHERE number = $1`, number,
	).Scan(&householdID)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get household id by number: %w", err)
	}
	return householdID, nil
}
