package household

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/justinlindh/digits/server/internal/dbutil"
)

// LinkStatusPending is the only link status compared in Go; the SQL in this
// file also writes 'active' and 'revoked', matching the DB CHECK constraint
// defined in db.go.
const LinkStatusPending = "pending"

const inviteCodeAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
const inviteCodeLength = 8

// linkColumns is the SELECT/RETURNING list for queries that scan into a
// HouseholdLink. Field order must stay aligned with the destination order in
// scanLink below.
const linkColumns = `id, household_a_id, household_b_id, status, invite_code, invited_by,
	accepted_by, created_at, accepted_at, revoked_at, revoked_by`

// scanLink materializes a HouseholdLink from any row whose columns match
// linkColumns in order.
func scanLink(row dbutil.RowScanner) (*HouseholdLink, error) {
	l := &HouseholdLink{}
	if err := row.Scan(
		&l.ID, &l.HouseholdAID, &l.HouseholdBID, &l.Status, &l.InviteCode,
		&l.InvitedBy, &l.AcceptedBy, &l.CreatedAt, &l.AcceptedAt,
		&l.RevokedAt, &l.RevokedBy,
	); err != nil {
		return nil, err
	}
	return l, nil
}

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
// Each character is drawn with rand.Int rather than a byte modulo, so the
// distribution over the 36-character alphabet is uniform.
func generateInviteCode() (string, error) {
	buf := make([]byte, inviteCodeLength)
	alphabetLen := big.NewInt(int64(len(inviteCodeAlphabet)))
	for i := range buf {
		n, err := rand.Int(rand.Reader, alphabetLen)
		if err != nil {
			return "", fmt.Errorf("generate invite code: %w", err)
		}
		buf[i] = inviteCodeAlphabet[n.Int64()]
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

	link, err := scanLink(s.db.QueryRowContext(ctx, `
		INSERT INTO household_links (household_a_id, invited_by, invite_code, status)
		VALUES ($1, $2, $3, 'pending')
		RETURNING `+linkColumns+`
	`, fromHouseholdID, invitedByUserID, code))
	if err != nil {
		return nil, fmt.Errorf("create invite: %w", err)
	}
	return link, nil
}

// AcceptInvite finds a pending invite by code, associates the accepting household,
// normalizes ordering (a_id < b_id), and marks it active. The whole operation
// runs in a transaction with the invite row locked, so two racing accepts of
// the same code serialize and the loser sees it as already used.
func (s *LinkStore) AcceptInvite(ctx context.Context, code, acceptingUserID, acceptingHouseholdID string) (*HouseholdLink, error) {
	var link *HouseholdLink
	err := dbutil.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		var err error
		link, err = scanLink(tx.QueryRowContext(ctx, `
			SELECT `+linkColumns+`
			FROM household_links
			WHERE invite_code = $1 AND status = 'pending'
			FOR UPDATE
		`, code))
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("invite code not found or already used")
		}
		if err != nil {
			return fmt.Errorf("lookup invite: %w", err)
		}

		// Prevent self-linking
		if link.HouseholdAID == acceptingHouseholdID {
			return errors.New("cannot link a household to itself")
		}

		// Check not already linked
		already, err := areLinked(ctx, tx, link.HouseholdAID, acceptingHouseholdID)
		if err != nil {
			return err
		}
		if already {
			return errors.New("households are already linked")
		}

		// Normalize: a_id < b_id
		aID := link.HouseholdAID
		bID := acceptingHouseholdID
		if aID > bID {
			aID, bID = bID, aID
		}

		link, err = scanLink(tx.QueryRowContext(ctx, `
			UPDATE household_links
			SET household_a_id = $1,
			    household_b_id = $2,
			    status = 'active',
			    accepted_by = $3,
			    accepted_at = $4
			WHERE id = $5
			RETURNING `+linkColumns+`
		`, aID, bID, acceptingUserID, time.Now(), link.ID))
		if err != nil {
			return fmt.Errorf("accept invite: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return link, nil
}

// GetLinkedHouseholds returns all active links where householdID is either a or b.
func (s *LinkStore) GetLinkedHouseholds(ctx context.Context, householdID string) ([]HouseholdLink, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+linkColumns+`
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
	return areLinked(ctx, s.db, householdAID, householdBID)
}

// rowQuerier is satisfied by both *sql.DB and *sql.Tx, so link checks can run
// standalone or inside a transaction.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func areLinked(ctx context.Context, q rowQuerier, householdAID, householdBID string) (bool, error) {
	// Normalize
	a, b := householdAID, householdBID
	if a > b {
		a, b = b, a
	}
	var count int
	err := q.QueryRowContext(ctx, `
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
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("link not found or already revoked")
	}
	if err != nil {
		return fmt.Errorf("revoke link: %w", err)
	}
	return nil
}

// GetByID returns a link by its ID.
func (s *LinkStore) GetByID(ctx context.Context, id string) (*HouseholdLink, error) {
	link, err := scanLink(s.db.QueryRowContext(ctx, `
		SELECT `+linkColumns+`
		FROM household_links WHERE id = $1
	`, id))
	if errors.Is(err, sql.ErrNoRows) {
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
		SELECT `+linkColumns+`
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
		l, err := scanLink(rows)
		if err != nil {
			return nil, fmt.Errorf("scan link: %w", err)
		}
		links = append(links, *l)
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
			return nil, fmt.Errorf("scan conflict: %w", err)
		}
		conflicts = append(conflicts, c)
	}
	return conflicts, rows.Err()
}
