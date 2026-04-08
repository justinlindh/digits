package device

import (
	"database/sql"
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
func (s *Store) Create(lineID int64, hardwareID string) (*Device, error) {
	d := &Device{}
	err := s.db.QueryRow(`
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
func (s *Store) GetByID(id int64) (*Device, error) {
	d := &Device{}
	err := s.db.QueryRow(`
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
func (s *Store) GetByHardwareID(hwID string) (*Device, error) {
	d := &Device{}
	err := s.db.QueryRow(`
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
func (s *Store) ListByLine(lineID int64) ([]Device, error) {
	rows, err := s.db.Query(`
		SELECT id, line_id, hardware_id, device_id, device_token,
		       pairing_code, pairing_code_expires_at, paired_at, created_at, last_seen_at
		FROM devices
		WHERE line_id = $1
		ORDER BY created_at
	`, lineID)
	if err != nil {
		return nil, fmt.Errorf("list devices by line: %w", err)
	}
	defer rows.Close()

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
func (s *Store) Delete(id int64) error {
	res, err := s.db.Exec(`DELETE FROM devices WHERE id = $1`, id)
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
func (s *Store) SetPairingCode(id int64, code string, expiresAt time.Time) error {
	res, err := s.db.Exec(`
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
func (s *Store) CompletePairing(id int64) error {
	res, err := s.db.Exec(`
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

// TouchLastSeen updates last_seen_at to NOW() for the device with the given hardware ID.
func (s *Store) TouchLastSeen(hardwareID string) error {
	_, err := s.db.Exec(
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
func (s *Store) GetByPairingCode(code string) (*Device, error) {
	d := &Device{}
	err := s.db.QueryRow(`
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
