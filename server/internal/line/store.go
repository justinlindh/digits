// Package line manages phone lines: their numbers, per-line settings (DND,
// voice style, voicemail, auto-update), and authorization. Store provides
// the database queries; Authorizer enforces per-request access control;
// Settings holds the JSON-encoded per-line configuration.
package line

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/dbutil"
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

// scanLine reads a single row from the lines table using lineColumns and
// returns the materialized Line. sql.ErrNoRows is returned unwrapped so
// callers can map it to ErrNotFound with errors.Is.
func scanLine(s dbutil.RowScanner) (Line, error) {
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

// Add inserts a new line for the given household and returns it. The initial
// settings column is seeded with DefaultSettings() so the DB round-trip
// preserves defaults (notably voicemail.enabled) that have non-zero values.
func (s *Store) Add(ctx context.Context, number, name, householdID string) (*Line, error) {
	defaults, err := json.Marshal(DefaultSettings().Normalize())
	if err != nil {
		return nil, fmt.Errorf("marshal default settings: %w", err)
	}
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO lines (number, name, household_id, settings)
		 VALUES ($1, $2, $3, $4)
		 RETURNING `+lineColumns,
		number, name, householdID, defaults,
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

// EffectiveSettingsByNumber returns the line's settings for the given number,
// with SilentMode already folded together with any active scheduled
// quiet-hours window. Quiet hours are evaluated in the owning household's
// timezone at the current instant, so the value a caller receives is what the
// device should treat as authoritative right now: if the window is open, the
// returned SilentMode is true even when the explicit toggle is off. Used by
// the signaling layer to push settings on device registration and by the
// quiet-hours scheduler on window transitions.
func (s *Store) EffectiveSettingsByNumber(ctx context.Context, number string) (Settings, error) {
	var (
		settingsRaw []byte
		timezone    string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT l.settings, h.timezone
		   FROM lines l
		   JOIN households h ON h.id = l.household_id
		  WHERE l.number = $1`,
		number,
	).Scan(&settingsRaw, &timezone)
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{}, ErrNotFound
	}
	if err != nil {
		return Settings{}, fmt.Errorf("effective settings by number %s: %w", number, err)
	}
	settings, err := scanSettings(settingsRaw)
	if err != nil {
		return Settings{}, err
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	settings.SilentMode = settings.SilentNow(time.Now().In(loc))
	return settings, nil
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

// SetAllSilentByHousehold batch-updates silent_mode in the JSONB settings
// column for every line in the household. Other settings fields are preserved.
func (s *Store) SetAllSilentByHousehold(ctx context.Context, householdID string, silent bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE lines
		 SET settings = jsonb_set(COALESCE(settings, '{}'), '{silent_mode}', $1::jsonb),
		     updated_at = NOW()
		 WHERE household_id = $2`,
		strconv.FormatBool(silent), householdID,
	)
	if err != nil {
		return fmt.Errorf("set all silent by household: %w", err)
	}
	return nil
}

// AllSilentByHousehold returns true when the household has at least one line
// and every line has silent_mode set to true. Returns false for households
// with no lines.
func (s *Store) AllSilentByHousehold(ctx context.Context, householdID string) (bool, error) {
	var total, silentCount int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*),
		        COUNT(*) FILTER (WHERE COALESCE(settings->>'silent_mode', 'false') = 'true')
		 FROM lines WHERE household_id = $1`,
		householdID,
	).Scan(&total, &silentCount)
	if err != nil {
		return false, fmt.Errorf("all silent by household: %w", err)
	}
	return total > 0 && total == silentCount, nil
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
