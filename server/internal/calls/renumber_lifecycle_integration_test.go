//go:build integration

package calls_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/justinlindh/digits/server/internal/calls"
	appdb "github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/line"
)

func seedRenumberLine(t *testing.T, d *appdb.Database, oldNumber string) int64 {
	t.Helper()
	ctx := context.Background()
	var householdID string
	if err := d.DB.QueryRowContext(ctx, `INSERT INTO households (name) VALUES ($1) RETURNING id`, "renumber-test").Scan(&householdID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = d.DB.ExecContext(context.Background(), `DELETE FROM households WHERE id = $1`, householdID)
	})
	var lineID int64
	if err := d.DB.QueryRowContext(ctx, `INSERT INTO lines (number, name, household_id) VALUES ($1, 'test', $2) RETURNING id`, oldNumber, householdID).Scan(&lineID); err != nil {
		t.Fatal(err)
	}
	return lineID
}

func TestRenumberLineRejectsActiveCallAtomically(t *testing.T) {
	d := openTestDB(t)
	tr := calls.New(d.DB)
	setRenumberEnabled(t, d, true)
	lineID := seedRenumberLine(t, d, "7600001")
	if _, err := tr.OnCallInitiated(context.Background(), "7600001", "7600002"); err != nil {
		t.Fatal(err)
	}

	err := tr.RenumberLine(context.Background(), lineID, "7600001", "7600003", "test")
	if !errors.Is(err, calls.ErrLineBusy) {
		t.Fatalf("got %v, want calls.ErrLineBusy", err)
	}
	var number string
	if err := d.DB.QueryRow(`SELECT number FROM lines WHERE id = $1`, lineID).Scan(&number); err != nil {
		t.Fatal(err)
	}
	if number != "7600001" {
		t.Fatalf("busy renumber changed line to %s", number)
	}
}

func TestRenumberLineAndCallInitiationCannotCross(t *testing.T) {
	d := openTestDB(t)
	setRenumberEnabled(t, d, true)
	tr := calls.New(d.DB)
	for i := 0; i < 20; i++ {
		oldNumber := fmt.Sprintf("761%04d", i)
		newNumber := fmt.Sprintf("762%04d", i)
		lineID := seedRenumberLine(t, d, oldNumber)
		targetNumber := fmt.Sprintf("763%04d", i)
		targetLineID := seedRenumberLine(t, d, targetNumber)
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var callErr, renameErr error
		go func() {
			defer wg.Done()
			<-start
			_, callErr = tr.OnCallInitiatedBound(context.Background(), oldNumber, targetNumber, lineID, targetLineID)
		}()
		go func() {
			defer wg.Done()
			<-start
			renameErr = tr.RenumberLine(context.Background(), lineID, oldNumber, newNumber, "test")
		}()
		close(start)
		wg.Wait()

		if callErr != nil && !errors.Is(callErr, calls.ErrLineChanged) {
			t.Fatalf("iteration %d call: %v", i, callErr)
		}
		if callErr != nil && renameErr != nil {
			t.Fatalf("iteration %d both operations failed: call=%v rename=%v", i, callErr, renameErr)
		}
		if renameErr == nil {
			var activeOld int
			if err := d.DB.QueryRow(`SELECT count(*) FROM calls WHERE (caller=$1 OR callee=$1) AND status IN ('initiated','ringing','connected')`, oldNumber).Scan(&activeOld); err != nil {
				t.Fatal(err)
			}
			if activeOld != 0 {
				t.Fatalf("iteration %d committed both renumber and old-number active call", i)
			}
		} else if !errors.Is(renameErr, calls.ErrLineBusy) && !errors.Is(renameErr, calls.ErrLineChanged) {
			t.Fatalf("iteration %d rename: %v", i, renameErr)
		}
		_, _ = d.DB.Exec(`UPDATE calls SET status='ended' WHERE caller=$1 OR callee=$1`, oldNumber)
	}
}

func TestRenumberWaitsForIdentityBoundDispatchLifetime(t *testing.T) {
	d := openTestDB(t)
	setRenumberEnabled(t, d, true)
	tr := calls.New(d.DB)
	store := line.NewStore(d.DB)
	lineID := seedRenumberLine(t, d, "7619001")

	dispatchEntered := make(chan struct{})
	releaseDispatch := make(chan struct{})
	dispatchDone := make(chan error, 1)
	go func() {
		dispatchDone <- store.WithRenumberReadFence(context.Background(), func(ctx context.Context) error {
			resolved, err := store.GetByNumber(ctx, "7619001")
			if err != nil {
				return err
			}
			if resolved.ID != lineID {
				return fmt.Errorf("resolved line %d, want %d", resolved.ID, lineID)
			}
			close(dispatchEntered)
			<-releaseDispatch
			return nil
		})
	}()
	<-dispatchEntered
	renameDone := make(chan error, 1)
	go func() {
		renameDone <- tr.RenumberLine(context.Background(), lineID, "7619001", "7619002", "test")
	}()
	select {
	case err := <-renameDone:
		t.Fatalf("renumber crossed active dispatch lifetime: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseDispatch)
	if err := <-dispatchDone; err != nil {
		t.Fatal(err)
	}
	if err := <-renameDone; err != nil {
		t.Fatal(err)
	}
}
