package household

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/justinlindh/digits/server/internal/dbutil"
)

const inviteTTL = 7 * 24 * time.Hour

// InviteStatusPending is the only invite status compared in Go; the SQL in
// this file also writes 'accepted' and 'cancelled', matching the DB CHECK
// constraint defined in db.go.
const InviteStatusPending = "pending"

var ErrInviteNotFound = errors.New("invite not found")

// ErrInviteExpiredOrUsed is returned by AcceptInvite when the token doesn't match a live pending invite.
var ErrInviteExpiredOrUsed = errors.New("invite not found, expired, or already used")

var ErrInviteNotPending = errors.New("invite not found or not pending")

// HouseholdInvite represents an email invitation to join a household. The
// one-time Token is emailed to the recipient and redeemed at accept time.
type HouseholdInvite struct {
	ID          string
	HouseholdID string
	Email       string
	InvitedBy   string
	Token       string
	Status      string // pending, accepted, cancelled
	CreatedAt   time.Time
	AcceptedAt  *time.Time
	ExpiresAt   time.Time
}

// InviteStore persists household invites and enforces one-time token
// redemption with TTL expiry.
type InviteStore struct {
	db *sql.DB
}

// NewInviteStore returns an InviteStore backed by db.
func NewInviteStore(db *sql.DB) *InviteStore {
	return &InviteStore{db: db}
}

func generateInviteToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate invite token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

const inviteColumns = `id, household_id, email, invited_by, token, status, created_at, accepted_at, expires_at`

func scanInvite(row dbutil.RowScanner) (*HouseholdInvite, error) {
	inv := &HouseholdInvite{}
	err := row.Scan(&inv.ID, &inv.HouseholdID, &inv.Email, &inv.InvitedBy,
		&inv.Token, &inv.Status, &inv.CreatedAt, &inv.AcceptedAt, &inv.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return inv, nil
}

// CreateInvite issues a pending invite for email to join householdID and
// returns it with a freshly generated token and expiry.
func (s *InviteStore) CreateInvite(ctx context.Context, householdID, email, invitedByUserID string) (*HouseholdInvite, error) {
	token, err := generateInviteToken()
	if err != nil {
		return nil, err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	inv, err := scanInvite(s.db.QueryRowContext(ctx, `
		INSERT INTO household_invites (household_id, email, invited_by, token, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+inviteColumns,
		householdID, email, invitedByUserID, token, time.Now().Add(inviteTTL),
	))
	if err != nil {
		return nil, fmt.Errorf("create invite: %w", err)
	}
	return inv, nil
}

// GetByID looks up an invite by its ID.
func (s *InviteStore) GetByID(ctx context.Context, id string) (*HouseholdInvite, error) {
	inv, err := scanInvite(s.db.QueryRowContext(ctx, `
		SELECT `+inviteColumns+` FROM household_invites WHERE id = $1
	`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInviteNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get invite by id: %w", err)
	}
	return inv, nil
}

// GetByToken looks up an invite by its token, returning ErrInviteNotFound
// when no row matches.
func (s *InviteStore) GetByToken(ctx context.Context, token string) (*HouseholdInvite, error) {
	inv, err := scanInvite(s.db.QueryRowContext(ctx, `
		SELECT `+inviteColumns+` FROM household_invites WHERE token = $1
	`, token))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInviteNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get invite by token: %w", err)
	}
	return inv, nil
}

// AcceptInvite marks a pending, unexpired invite as accepted and returns it.
// It returns ErrInviteExpiredOrUsed when no matching pending invite exists.
func (s *InviteStore) AcceptInvite(ctx context.Context, token string) (*HouseholdInvite, error) {
	inv, err := scanInvite(s.db.QueryRowContext(ctx, `
		UPDATE household_invites
		SET status = 'accepted', accepted_at = NOW()
		WHERE token = $1 AND status = 'pending' AND expires_at > NOW()
		RETURNING `+inviteColumns,
		token,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInviteExpiredOrUsed
	}
	if err != nil {
		return nil, fmt.Errorf("accept invite: %w", err)
	}
	return inv, nil
}

// CancelInvite marks a pending invite as cancelled. It returns
// ErrInviteNotPending when the invite is missing or no longer pending.
func (s *InviteStore) CancelInvite(ctx context.Context, inviteID string) error {
	var id string
	err := s.db.QueryRowContext(ctx, `
		UPDATE household_invites SET status = 'cancelled'
		WHERE id = $1 AND status = 'pending'
		RETURNING id
	`, inviteID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInviteNotPending
	}
	if err != nil {
		return fmt.Errorf("cancel invite: %w", err)
	}
	return nil
}

// GetPendingForHousehold returns the household's pending, unexpired invites,
// newest first.
func (s *InviteStore) GetPendingForHousehold(ctx context.Context, householdID string) ([]*HouseholdInvite, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+inviteColumns+`
		FROM household_invites
		WHERE household_id = $1 AND status = 'pending' AND expires_at > NOW()
		ORDER BY created_at DESC
	`, householdID)
	if err != nil {
		return nil, fmt.Errorf("get pending invites: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var invites []*HouseholdInvite
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, fmt.Errorf("scan invite: %w", err)
		}
		invites = append(invites, inv)
	}
	return invites, rows.Err()
}

// IsPendingForHouseholdEmail reports whether a pending, unexpired invite
// already exists for email in householdID.
func (s *InviteStore) IsPendingForHouseholdEmail(ctx context.Context, householdID, email string) (bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM household_invites
		WHERE household_id = $1 AND email = $2 AND status = 'pending' AND expires_at > NOW()
	`, householdID, email).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check pending invite: %w", err)
	}
	return count > 0, nil
}
