// Package pairing implements the device-to-line pairing flow: generating
// short numeric codes displayed on the phone, validating them from the web
// UI, and completing the pairing by writing a device token to the database.
// Typed errors (ErrInvalidCode, ErrAlreadyPaired, ErrNumberTaken) let callers
// present specific feedback without inspecting error strings.
package pairing

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/lib/pq"

	"github.com/justinlindh/digits/server/internal/dbutil"
	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/tokens"
)

const (
	// CodeLength is the number of digits in a pairing code.
	CodeLength = 6
	// CodeTTL is how long a pairing code remains valid. The device is told
	// this TTL (via PairingCodeTTL) so its spoken countdown matches and it
	// refreshes before expiry; keep it comfortably longer than the time it
	// takes a user to hear the code and type it into the web UI.
	CodeTTL = 10 * time.Minute
)

var (
	ErrInvalidCode   = errors.New("invalid or expired pairing code")
	ErrAlreadyPaired = errors.New("device is already paired")
	ErrNumberTaken   = errors.New("line number is already in use")
	ErrLineNotOwned  = errors.New("line does not belong to this household")
)

// Store handles device pairing operations.
type Store struct {
	db *sql.DB
}

// NewStore creates a new pairing Store.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// GenerateCode creates a fresh 6-digit pairing code for the given hardware ID,
// replacing any previously issued code. A new code is generated on every call
// so that a leaked code becomes invalid as soon as the device reconnects.
func (s *Store) GenerateCode(ctx context.Context, hardwareID string) (string, error) {
	newCode, err := randomCode(CodeLength)
	if err != nil {
		return "", fmt.Errorf("generate code: %w", err)
	}

	expiresAt := time.Now().Add(CodeTTL)

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO devices (line_id, hardware_id, pairing_code, pairing_code_expires_at)
		VALUES (NULL, $1, $2, $3)
		ON CONFLICT (hardware_id) DO UPDATE
		SET pairing_code = $2, pairing_code_expires_at = $3
		WHERE devices.paired_at IS NULL
	`, hardwareID, newCode, expiresAt)
	if err != nil {
		return "", fmt.Errorf("upsert pairing code: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("upsert pairing code: %w", err)
	}
	if n == 0 {
		return "", ErrAlreadyPaired
	}

	return newCode, nil
}

// lookupPairingCode locks the device row for the given code within a
// transaction and returns (deviceID, hardwareID). Returns ErrInvalidCode
// when the code is missing, expired, or already claimed.
func lookupPairingCode(ctx context.Context, tx *sql.Tx, code string) (int64, string, error) {
	var deviceID int64
	var hwID sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT id, hardware_id FROM devices
		WHERE pairing_code = $1 AND pairing_code_expires_at > NOW() AND paired_at IS NULL
		FOR UPDATE
	`, code).Scan(&deviceID, &hwID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", ErrInvalidCode
	}
	if err != nil {
		return 0, "", fmt.Errorf("lookup pairing code: %w", err)
	}
	return deviceID, hwID.String, nil
}

// bindDeviceToLine marks a device as paired and assigns it to a line within
// a transaction. Clears the pairing code and sets the device token.
func bindDeviceToLine(ctx context.Context, tx *sql.Tx, deviceID, lineID int64, tokenHash, deviceName string) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE devices
		SET line_id = $2, device_token = $3, name = $4,
		    paired_at = NOW(), pairing_code = NULL, pairing_code_expires_at = NULL
		WHERE id = $1 AND paired_at IS NULL
	`, deviceID, lineID, tokenHash, deviceName)
	if err != nil {
		return fmt.Errorf("bind device to line: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("bind device to line: %w", err)
	}
	if n == 0 {
		return ErrInvalidCode
	}
	return nil
}

// ClaimDevice pairs a device using a pairing code, creating a new line and
// assigning the device in a single transaction. If any step fails the whole
// operation rolls back, leaving the device claimable with a fresh code.
// Returns (deviceToken, hardwareID, error).
func (s *Store) ClaimDevice(ctx context.Context, code, lineNumber, lineName, deviceName, householdID string) (string, string, error) {
	token, err := tokens.RandomHex(32)
	if err != nil {
		return "", "", fmt.Errorf("generate device token: %w", err)
	}
	tokenHash := tokens.Hash(token)

	var hardwareID string
	if err := dbutil.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		deviceID, hwID, err := lookupPairingCode(ctx, tx, code)
		if err != nil {
			return err
		}
		hardwareID = hwID

		defaults, jsonErr := json.Marshal(line.DefaultSettings().Normalize())
		if jsonErr != nil {
			return fmt.Errorf("marshal default settings: %w", jsonErr)
		}
		var lineID int64
		err = tx.QueryRowContext(ctx, `
			INSERT INTO lines (number, name, household_id, settings)
			VALUES ($1, $2, $3, $4)
			RETURNING id
		`, lineNumber, lineName, householdID, defaults).Scan(&lineID)
		if err != nil {
			if isUniqueViolation(err) {
				return ErrNumberTaken
			}
			return fmt.Errorf("create line: %w", err)
		}

		return bindDeviceToLine(ctx, tx, deviceID, lineID, tokenHash, deviceName)
	}); err != nil {
		return "", "", err
	}
	return token, hardwareID, nil
}

// ClaimDeviceToLine pairs a device to an existing line using a pairing code.
// Unlike ClaimDevice, no new line is created. The line must exist and belong
// to the given household. Returns (deviceToken, hardwareID, error).
func (s *Store) ClaimDeviceToLine(ctx context.Context, code string, lineID int64, deviceName, householdID string) (string, string, error) {
	token, err := tokens.RandomHex(32)
	if err != nil {
		return "", "", fmt.Errorf("generate device token: %w", err)
	}
	tokenHash := tokens.Hash(token)

	var hardwareID string
	if err := dbutil.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		deviceID, hwID, err := lookupPairingCode(ctx, tx, code)
		if err != nil {
			return err
		}
		hardwareID = hwID

		var ownerHH string
		err = tx.QueryRowContext(ctx,
			`SELECT household_id FROM lines WHERE id = $1`, lineID,
		).Scan(&ownerHH)
		if errors.Is(err, sql.ErrNoRows) {
			return line.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("verify line ownership: %w", err)
		}
		if ownerHH != householdID {
			return ErrLineNotOwned
		}

		return bindDeviceToLine(ctx, tx, deviceID, lineID, tokenHash, deviceName)
	}); err != nil {
		return "", "", err
	}
	return token, hardwareID, nil
}

// CleanupExpired nulls out pairing codes that have passed their expiry time
// on unpaired devices. Returns the number of codes cleaned.
func (s *Store) CleanupExpired(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
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

func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
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
