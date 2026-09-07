//go:build integration

package calls_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/line"
)

func TestReviewRenumberGateFreezesLegacyAndCurrentWriters(t *testing.T) {
	d := openTestDB(t)
	setRenumberEnabled(t, d, false)
	tr := calls.New(d.DB)
	lineID := seedRenumberLine(t, d, "7640001")

	err := tr.RenumberLine(context.Background(), lineID, "7640001", "7640002", "rollout")
	if !errors.Is(err, calls.ErrRenumberDisabled) {
		t.Fatalf("renumber with default-off gate: got %v, want ErrRenumberDisabled", err)
	}

	// The gate freezes number changes, not ordinary service. This direct insert
	// is the exact call path used by a legacy replica and must remain available
	// during the rolling deployment while renumber stays disabled.
	var id int64
	if err := d.DB.QueryRowContext(context.Background(),
		"INSERT INTO calls (caller, callee, status) VALUES ($1, $2, 'initiated') RETURNING id",
		"7640001", "7640003",
	).Scan(&id); err != nil {
		t.Fatalf("legacy call insert while renumber frozen: %v", err)
	}

	var number string
	if err := d.DB.QueryRowContext(context.Background(),
		`SELECT number FROM lines WHERE id = $1`, lineID).Scan(&number); err != nil {
		t.Fatal(err)
	}
	if number != "7640001" {
		t.Fatalf("disabled renumber changed line to %s", number)
	}
}

func TestReviewRenumberGateBlocksLegacyLineUpdate(t *testing.T) {
	d := openTestDB(t)
	setRenumberEnabled(t, d, false)
	lineID := seedRenumberLine(t, d, "7640011")

	if _, err := d.DB.ExecContext(context.Background(),
		`UPDATE lines SET number = $1 WHERE id = $2`, "7640012", lineID); err == nil {
		t.Fatal("legacy direct line update bypassed disabled renumber gate")
	}
}

func TestReviewGateDisableWaitsForLegacyRenumberCommitBoundary(t *testing.T) {
	d := openTestDB(t)
	setRenumberEnabled(t, d, true)
	lineID := seedRenumberLine(t, d, "7640021")

	tx, err := d.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(context.Background(),
		`UPDATE lines SET number = $1 WHERE id = $2`, "7640022", lineID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}

	disabled := make(chan error, 1)
	go func() {
		_, err := d.DB.ExecContext(context.Background(),
			`UPDATE renumber_control SET enabled = FALSE, updated_at = NOW() WHERE singleton`)
		disabled <- err
	}()
	select {
	case err := <-disabled:
		_ = tx.Rollback()
		t.Fatalf("gate disable crossed an in-flight legacy renumber: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-disabled; err != nil {
		t.Fatal(err)
	}
	if _, err := d.DB.ExecContext(context.Background(),
		`UPDATE lines SET number = $1 WHERE id = $2`, "7640023", lineID); err == nil {
		t.Fatal("legacy renumber committed after gate-disable boundary")
	}
}

func TestReviewIdentityCutoverStaysStrictWhenWritesRefreeze(t *testing.T) {
	d := openTestDB(t)
	store := line.NewStore(d.DB)
	setRenumberEnabled(t, d, false)
	allowed, err := store.LegacyIdentityAllowed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("legacy identity rejected before first activation")
	}
	setRenumberEnabled(t, d, true)
	setRenumberEnabled(t, d, false)
	allowed, err = store.LegacyIdentityAllowed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("refreezing renumber writes re-authorized legacy identity")
	}
}
