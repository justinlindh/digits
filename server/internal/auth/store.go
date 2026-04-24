package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/justinlindh/digits/server/internal/device"
)

// ErrUserNotFound is returned by GetUserBy* when no matching row exists.
// Callers that want find-or-create semantics must check for this explicitly
// so that real DB errors (connection loss, context cancellation, etc.) are
// not silently treated as "user missing".
var ErrUserNotFound = errors.New("user not found")

// User represents a registered user account.
type User struct {
	ID          string
	Email       string
	Name        string
	GoogleID    *string
	Theme       Theme
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

// CreateUser inserts a new user record and returns it.
func (s *Store) CreateUser(ctx context.Context, email, name string, googleID *string) (*User, error) {
	u := &User{}
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO users (email, name, google_id) VALUES ($1, $2, $3)
		 RETURNING id, email, name, google_id, theme, crt_mode, appearance, created_at, last_login_at`,
		email, name, googleID,
	).Scan(&u.ID, &u.Email, &u.Name, &u.GoogleID, &u.Theme, &u.CRTMode, &u.Appearance, &u.CreatedAt, &u.LastLoginAt)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

const userSelectBase = `SELECT id, email, name, google_id, theme, crt_mode, appearance, created_at, last_login_at FROM users WHERE `

func (s *Store) queryUser(ctx context.Context, whereClause string, arg any) (*User, error) {
	u := &User{}
	err := s.db.QueryRowContext(ctx, userSelectBase+whereClause, arg).Scan(
		&u.ID, &u.Email, &u.Name, &u.GoogleID, &u.Theme, &u.CRTMode, &u.Appearance, &u.CreatedAt, &u.LastLoginAt,
	)
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

// CreateSession generates a random token, stores its SHA-256 hash, and returns
// the raw token (which must be given to the client) and the session record.
func (s *Store) CreateSession(ctx context.Context, userID string, ttl time.Duration) (string, *Session, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", nil, err
	}
	hash := device.HashToken(token)
	sess := &Session{}
	err = s.db.QueryRowContext(ctx,
		`INSERT INTO sessions (user_id, token_hash, expires_at)
		 VALUES ($1, $2, $3)
		 RETURNING id, user_id, expires_at, created_at`,
		userID, hash, time.Now().Add(ttl),
	).Scan(&sess.ID, &sess.UserID, &sess.ExpiresAt, &sess.CreatedAt)
	if err != nil {
		return "", nil, fmt.Errorf("create session: %w", err)
	}
	return token, sess, nil
}

// ValidateSession looks up a session by its raw token and checks it hasn't expired.
func (s *Store) ValidateSession(ctx context.Context, token string) (*Session, error) {
	hash := device.HashToken(token)
	sess := &Session{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, expires_at, created_at FROM sessions
		 WHERE token_hash = $1 AND expires_at > NOW()`,
		hash,
	).Scan(&sess.ID, &sess.UserID, &sess.ExpiresAt, &sess.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("invalid or expired session")
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

// RefreshSession extends the expiry of an active session.
func (s *Store) RefreshSession(ctx context.Context, token string, ttl time.Duration) error {
	hash := device.HashToken(token)
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET expires_at = $1 WHERE token_hash = $2 AND expires_at > NOW()`,
		time.Now().Add(ttl), hash,
	)
	return err
}

// CreateMagicLink generates a single-use login token for passwordless email auth.
// Returns the raw token to embed in the email link.
func (s *Store) CreateMagicLink(ctx context.Context, email string, ttl time.Duration) (string, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	hash := device.HashToken(token)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO magic_links (email, token_hash, expires_at) VALUES ($1, $2, $3)`,
		email, hash, time.Now().Add(ttl),
	)
	if err != nil {
		return "", fmt.Errorf("create magic link: %w", err)
	}
	return token, nil
}

// ValidateMagicLink checks and atomically consumes a magic link token.
// Returns the associated email on success. Returns an error if the token
// is invalid, expired, or has already been used.
func (s *Store) ValidateMagicLink(ctx context.Context, token string) (string, error) {
	hash := device.HashToken(token)
	var email string
	err := s.db.QueryRowContext(ctx,
		`UPDATE magic_links SET used = TRUE
		 WHERE token_hash = $1 AND expires_at > NOW() AND used = FALSE
		 RETURNING email`,
		hash,
	).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("invalid, expired, or already used magic link")
	}
	if err != nil {
		return "", err
	}
	return email, nil
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

// randomToken generates a cryptographically random hex string of the given byte length.
func randomToken(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
