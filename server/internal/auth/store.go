// Package auth manages user accounts, browser sessions, and passwordless
// authentication flows (magic link and Google OAuth). It owns the users,
// sessions, and magic_links tables and exposes typed sentinel errors
// (ErrUserNotFound) so callers can distinguish missing-record results from
// real database errors.
package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/justinlindh/digits/server/internal/dbutil"
	"github.com/justinlindh/digits/server/internal/device"
)

// ErrUserNotFound is returned by GetUserBy* when no matching row exists.
// Callers that want find-or-create semantics must check for this explicitly
// so that real DB errors (connection loss, context cancellation, etc.) are
// not silently treated as "user missing".
//
// ErrInvalidSession and ErrInvalidMagicLink are returned by ValidateSession,
// ValidateAndRefreshSession, and ValidateMagicLink when the token does not
// match a valid, unexpired, unused record. They are distinct from nil so
// callers can use errors.Is to distinguish "bad token" from a real DB error.
var (
	ErrUserNotFound     = errors.New("user not found")
	ErrInvalidSession   = errors.New("invalid or expired session")
	ErrInvalidMagicLink = errors.New("invalid, expired, or already used magic link")
)

// User represents a registered user account.
type User struct {
	ID          string
	Email       string
	Name        string
	GoogleID    *string
	Theme       Theme
	ThemeChosen bool
	CRTMode     CRTMode
	Appearance  Appearance
	CreatedAt   time.Time
	LastLoginAt *time.Time
}

// Session represents an authenticated browser session.
type Session struct {
	ID        string
	UserID    string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// Store provides user and session persistence backed by Postgres.
type Store struct {
	db           *sql.DB
	CookieDomain string
}

// NewStore wraps an existing *sql.DB (shared with the rest of signald via db.Open).
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// userColumns lists every users-table column scanned into User. Kept in
// one place so adding or renaming a column does not require chasing three
// SELECT/RETURNING lists and three Scan argument lists.
const userColumns = `id, email, name, google_id, theme, theme_chosen, crt_mode, appearance, created_at, last_login_at`

// scanUser materializes a User from any row whose columns match userColumns
// in order.
func scanUser(row dbutil.RowScanner) (*User, error) {
	u := &User{}
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.GoogleID, &u.Theme, &u.ThemeChosen, &u.CRTMode, &u.Appearance, &u.CreatedAt, &u.LastLoginAt); err != nil {
		return nil, err
	}
	return u, nil
}

// CreateUser inserts a new user record and returns it.
func (s *Store) CreateUser(ctx context.Context, email, name string, googleID *string) (*User, error) {
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO users (email, name, google_id) VALUES ($1, $2, $3) RETURNING `+userColumns,
		email, name, googleID,
	)
	u, err := scanUser(row)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

func (s *Store) queryUser(ctx context.Context, whereClause string, arg any) (*User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE `+whereClause, arg)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// GetUserByEmail looks up a user by email address.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	return s.queryUser(ctx, `email = $1`, email)
}

// GetOrCreateUserByEmail returns the user for email, creating one (with an empty
// name and no Google ID) if none exists. The bool reports whether a new user was
// created. A lookup failure other than ErrUserNotFound is returned unchanged so
// callers can tell a transient DB error apart from a missing user and avoid
// silently creating a duplicate account.
func (s *Store) GetOrCreateUserByEmail(ctx context.Context, email string) (*User, bool, error) {
	user, err := s.GetUserByEmail(ctx, email)
	switch {
	case err == nil:
		return user, false, nil
	case errors.Is(err, ErrUserNotFound):
		user, err = s.CreateUser(ctx, email, "", nil)
		if err != nil {
			return nil, false, err
		}
		return user, true, nil
	default:
		return nil, false, err
	}
}

// GetUserByGoogleID looks up a user by their Google OAuth subject ID.
func (s *Store) GetUserByGoogleID(ctx context.Context, googleID string) (*User, error) {
	return s.queryUser(ctx, `google_id = $1`, googleID)
}

// GetUserByID looks up a user by their UUID.
func (s *Store) GetUserByID(ctx context.Context, id string) (*User, error) {
	return s.queryUser(ctx, `id = $1`, id)
}

// UpdateLastLogin sets last_login_at to now for the given user.
func (s *Store) UpdateLastLogin(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET last_login_at = NOW() WHERE id = $1`, userID)
	return err
}

// LinkGoogleID associates a Google OAuth subject ID with an existing user.
func (s *Store) LinkGoogleID(ctx context.Context, userID, googleID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET google_id = $1 WHERE id = $2`, googleID, userID)
	return err
}

// SetTheme updates the user's selected webapp theme.
func (s *Store) SetTheme(ctx context.Context, userID string, theme Theme) error {
	if !theme.Valid() {
		return fmt.Errorf("invalid theme: %q", theme)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE users SET theme = $1 WHERE id = $2`, theme, userID)
	return err
}

// MarkThemeChosen marks the welcome theme picker as completed. One-shot:
// once true, the welcome gate releases and later /settings/theme changes
// don't toggle it back.
func (s *Store) MarkThemeChosen(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET theme_chosen = TRUE WHERE id = $1`, userID)
	return err
}

// SetThemeAndMarkChosen flips both theme and theme_chosen in one UPDATE and
// returns the refreshed user. Used by the welcome handler so a partial write
// can't leave a user with their picked theme but theme_chosen=false (which
// would loop them back to /welcome until the next submit).
func (s *Store) SetThemeAndMarkChosen(ctx context.Context, userID string, theme Theme) (*User, error) {
	if !theme.Valid() {
		return nil, fmt.Errorf("invalid theme: %q", theme)
	}
	row := s.db.QueryRowContext(ctx,
		`UPDATE users SET theme = $1, theme_chosen = TRUE WHERE id = $2 RETURNING `+userColumns,
		theme, userID,
	)
	u, err := scanUser(row)
	if err != nil {
		return nil, fmt.Errorf("set theme and mark chosen: %w", err)
	}
	return u, nil
}

// SetCRTMode updates the user's selected CRT bezel mode.
func (s *Store) SetCRTMode(ctx context.Context, userID string, mode CRTMode) error {
	if !mode.Valid() {
		return fmt.Errorf("invalid crt mode: %q", mode)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE users SET crt_mode = $1 WHERE id = $2`, mode, userID)
	return err
}

// SetAppearance updates the user's selected intercom appearance (day/night).
func (s *Store) SetAppearance(ctx context.Context, userID string, appearance Appearance) error {
	if !appearance.Valid() {
		return fmt.Errorf("invalid appearance: %q", appearance)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE users SET appearance = $1 WHERE id = $2`, appearance, userID)
	return err
}

// sessionColumns lists every sessions-table column scanned into Session.
const sessionColumns = `id, user_id, expires_at, created_at`

// scanSession materializes a Session from any row whose columns match
// sessionColumns in order.
func scanSession(row dbutil.RowScanner) (*Session, error) {
	sess := &Session{}
	if err := row.Scan(&sess.ID, &sess.UserID, &sess.ExpiresAt, &sess.CreatedAt); err != nil {
		return nil, err
	}
	return sess, nil
}

// CreateSession generates a random token, stores its SHA-256 hash, and returns
// the raw token (which must be given to the client) and the session record.
func (s *Store) CreateSession(ctx context.Context, userID string, ttl time.Duration) (string, *Session, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", nil, err
	}
	hash := device.HashToken(token)
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO sessions (user_id, token_hash, expires_at)
		 VALUES ($1, $2, $3)
		 RETURNING `+sessionColumns,
		userID, hash, time.Now().Add(ttl),
	)
	sess, err := scanSession(row)
	if err != nil {
		return "", nil, fmt.Errorf("create session: %w", err)
	}
	return token, sess, nil
}

// ValidateSession looks up a session by its raw token and checks it hasn't expired.
func (s *Store) ValidateSession(ctx context.Context, token string) (*Session, error) {
	hash := device.HashToken(token)
	row := s.db.QueryRowContext(ctx,
		`SELECT `+sessionColumns+` FROM sessions
		 WHERE token_hash = $1 AND expires_at > NOW()`,
		hash,
	)
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidSession
	}
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// DeleteSession removes a session by its raw token (used for logout).
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	hash := device.HashToken(token)
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hash)
	return err
}

// DeleteUser deletes the user row. FK CASCADE handles sessions and household_members.
// The v28 migration SET NULL handles household_links, household_invites, calls, and conference_kicks.
func (s *Store) DeleteUser(ctx context.Context, userID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// ValidateAndRefreshSession atomically validates a session and extends its
// expiry in a single UPDATE ... RETURNING query, eliminating the TOCTOU window
// between a separate validate-then-refresh pair.
func (s *Store) ValidateAndRefreshSession(ctx context.Context, token string, ttl time.Duration) (*Session, error) {
	hash := device.HashToken(token)
	row := s.db.QueryRowContext(ctx,
		`UPDATE sessions SET expires_at = $1
		 WHERE token_hash = $2 AND expires_at > NOW()
		 RETURNING `+sessionColumns,
		time.Now().Add(ttl), hash,
	)
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidSession
	}
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// CreateMagicLink generates a single-use login token for passwordless email auth.
// Returns the raw token to embed in the email link. returnTo is an optional
// path to redirect to after authentication; pass "" to use the default.
func (s *Store) CreateMagicLink(ctx context.Context, email string, ttl time.Duration, returnTo string) (string, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	hash := device.HashToken(token)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO magic_links (email, token_hash, expires_at, return_to) VALUES ($1, $2, $3, $4)`,
		email, hash, time.Now().Add(ttl), sql.NullString{String: returnTo, Valid: returnTo != ""},
	)
	if err != nil {
		return "", fmt.Errorf("create magic link: %w", err)
	}
	return token, nil
}

// ValidateMagicLink checks and atomically consumes a magic link token.
// Returns the associated email and optional returnTo path on success.
// Returns an error if the token is invalid, expired, or has already been used.
func (s *Store) ValidateMagicLink(ctx context.Context, token string) (string, string, error) {
	hash := device.HashToken(token)
	var email string
	var returnTo sql.NullString
	err := s.db.QueryRowContext(ctx,
		`UPDATE magic_links SET used = TRUE
		 WHERE token_hash = $1 AND expires_at > NOW() AND used = FALSE
		 RETURNING email, return_to`,
		hash,
	).Scan(&email, &returnTo)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrInvalidMagicLink
	}
	if err != nil {
		return "", "", err
	}
	return email, returnTo.String, nil
}

// CountUsers returns the total number of user accounts.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	return count, err
}

// CleanupExpired removes expired sessions and used/expired magic links.
func (s *Store) CleanupExpired(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < NOW()`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM magic_links WHERE expires_at < NOW() OR used = TRUE`)
	return err
}

// SetActiveHousehold updates the active_household_id on the user's current session.
func (s *Store) SetActiveHousehold(ctx context.Context, sessionToken string, householdID string) error {
	hash := device.HashToken(sessionToken)
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET active_household_id = $1 WHERE token_hash = $2 AND expires_at > NOW()`,
		householdID, hash,
	)
	if err != nil {
		return fmt.Errorf("set active household: %w", err)
	}
	return nil
}

// ActiveHouseholdID returns the active_household_id from the current session,
// or empty string if not set.
func (s *Store) ActiveHouseholdID(ctx context.Context, sessionToken string) (string, error) {
	hash := device.HashToken(sessionToken)
	var id sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT active_household_id FROM sessions WHERE token_hash = $1 AND expires_at > NOW()`,
		hash,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return id.String, nil
}

// randomToken generates a cryptographically random hex string of the given byte length.
func randomToken(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
