//go:build integration

package device_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	_ "github.com/lib/pq"

	"github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/device"
)

func openTestDB(t *testing.T) *db.Database {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	return d
}

// TestReassignLine verifies that all devices on srcLine are moved to tgtLine.
func TestReassignLine(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	var householdID string
	if err := d.DB.QueryRowContext(ctx,
		`INSERT INTO households (name) VALUES ('test-household') RETURNING id`,
	).Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}

	var srcLineID, tgtLineID int64
	if err := d.DB.QueryRowContext(ctx,
		`INSERT INTO lines (number, name, household_id) VALUES ($1, 'src', $2) RETURNING id`,
		fmt.Sprintf("555-0001-%d", os.Getpid()), householdID,
	).Scan(&srcLineID); err != nil {
		t.Fatalf("insert src line: %v", err)
	}
	if err := d.DB.QueryRowContext(ctx,
		`INSERT INTO lines (number, name, household_id) VALUES ($1, 'tgt', $2) RETURNING id`,
		fmt.Sprintf("555-0002-%d", os.Getpid()), householdID,
	).Scan(&tgtLineID); err != nil {
		t.Fatalf("insert tgt line: %v", err)
	}

	// Insert two devices on srcLine.
	var dev1ID, dev2ID int64
	if err := d.DB.QueryRowContext(ctx,
		`INSERT INTO devices (line_id, hardware_id, device_id) VALUES ($1, $2, '') RETURNING id`,
		srcLineID, fmt.Sprintf("hw-a-%d", os.Getpid()),
	).Scan(&dev1ID); err != nil {
		t.Fatalf("insert device 1: %v", err)
	}
	if err := d.DB.QueryRowContext(ctx,
		`INSERT INTO devices (line_id, hardware_id, device_id) VALUES ($1, $2, '') RETURNING id`,
		srcLineID, fmt.Sprintf("hw-b-%d", os.Getpid()),
	).Scan(&dev2ID); err != nil {
		t.Fatalf("insert device 2: %v", err)
	}

	t.Cleanup(func() {
		_, _ = d.DB.Exec("DELETE FROM devices WHERE id IN ($1, $2)", dev1ID, dev2ID)
		_, _ = d.DB.Exec("DELETE FROM lines WHERE id IN ($1, $2)", srcLineID, tgtLineID)
		_, _ = d.DB.Exec("DELETE FROM households WHERE id = $1", householdID)
		_ = d.Close()
	})

	store := device.NewStore(d)
	n, err := store.ReassignLine(ctx, srcLineID, tgtLineID)
	if err != nil {
		t.Fatalf("ReassignLine: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 devices moved, got %d", n)
	}

	// Verify source line is empty.
	srcDevices, err := store.ListByLine(ctx, srcLineID)
	if err != nil {
		t.Fatalf("ListByLine src: %v", err)
	}
	if len(srcDevices) != 0 {
		t.Errorf("expected 0 devices on src line, got %d", len(srcDevices))
	}

	// Verify target line has both devices.
	tgtDevices, err := store.ListByLine(ctx, tgtLineID)
	if err != nil {
		t.Fatalf("ListByLine tgt: %v", err)
	}
	if len(tgtDevices) != 2 {
		t.Errorf("expected 2 devices on tgt line, got %d", len(tgtDevices))
	}
}

// TestReassignLineNoDevices verifies that reassigning from an empty line returns 0.
func TestReassignLineNoDevices(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	var householdID string
	if err := d.DB.QueryRowContext(ctx,
		`INSERT INTO households (name) VALUES ('test-household-empty') RETURNING id`,
	).Scan(&householdID); err != nil {
		t.Fatalf("insert household: %v", err)
	}

	var srcLineID, tgtLineID int64
	if err := d.DB.QueryRowContext(ctx,
		`INSERT INTO lines (number, name, household_id) VALUES ($1, 'src-empty', $2) RETURNING id`,
		fmt.Sprintf("555-0003-%d", os.Getpid()), householdID,
	).Scan(&srcLineID); err != nil {
		t.Fatalf("insert src line: %v", err)
	}
	if err := d.DB.QueryRowContext(ctx,
		`INSERT INTO lines (number, name, household_id) VALUES ($1, 'tgt-empty', $2) RETURNING id`,
		fmt.Sprintf("555-0004-%d", os.Getpid()), householdID,
	).Scan(&tgtLineID); err != nil {
		t.Fatalf("insert tgt line: %v", err)
	}

	t.Cleanup(func() {
		_, _ = d.DB.Exec("DELETE FROM lines WHERE id IN ($1, $2)", srcLineID, tgtLineID)
		_, _ = d.DB.Exec("DELETE FROM households WHERE id = $1", householdID)
		_ = d.Close()
	})

	store := device.NewStore(d)
	n, err := store.ReassignLine(ctx, srcLineID, tgtLineID)
	if err != nil {
		t.Fatalf("ReassignLine: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 devices moved, got %d", n)
	}
}
