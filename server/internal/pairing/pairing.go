package pairing

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"
)

const (
	// CodeLength is the number of digits in a pairing code.
	CodeLength = 6
	// CodeTTL is how long a pairing code remains valid.
	CodeTTL = 10 * time.Minute
)

var (
	ErrInvalidCode = errors.New("invalid or expired pairing code")
	ErrAlreadyPaired = errors.New("device is already paired")
	ErrNumberTaken = errors.New("line number is already in use")
)

// Store handles device pairing operations.
type Store struct {
	db *sql.DB
}

// NewStore creates a new pairing Store.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// GenerateCode returns a 6-digit pairing code for the given hardware ID.
// If a valid (non-expired, unpaired) code already exists, it is reused.
func (s *Store) GenerateCode(hardwareID string) (string, error) {
	// Check for existing valid code
	var code sql.NullString
	err := s.db.QueryRow(`
		SELECT pairing_code FROM devices
		WHERE hardware_id = $1
		  AND pairing_code IS NOT NULL
		  AND pairing_code_expires_at > NOW()
		  AND paired_at IS NULL
	`, hardwareID).Scan(&code)

	if err == nil && code.Valid {
		return code.String, nil
	}

	// Generate a new code
	newCode, err := randomCode(CodeLength)
	if err != nil {
		return "", fmt.Errorf("generate code: %w", err)
	}

	expiresAt := time.Now().Add(CodeTTL)

	// Upsert: insert device row if it doesn't exist, or update the pairing code.
	// New devices get line_id=NULL until pairing completes and a line is created.
	_, err = s.db.Exec(`
		INSERT INTO devices (line_id, hardware_id, pairing_code, pairing_code_expires_at)
		VALUES (NULL, $1, $2, $3)
		ON CONFLICT (hardware_id) DO UPDATE
		SET pairing_code = $2, pairing_code_expires_at = $3
	`, hardwareID, newCode, expiresAt)
	if err != nil {
		return "", fmt.Errorf("upsert pairing code: %w", err)
	}

	return newCode, nil
}

// ClaimDevice pairs a device using a pairing code, creating or looking up the
// line and assigning the device to it.
// Returns (deviceToken, hardwareID, error).
func (s *Store) ClaimDevice(code, lineNumber, lineName, householdID string) (string, string, error) {
	// Check line number uniqueness
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM lines WHERE number = $1`,
		lineNumber,
	).Scan(&count)
	if err != nil {
		return "", "", fmt.Errorf("check number uniqueness: %w", err)
	}
	if count > 0 {
		return "", "", ErrNumberTaken
	}

	// Find the device with this pairing code
	var deviceID int64
	var hardwareID sql.NullString
	err = s.db.QueryRow(`
		SELECT id, hardware_id FROM devices
		WHERE pairing_code = $1 AND pairing_code_expires_at > NOW() AND paired_at IS NULL
	`, code).Scan(&deviceID, &hardwareID)
	if err == sql.ErrNoRows {
		return "", "", ErrInvalidCode
	}
	if err != nil {
		return "", "", fmt.Errorf("lookup pairing code: %w", err)
	}

	// Create the line
	var lineID int64
	err = s.db.QueryRow(`
		INSERT INTO lines (number, name, household_id)
		VALUES ($1, $2, $3)
		RETURNING id
	`, lineNumber, lineName, householdID).Scan(&lineID)
	if err != nil {
		return "", "", fmt.Errorf("create line: %w", err)
	}

	token := randomHex(32)

	// Update device: set line_id, device_token, mark as paired, clear pairing code
	res, err := s.db.Exec(`
		UPDATE devices
		SET line_id = $2, device_token = $3,
		    paired_at = NOW(), pairing_code = NULL, pairing_code_expires_at = NULL
		WHERE id = $1 AND paired_at IS NULL
	`, deviceID, lineID, token)
	if err != nil {
		return "", "", fmt.Errorf("claim device: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return "", "", fmt.Errorf("claim device rows: %w", err)
	}
	if rows == 0 {
		return "", "", ErrInvalidCode
	}
	return token, hardwareID.String, nil
}

// IsPaired returns whether the device with the given hardware ID has been paired.
func (s *Store) IsPaired(hardwareID string) (bool, error) {
	var pairedAt sql.NullTime
	err := s.db.QueryRow(`
		SELECT paired_at FROM devices WHERE hardware_id = $1
	`, hardwareID).Scan(&pairedAt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check paired: %w", err)
	}
	return pairedAt.Valid, nil
}

// CleanupExpired nulls out pairing codes that have passed their expiry time
// on unpaired devices. Returns the number of codes cleaned.
func (s *Store) CleanupExpired() (int64, error) {
	res, err := s.db.Exec(`
		UPDATE devices
		SET pairing_code = NULL, pairing_code_expires_at = NULL
		WHERE pairing_code IS NOT NULL
		  AND pairing_code_expires_at < NOW()
		  AND paired_at IS NULL
	`)
	if err != nil {
		return 0, fmt.Errorf("cleanup expired codes: %w", err)
	}
	return res.RowsAffected()
}

// RegenerateCode forces a new pairing code for the given hardware ID,
// replacing any existing (valid or expired) code.
func (s *Store) RegenerateCode(hardwareID string) (string, error) {
	code, err := randomCode(CodeLength)
	if err != nil {
		return "", fmt.Errorf("generate code: %w", err)
	}
	expiresAt := time.Now().Add(CodeTTL)

	res, err := s.db.Exec(`
		UPDATE devices
		SET pairing_code = $2, pairing_code_expires_at = $3
		WHERE hardware_id = $1 AND paired_at IS NULL
	`, hardwareID, code, expiresAt)
	if err != nil {
		return "", fmt.Errorf("regenerate code: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return s.GenerateCode(hardwareID)
	}
	return code, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// randomCode generates a cryptographically random numeric code of the given length.
func randomCode(length int) (string, error) {
	max := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(length)), nil)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", length, n), nil
}
