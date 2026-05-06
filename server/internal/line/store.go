package line

import (
	"context"
	"database/sql"
	"encoding/json"
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

var numberRegex = regexp.MustCompile(`^\d{3}-?\d{4}$`)

// scanSettings parses the settings JSONB value returned by Postgres into a
// Settings struct, layering the result on top of DefaultSettings so any
// field not present in the DB falls through to its default.
func scanSettings(raw []byte) (Settings, error) {
	patch := Settings{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &patch); err != nil {
			return Settings{}, fmt.Errorf("settings: %w", err)
		}
	}
	return DefaultSettings().Merge(patch).Normalize(), nil
}

const lineColumns = `id, number, name, household_id, settings, created_at, updated_at`

// rowScanner abstracts *sql.Row and *sql.Rows so the same field list and
// settings decode can serve both single-row and multi-row queries.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanLine reads a single row from the lines table using lineColumns and
// returns the materialized Line. sql.ErrNoRows is returned unwrapped so
// callers can map it to ErrNotFound with errors.Is.
func scanLine(s rowScanner) (Line, error) {
	var l Line
	var settingsRaw []byte
	if err := s.Scan(&l.ID, &l.Number, &l.Name, &l.HouseholdID, &settingsRaw, &l.CreatedAt, &l.UpdatedAt); err != nil {
		return Line{}, err
	}
	settings, err := scanSettings(settingsRaw)
	if err != nil {
		return Line{}, err
	}
	l.Settings = settings
	return l, nil
}

// scanLineRow is the multi-row variant used by List* iterators.
// sql.Rows.Scan never reports ErrNoRows, so wrapping unconditionally is safe.
func scanLineRow(rows *sql.Rows) (Line, error) {
	l, err := scanLine(rows)
	if err != nil {
		return Line{}, fmt.Errorf("scan line: %w", err)
	}
	return l, nil
}

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
	Settings    Settings
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
func (s *Store) Add(ctx context.Context, number, name, householdID string) (*Line, error) {
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO lines (number, name, household_id)
		 VALUES ($1, $2, $3)
		 RETURNING `+lineColumns,
		number, name, householdID,
	)
	l, err := scanLine(row)
	if err != nil {
		return nil, fmt.Errorf("add line: %w", err)
	}
	return &l, nil
}

// GetByID retrieves a line by its integer ID.
func (s *Store) GetByID(ctx context.Context, id int64) (*Line, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+lineColumns+` FROM lines WHERE id = $1`,
		id,
	)
	l, err := scanLine(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get line by id %d: %w", id, err)
	}
	return &l, nil
}

// GetByNumber retrieves a line by its phone number.
func (s *Store) GetByNumber(ctx context.Context, number string) (*Line, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+lineColumns+` FROM lines WHERE number = $1`,
		number,
	)
	l, err := scanLine(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get line by number %s: %w", number, err)
	}
	return &l, nil
}

// EffectiveSettingsByNumber returns the line's settings plus the household's
// do_not_disturb flag in a single query. Used by the signaling layer to push
// effective silent state without two round-trips.
func (s *Store) EffectiveSettingsByNumber(ctx context.Context, number string) (Settings, bool, error) {
	var settingsRaw []byte
	var householdDND bool
	err := s.db.QueryRowContext(ctx,
		`SELECT l.settings, h.do_not_disturb
		 FROM lines l
		 JOIN households h ON h.id = l.household_id
		 WHERE l.number = $1`,
		number,
	).Scan(&settingsRaw, &householdDND)
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{}, false, ErrNotFound
	}
	if err != nil {
		return Settings{}, false, fmt.Errorf("effective settings by number %s: %w", number, err)
	}
	settings, err := scanSettings(settingsRaw)
	if err != nil {
		return Settings{}, false, err
	}
	return settings, householdDND, nil
}

// List returns all lines ordered by number.
func (s *Store) List(ctx context.Context) ([]Line, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+lineColumns+` FROM lines ORDER BY number`,
	)
	if err != nil {
		return nil, fmt.Errorf("list lines: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var lines []Line
	for rows.Next() {
		l, err := scanLineRow(rows)
		if err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}
	return lines, rows.Err()
}

// ListByHousehold returns all lines belonging to the given household, ordered by number.
func (s *Store) ListByHousehold(ctx context.Context, householdID string) ([]Line, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+lineColumns+` FROM lines WHERE household_id = $1 ORDER BY number`,
		householdID,
	)
	if err != nil {
		return nil, fmt.Errorf("list lines by household: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var lines []Line
	for rows.Next() {
		l, err := scanLineRow(rows)
		if err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}
	return lines, rows.Err()
}

// ListByHouseholds returns all lines for the given household IDs, grouped by
// household ID and ordered by number within each group.
func (s *Store) ListByHouseholds(ctx context.Context, householdIDs []string) (map[string][]Line, error) {
	if len(householdIDs) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+lineColumns+` FROM lines WHERE household_id = ANY($1) ORDER BY number`,
		pq.Array(householdIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("list lines by households: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string][]Line, len(householdIDs))
	for rows.Next() {
		l, err := scanLineRow(rows)
		if err != nil {
			return nil, err
		}
		result[l.HouseholdID] = append(result[l.HouseholdID], l)
	}
	return result, rows.Err()
}

// Update modifies the number and name of the line with the given ID.
func (s *Store) Update(ctx context.Context, id int64, number, name string) error {
	res, err := s.db.ExecContext(ctx,
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
func (s *Store) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM lines WHERE id = $1`, id)
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
func (s *Store) NumberExists(ctx context.Context, number string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM lines WHERE number = $1`, number,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("number exists: %w", err)
	}
	return count > 0, nil
}

// NumberExistsExcluding reports whether the given number is in use by any line
// other than the one with excludeID. Used when validating edits.
func (s *Store) NumberExistsExcluding(ctx context.Context, number string, excludeID int64) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM lines WHERE number = $1 AND id != $2`,
		number, excludeID,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("number exists excluding: %w", err)
	}
	return count > 0, nil
}

// UpdateSettings replaces the settings JSONB for the line with the given ID.
func (s *Store) UpdateSettings(ctx context.Context, id int64, settings Settings) error {
	raw, err := json.Marshal(settings.Normalize())
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE lines SET settings = $1, updated_at = NOW() WHERE id = $2`,
		raw, id,
	)
	if err != nil {
		return fmt.Errorf("update settings: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("line %d not found", id)
	}
	return nil
}

// GetHouseholdIDByNumber returns the household UUID for the given phone number.
func (s *Store) GetHouseholdIDByNumber(ctx context.Context, number string) (string, error) {
	var householdID string
	err := s.db.QueryRowContext(ctx,
		`SELECT household_id FROM lines WHERE number = $1`, number,
	).Scan(&householdID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get household id by number: %w", err)
	}
	return householdID, nil
}
