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

	"github.com/justinlindh/digits/server/internal/db"
)

// ErrNotFound is returned when a device cannot be found.
var ErrNotFound = errors.New("device not found")

// Device represents a physical handset paired to a line.
type Device struct {
	ID                   int64
	LineID               *int64
	HardwareID           string
	DeviceID             string
	DeviceToken          *string
	PairingCode          *string
	PairingCodeExpiresAt *time.Time
	PairedAt             *time.Time
	CreatedAt            time.Time
	LastSeenAt           *time.Time
}

// Store provides CRUD operations for devices.
type Store struct {
	db *sql.DB
}

// NewStore creates a new device Store backed by the given database.
func NewStore(database *db.Database) *Store {
	return &Store{db: database.DB}
}

// Create inserts a new device record for the given line and hardware ID.
func (s *Store) Create(ctx context.Context, lineID int64, hardwareID string) (*Device, error) {
	d := &Device{}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO devices (line_id, hardware_id)
		VALUES ($1, $2)
		RETURNING id, line_id, hardware_id, device_id, device_token,
		          pairing_code, pairing_code_expires_at, paired_at, created_at, last_seen_at
	`, lineID, hardwareID).Scan(
		&d.ID, &d.LineID, &d.HardwareID, &d.DeviceID, &d.DeviceToken,
		&d.PairingCode, &d.PairingCodeExpiresAt, &d.PairedAt, &d.CreatedAt, &d.LastSeenAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create device: %w", err)
	}
	return d, nil
}

// GetByID returns a device by its primary key.
func (s *Store) GetByID(ctx context.Context, id int64) (*Device, error) {
	d := &Device{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, line_id, hardware_id, device_id, device_token,
		       pairing_code, pairing_code_expires_at, paired_at, created_at, last_seen_at
		FROM devices
		WHERE id = $1
	`, id).Scan(
		&d.ID, &d.LineID, &d.HardwareID, &d.DeviceID, &d.DeviceToken,
		&d.PairingCode, &d.PairingCodeExpiresAt, &d.PairedAt, &d.CreatedAt, &d.LastSeenAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get device by id: %w", err)
	}
	return d, nil
}

// GetByHardwareID returns a device by its hardware identifier.
func (s *Store) GetByHardwareID(ctx context.Context, hwID string) (*Device, error) {
	d := &Device{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, line_id, hardware_id, device_id, device_token,
		       pairing_code, pairing_code_expires_at, paired_at, created_at, last_seen_at
		FROM devices
		WHERE hardware_id = $1
	`, hwID).Scan(
		&d.ID, &d.LineID, &d.HardwareID, &d.DeviceID, &d.DeviceToken,
		&d.PairingCode, &d.PairingCodeExpiresAt, &d.PairedAt, &d.CreatedAt, &d.LastSeenAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get device by hardware id: %w", err)
	}
	return d, nil
}

// ListByLine returns all devices associated with a given line.
func (s *Store) ListByLine(ctx context.Context, lineID int64) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, line_id, hardware_id, device_id, device_token,
		       pairing_code, pairing_code_expires_at, paired_at, created_at, last_seen_at
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
		var d Device
		if err := rows.Scan(
			&d.ID, &d.LineID, &d.HardwareID, &d.DeviceID, &d.DeviceToken,
			&d.PairingCode, &d.PairingCodeExpiresAt, &d.PairedAt, &d.CreatedAt, &d.LastSeenAt,
		); err != nil {
			return nil, fmt.Errorf("scan device: %w", err)
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

// Delete removes a device by its primary key.
func (s *Store) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM devices WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete device: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete device rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetPairingCode sets the pairing code and its expiry on a device.
func (s *Store) SetPairingCode(ctx context.Context, id int64, code string, expiresAt time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE devices
		SET pairing_code = $2, pairing_code_expires_at = $3
		WHERE id = $1
	`, id, code, expiresAt)
	if err != nil {
		return fmt.Errorf("set pairing code: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set pairing code rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CompletePairing marks a device as paired by setting paired_at to now and
// clearing the pairing code fields.
func (s *Store) CompletePairing(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE devices
		SET paired_at = NOW(), pairing_code = NULL, pairing_code_expires_at = NULL
		WHERE id = $1
	`, id)
	if err != nil {
		return fmt.Errorf("complete pairing: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("complete pairing rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
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

// GetByPairingCode returns the device with the given pairing code, provided
// the code has not yet expired. Returns ErrNotFound if no matching device exists.
func (s *Store) GetByPairingCode(ctx context.Context, code string) (*Device, error) {
	d := &Device{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, line_id, hardware_id, device_id, device_token,
		       pairing_code, pairing_code_expires_at, paired_at, created_at, last_seen_at
		FROM devices
		WHERE pairing_code = $1
		  AND pairing_code_expires_at > NOW()
	`, code).Scan(
		&d.ID, &d.LineID, &d.HardwareID, &d.DeviceID, &d.DeviceToken,
		&d.PairingCode, &d.PairingCodeExpiresAt, &d.PairedAt, &d.CreatedAt, &d.LastSeenAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get device by pairing code: %w", err)
	}
	return d, nil
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

// ValidateToken checks if the given plaintext token matches the stored hash
// for the device with the given hardware ID. Uses constant-time comparison.
func (s *Store) ValidateToken(ctx context.Context, hardwareID, token string) (bool, error) {
	var storedHash sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT device_token FROM devices WHERE hardware_id = $1 AND paired_at IS NOT NULL`,
		hardwareID,
	).Scan(&storedHash)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("validate token: %w", err)
	}
	if !storedHash.Valid {
		return false, nil
	}
	candidate := HashToken(token)
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(storedHash.String)) == 1, nil
}
