// Package device manages physical handsets paired to lines. Store handles
// the read paths and side-effecting mutations (heartbeat, unpair, reassign)
// while the pairing package owns device row creation and the pairing-code
// lifecycle. HashToken is shared with the auth and pairing packages.
package device

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/dbutil"
)

// Device represents a physical handset paired to a line.
type Device struct {
	ID                   int64
	LineID               *int64
	Name                 string
	HardwareID           string
	DeviceID             string
	DeviceToken          *string
	PairingCode          *string
	PairingCodeExpiresAt *time.Time
	PairedAt             *time.Time
	CreatedAt            time.Time
	LastSeenAt           *time.Time
}

// Store provides device queries and pairing-state mutations. Row inserts
// happen in the pairing package (which owns the pairing-code lifecycle);
// this package handles the read paths and the side effects pairing does
// not own (last-seen heartbeat, repair-flow unpair).
type Store struct {
	db *sql.DB
}

// NewStore creates a new device Store backed by the given database.
func NewStore(database *db.Database) *Store {
	return &Store{db: database.DB}
}

// deviceColumns is the SELECT list for queries that scan into a Device via
// scanDevice. Keep the order in sync with the scan there.
const deviceColumns = `id, line_id, name, hardware_id, device_id, device_token,
	pairing_code, pairing_code_expires_at, paired_at, created_at, last_seen_at`

// scanDevice materializes a Device from any row whose columns match
// deviceColumns in order.
func scanDevice(row dbutil.RowScanner) (Device, error) {
	var d Device
	if err := row.Scan(
		&d.ID, &d.LineID, &d.Name, &d.HardwareID, &d.DeviceID, &d.DeviceToken,
		&d.PairingCode, &d.PairingCodeExpiresAt, &d.PairedAt, &d.CreatedAt, &d.LastSeenAt,
	); err != nil {
		return Device{}, fmt.Errorf("scan device: %w", err)
	}
	return d, nil
}

// ListByLine returns all devices associated with a given line.
func (s *Store) ListByLine(ctx context.Context, lineID int64) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+deviceColumns+`
		FROM devices
		WHERE line_id = $1
		ORDER BY created_at
	`, lineID)
	if err != nil {
		return nil, fmt.Errorf("list devices by line: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var devices []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

// ListByLines returns all devices for the given line IDs, grouped by line ID
// and ordered by created_at within each group. Batch variant of ListByLine
// for callers that render many lines at once.
func (s *Store) ListByLines(ctx context.Context, lineIDs []int64) (map[int64][]Device, error) {
	if len(lineIDs) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+deviceColumns+`
		FROM devices
		WHERE line_id = ANY($1)
		ORDER BY created_at
	`, pq.Array(lineIDs))
	if err != nil {
		return nil, fmt.Errorf("list devices by lines: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[int64][]Device, len(lineIDs))
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		if d.LineID != nil {
			result[*d.LineID] = append(result[*d.LineID], d)
		}
	}
	return result, rows.Err()
}

// Unpair invalidates the device's paired_at and device_token so the next
// register-without-token from this hardware ID will be issued a fresh
// pairing code instead of being rejected. Used by the Phone -> Server
// repair flow (the *#0* service code) so digitsd can tell the server
// "I'm intentionally clearing my local token, please drop yours too."
// No-op (returns nil) if the device row doesn't exist.
func (s *Store) Unpair(ctx context.Context, hardwareID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE devices
		SET paired_at = NULL, device_token = NULL
		WHERE hardware_id = $1
	`, hardwareID)
	if err != nil {
		return fmt.Errorf("unpair: %w", err)
	}
	return nil
}

// Reassign moves a single device to a different line.
func (s *Store) Reassign(ctx context.Context, deviceID, newLineID int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE devices SET line_id = $1 WHERE id = $2`,
		newLineID, deviceID,
	)
	if err != nil {
		return fmt.Errorf("reassign device: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("reassign device: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("reassign device: device %d not found", deviceID)
	}
	return nil
}

// TouchLastSeen updates last_seen_at to NOW() for the device with the given hardware ID.
func (s *Store) TouchLastSeen(ctx context.Context, hardwareID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE devices SET last_seen_at = NOW() WHERE hardware_id = $1`,
		hardwareID,
	)
	if err != nil {
		return fmt.Errorf("touch last seen: %w", err)
	}
	return nil
}

// HashToken returns the SHA-256 hex hash of a plaintext device token.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// AuthStatus returns the pairing and token status for a device.
// Returns (paired, tokenValid, error).
// If the device doesn't exist, returns (false, false, nil).
// If paired and token is provided, validates it against the stored hash.
func (s *Store) AuthStatus(ctx context.Context, hardwareID, token string) (paired bool, tokenValid bool, err error) {
	var pairedAt sql.NullTime
	var storedHash sql.NullString
	err = s.db.QueryRowContext(ctx,
		`SELECT paired_at, device_token FROM devices WHERE hardware_id = $1`,
		hardwareID,
	).Scan(&pairedAt, &storedHash)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("auth status: %w", err)
	}
	if !pairedAt.Valid {
		return false, false, nil
	}
	if token == "" || !storedHash.Valid {
		return true, false, nil
	}
	candidate := HashToken(token)
	valid := subtle.ConstantTimeCompare([]byte(candidate), []byte(storedHash.String)) == 1
	return true, valid, nil
}

// BoundLineNumber returns the line number assigned to the paired device with
// the given hardware ID. Returns ("", nil) if the device has no bound line.
func (s *Store) BoundLineNumber(ctx context.Context, hardwareID string) (string, error) {
	var number string
	err := s.db.QueryRowContext(ctx, `
		SELECT l.number FROM devices d
		JOIN lines l ON l.id = d.line_id
		WHERE d.hardware_id = $1 AND d.paired_at IS NOT NULL
	`, hardwareID).Scan(&number)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("bound line number: %w", err)
	}
	return number, nil
}
