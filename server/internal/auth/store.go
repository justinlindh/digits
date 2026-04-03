package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// User represents a registered user account.
type User struct {
	ID          string
	Email       string
	Name        string
	GoogleID    *string
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

// NewStore opens a new Postgres connection and returns a Store.
func NewStore(databaseURL string) (*Store, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// NewStoreFromDB wraps an existing *sql.DB (used when sharing a connection from db.Open).
func NewStoreFromDB(db *sql.DB) *Store {
	return &Store{db: db}
}

// CreateUser inserts a new user record and returns it.
func (s *Store) CreateUser(email, name string, googleID *string) (*User, error) {
	u := &User{}
	err := s.db.QueryRow(
		`INSERT INTO users (email, name, google_id) VALUES ($1, $2, $3)
		 RETURNING id, email, name, google_id, created_at, last_login_at`,
		email, name, googleID,
	).Scan(&u.ID, &u.Email, &u.Name, &u.GoogleID, &u.CreatedAt, &u.LastLoginAt)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

// GetUserByEmail looks up a user by email address.
func (s *Store) GetUserByEmail(email string) (*User, error) {
	u := &User{}
	err := s.db.QueryRow(
		`SELECT id, email, name, google_id, created_at, last_login_at FROM users WHERE email = $1`,
		email,
	).Scan(&u.ID, &u.Email, &u.Name, &u.GoogleID, &u.CreatedAt, &u.LastLoginAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("user not found")
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// GetUserByGoogleID looks up a user by their Google OAuth subject ID.
func (s *Store) GetUserByGoogleID(googleID string) (*User, error) {
	u := &User{}
	err := s.db.QueryRow(
		`SELECT id, email, name, google_id, created_at, last_login_at FROM users WHERE google_id = $1`,
		googleID,
	).Scan(&u.ID, &u.Email, &u.Name, &u.GoogleID, &u.CreatedAt, &u.LastLoginAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("user not found")
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// GetUserByID looks up a user by their UUID.
func (s *Store) GetUserByID(id string) (*User, error) {
	u := &User{}
	err := s.db.QueryRow(
		`SELECT id, email, name, google_id, created_at, last_login_at FROM users WHERE id = $1`,
		id,
	).Scan(&u.ID, &u.Email, &u.Name, &u.GoogleID, &u.CreatedAt, &u.LastLoginAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("user not found")
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateLastLogin sets last_login_at to now for the given user.
func (s *Store) UpdateLastLogin(userID string) error {
	_, err := s.db.Exec(`UPDATE users SET last_login_at = NOW() WHERE id = $1`, userID)
	return err
}

// LinkGoogleID associates a Google OAuth subject ID with an existing user.
func (s *Store) LinkGoogleID(userID, googleID string) error {
	_, err := s.db.Exec(`UPDATE users SET google_id = $1 WHERE id = $2`, googleID, userID)
	return err
}

// CreateSession generates a random token, stores its SHA-256 hash, and returns
// the raw token (which must be given to the client) and the session record.
func (s *Store) CreateSession(userID string, ttl time.Duration) (string, *Session, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", nil, err
	}
	hash := hashToken(token)
	sess := &Session{}
	err = s.db.QueryRow(
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
func (s *Store) ValidateSession(token string) (*Session, error) {
	hash := hashToken(token)
	sess := &Session{}
	err := s.db.QueryRow(
		`SELECT id, user_id, expires_at, created_at FROM sessions
		 WHERE token_hash = $1 AND expires_at > NOW()`,
		hash,
	).Scan(&sess.ID, &sess.UserID, &sess.ExpiresAt, &sess.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("invalid or expired session")
	}
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// DeleteSession removes a session by its raw token (used for logout).
func (s *Store) DeleteSession(token string) error {
	hash := hashToken(token)
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = $1`, hash)
	return err
}

// RefreshSession extends the expiry of an active session.
func (s *Store) RefreshSession(token string, ttl time.Duration) error {
	hash := hashToken(token)
	_, err := s.db.Exec(
		`UPDATE sessions SET expires_at = $1 WHERE token_hash = $2 AND expires_at > NOW()`,
		time.Now().Add(ttl), hash,
	)
	return err
}

// CreateMagicLink generates a single-use login token for passwordless email auth.
// Returns the raw token to embed in the email link.
func (s *Store) CreateMagicLink(email string, ttl time.Duration) (string, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	hash := hashToken(token)
	_, err = s.db.Exec(
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
func (s *Store) ValidateMagicLink(token string) (string, error) {
	hash := hashToken(token)
	var email string
	err := s.db.QueryRow(
		`UPDATE magic_links SET used = TRUE
		 WHERE token_hash = $1 AND expires_at > NOW() AND used = FALSE
		 RETURNING email`,
		hash,
	).Scan(&email)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("invalid, expired, or already used magic link")
	}
	if err != nil {
		return "", err
	}
	return email, nil
}

// CountUsers returns the total number of user accounts.
func (s *Store) CountUsers() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count)
	return count, err
}

// CleanupExpired removes expired sessions and used/expired magic links.
func (s *Store) CleanupExpired() error {
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < NOW()`); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM magic_links WHERE expires_at < NOW() OR used = TRUE`)
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

// hashToken returns the hex-encoded SHA-256 hash of the token.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
