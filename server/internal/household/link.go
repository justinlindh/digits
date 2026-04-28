package household

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const inviteCodeAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
const inviteCodeLength = 8

// HouseholdLink represents a link (invitation or active connection) between two households.
type HouseholdLink struct {
	ID           string
	HouseholdAID string
	HouseholdBID *string // NULL while invite is pending
	Status       string  // pending, active, revoked
	InviteCode   string
	InvitedBy    string
	AcceptedBy   *string
	CreatedAt    time.Time
	AcceptedAt   *time.Time
	RevokedAt    *time.Time
	RevokedBy    *string
}

// LinkStore provides household link persistence backed by Postgres.
type LinkStore struct {
	db *sql.DB
}

// NewLinkStore wraps an existing *sql.DB.
func NewLinkStore(db *sql.DB) *LinkStore {
	return &LinkStore{db: db}
}

// generateInviteCode returns an 8-character crypto-random alphanumeric string.
func generateInviteCode() (string, error) {
	buf := make([]byte, inviteCodeLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate invite code: %w", err)
	}
	for i, b := range buf {
		buf[i] = inviteCodeAlphabet[int(b)%len(inviteCodeAlphabet)]
	}
	return string(buf), nil
}

// CreateInvite creates a pending invite from fromHouseholdID.
// household_b_id is NULL until the invite is accepted.
// Multiple pending invites per household are allowed; duplicate-link
// prevention happens in AcceptInvite via AreLinked.
func (s *LinkStore) CreateInvite(ctx context.Context, fromHouseholdID, invitedByUserID string) (*HouseholdLink, error) {
	code, err := generateInviteCode()
	if err != nil {
		return nil, err
	}

	link := &HouseholdLink{}
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO household_links (household_a_id, invited_by, invite_code, status)
		VALUES ($1, $2, $3, 'pending')
		RETURNING id, household_a_id, household_b_id, status, invite_code, invited_by,
		          accepted_by, created_at, accepted_at, revoked_at, revoked_by
	`, fromHouseholdID, invitedByUserID, code).Scan(
		&link.ID, &link.HouseholdAID, &link.HouseholdBID, &link.Status, &link.InviteCode,
		&link.InvitedBy, &link.AcceptedBy, &link.CreatedAt, &link.AcceptedAt,
		&link.RevokedAt, &link.RevokedBy,
	)
	if err != nil {
		return nil, fmt.Errorf("create invite: %w", err)
	}
	return link, nil
}

// AcceptInvite finds a pending invite by code, associates the accepting household,
// normalizes ordering (a_id < b_id), and marks it active.
func (s *LinkStore) AcceptInvite(ctx context.Context, code, acceptingUserID, acceptingHouseholdID string) (*HouseholdLink, error) {
	// Fetch the pending invite
	link := &HouseholdLink{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, household_a_id, household_b_id, status, invite_code, invited_by,
		       accepted_by, created_at, accepted_at, revoked_at, revoked_by
		FROM household_links
		WHERE invite_code = $1 AND status = 'pending'
	`, code).Scan(
		&link.ID, &link.HouseholdAID, &link.HouseholdBID, &link.Status, &link.InviteCode,
		&link.InvitedBy, &link.AcceptedBy, &link.CreatedAt, &link.AcceptedAt,
		&link.RevokedAt, &link.RevokedBy,
	)
	if err == sql.ErrNoRows {
		return nil, errors.New("invite code not found or already used")
	}
	if err != nil {
		return nil, fmt.Errorf("lookup invite: %w", err)
	}

	// Prevent self-linking
	if link.HouseholdAID == acceptingHouseholdID {
		return nil, errors.New("cannot link a household to itself")
	}

	// Check not already linked
	already, err := s.AreLinked(ctx, link.HouseholdAID, acceptingHouseholdID)
	if err != nil {
		return nil, err
	}
	if already {
		return nil, errors.New("households are already linked")
	}

	// Normalize: a_id < b_id
	aID := link.HouseholdAID
	bID := acceptingHouseholdID
	if aID > bID {
		aID, bID = bID, aID
	}

	now := time.Now()
	err = s.db.QueryRowContext(ctx, `
		UPDATE household_links
		SET household_a_id = $1,
		    household_b_id = $2,
		    status = 'active',
		    accepted_by = $3,
		    accepted_at = $4
		WHERE id = $5
		RETURNING id, household_a_id, household_b_id, status, invite_code, invited_by,
		          accepted_by, created_at, accepted_at, revoked_at, revoked_by
	`, aID, bID, acceptingUserID, now, link.ID).Scan(
		&link.ID, &link.HouseholdAID, &link.HouseholdBID, &link.Status, &link.InviteCode,
		&link.InvitedBy, &link.AcceptedBy, &link.CreatedAt, &link.AcceptedAt,
		&link.RevokedAt, &link.RevokedBy,
	)
	if err != nil {
		return nil, fmt.Errorf("accept invite: %w", err)
	}
	return link, nil
}

// GetLinkedHouseholds returns all active links where householdID is either a or b.
func (s *LinkStore) GetLinkedHouseholds(ctx context.Context, householdID string) ([]HouseholdLink, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, household_a_id, household_b_id, status, invite_code, invited_by,
		       accepted_by, created_at, accepted_at, revoked_at, revoked_by
		FROM household_links
		WHERE status = 'active'
		  AND (household_a_id = $1 OR household_b_id = $1)
	`, householdID)
	if err != nil {
		return nil, fmt.Errorf("get linked households: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanLinks(rows)
}

// AreLinked returns true if the two households have an active link.
func (s *LinkStore) AreLinked(ctx context.Context, householdAID, householdBID string) (bool, error) {
	// Normalize
	a, b := householdAID, householdBID
	if a > b {
		a, b = b, a
	}
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM household_links
		WHERE household_a_id = $1 AND household_b_id = $2 AND status = 'active'
	`, a, b).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("are linked: %w", err)
	}
	return count > 0, nil
}

// RevokeLink sets a link's status to 'revoked'. Returns an error if the link
// does not exist or is already revoked.
func (s *LinkStore) RevokeLink(ctx context.Context, linkID, revokedByUserID string) error {
	var id string
	err := s.db.QueryRowContext(ctx, `
		UPDATE household_links
		SET status = 'revoked', revoked_at = $1, revoked_by = $2
		WHERE id = $3 AND status != 'revoked'
		RETURNING id
	`, time.Now(), revokedByUserID, linkID).Scan(&id)
	if err == sql.ErrNoRows {
		return errors.New("link not found or already revoked")
	}
	if err != nil {
		return fmt.Errorf("revoke link: %w", err)
	}
	return nil
}

// GetByID returns a link by its ID.
func (s *LinkStore) GetByID(ctx context.Context, id string) (*HouseholdLink, error) {
	link := &HouseholdLink{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, household_a_id, household_b_id, status, invite_code, invited_by,
		       accepted_by, created_at, accepted_at, revoked_at, revoked_by
		FROM household_links WHERE id = $1
	`, id).Scan(
		&link.ID, &link.HouseholdAID, &link.HouseholdBID, &link.Status, &link.InviteCode,
		&link.InvitedBy, &link.AcceptedBy, &link.CreatedAt, &link.AcceptedAt,
		&link.RevokedAt, &link.RevokedBy,
	)
	if err == sql.ErrNoRows {
		return nil, errors.New("link not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get link by id: %w", err)
	}
	return link, nil
}

// GetPendingForHousehold returns all pending invites where householdID is the inviting household.
func (s *LinkStore) GetPendingForHousehold(ctx context.Context, householdID string) ([]HouseholdLink, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, household_a_id, household_b_id, status, invite_code, invited_by,
		       accepted_by, created_at, accepted_at, revoked_at, revoked_by
		FROM household_links
		WHERE status = 'pending' AND household_a_id = $1
	`, householdID)
	if err != nil {
		return nil, fmt.Errorf("get pending for household: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanLinks(rows)
}

func scanLinks(rows *sql.Rows) ([]HouseholdLink, error) {
	var links []HouseholdLink
	for rows.Next() {
		var l HouseholdLink
		if err := rows.Scan(
			&l.ID, &l.HouseholdAID, &l.HouseholdBID, &l.Status, &l.InviteCode,
			&l.InvitedBy, &l.AcceptedBy, &l.CreatedAt, &l.AcceptedAt,
			&l.RevokedAt, &l.RevokedBy,
		); err != nil {
			return nil, fmt.Errorf("scan link: %w", err)
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

// CountActiveLinks returns the total number of active household links.
func (s *LinkStore) CountActiveLinks(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM household_links WHERE status = 'active'`).Scan(&count)
	return count, err
}

// NumberConflict represents a phone number that exists in both households.
type NumberConflict struct {
	Number       string
	PhoneAName   string // name in household A
	PhoneBName   string // name in household B
	HouseholdAID string
	HouseholdBID string
}

// FindNumberConflicts checks for phone numbers that appear in both households' networks.
func (s *LinkStore) FindNumberConflicts(ctx context.Context, householdAID, householdBID string) ([]NumberConflict, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.number, a.name, b.name
		FROM lines a
		JOIN lines b ON a.number = b.number AND a.id != b.id
		WHERE a.household_id = $1 AND b.household_id = $2
		  AND a.number != '' AND b.number != ''
	`, householdAID, householdBID)
	if err != nil {
		return nil, fmt.Errorf("find conflicts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var conflicts []NumberConflict
	for rows.Next() {
		c := NumberConflict{HouseholdAID: householdAID, HouseholdBID: householdBID}
		if err := rows.Scan(&c.Number, &c.PhoneAName, &c.PhoneBName); err != nil {
			return nil, err
		}
		conflicts = append(conflicts, c)
	}
	return conflicts, rows.Err()
}
