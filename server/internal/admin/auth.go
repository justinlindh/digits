package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/justinlindh/digits/server/internal/device"
	"golang.org/x/crypto/bcrypt"
)

type AuthStore struct {
	db *AdminDB
}

func NewAuthStore(db *AdminDB) *AuthStore {
	return &AuthStore{db: db}
}

func HashSecret(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func VerifySecret(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

func (s *AuthStore) CreateAdmin(ctx context.Context, username, secretHash string) (string, error) {
	var id string
	err := s.db.DB.QueryRowContext(ctx,
		"INSERT INTO admin_users (username, secret_hash) VALUES ($1, $2) RETURNING id",
		username, secretHash,
	).Scan(&id)
	return id, err
}

func (s *AuthStore) VerifyLogin(ctx context.Context, username, password string) (string, error) {
	var id, hash string
	err := s.db.DB.QueryRowContext(ctx,
		"SELECT id, secret_hash FROM admin_users WHERE username = $1",
		username,
	).Scan(&id, &hash)
	if err != nil {
		return "", errors.New("invalid credentials")
	}
	if err := VerifySecret(hash, password); err != nil {
		return "", errors.New("invalid credentials")
	}
	return id, nil
}

func (s *AuthStore) CreateSession(ctx context.Context, adminID string) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	tokenHash := device.HashToken(token)
	expires := time.Now().Add(24 * time.Hour)

	_, err = s.db.DB.ExecContext(ctx,
		"INSERT INTO admin_sessions (admin_id, token_hash, expires_at) VALUES ($1, $2, $3)",
		adminID, tokenHash, expires,
	)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (s *AuthStore) ValidateSession(ctx context.Context, token string) (string, error) {
	tokenHash := device.HashToken(token)
	var adminID string
	err := s.db.DB.QueryRowContext(ctx,
		"SELECT admin_id FROM admin_sessions WHERE token_hash = $1 AND expires_at > NOW()",
		tokenHash,
	).Scan(&adminID)
	if err != nil {
		return "", errors.New("invalid or expired session")
	}
	return adminID, nil
}

func (s *AuthStore) CleanupExpired(ctx context.Context) error {
	_, err := s.db.DB.ExecContext(ctx, "DELETE FROM admin_sessions WHERE expires_at < NOW()")
	return err
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
