//go:build integration

package calls

import (
	"context"
	"testing"
	"time"

	"github.com/justinlindh/digits/server/internal/db"
)

func insertCall(t *testing.T, d *db.Database, caller, callee string) int64 {
	var id int64
	err := d.DB.QueryRow(
		"INSERT INTO calls (caller, callee, status) VALUES ($1,$2,'connected') RETURNING id",
		caller, callee,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert call: %v", err)
	}
	return id
}

func TestFlusherWritesLatestPerEndpoint(t *testing.T) {
	d := setupTestDB(t)
	s := NewHealthStore(d)
	callID := insertCall(t, d, "555-1111", "555-2222")
	s.Init(callID)
	loss1 := float32(0.1)
	loss2 := float32(0.2)
	loss3 := float32(0.3)
	s.Record(callID, "555-1111", Sample{TS: time.Now().Add(-2 * time.Second), LossPct: &loss1})
	s.Record(callID, "555-1111", Sample{TS: time.Now().Add(-1 * time.Second), LossPct: &loss2})
	s.Record(callID, "555-2222", Sample{TS: time.Now(), LossPct: &loss3})

	if err := s.FlushOnce(context.Background()); err != nil {
		t.Fatalf("FlushOnce: %v", err)
	}

	var n int
	if err := d.DB.QueryRow(
		"SELECT COUNT(*) FROM call_link_health WHERE call_id = $1", callID,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("row count: got %d want 2", n)
	}

	// Second flush with no new samples must write 0 rows.
	if err := s.FlushOnce(context.Background()); err != nil {
		t.Fatalf("second flush: %v", err)
	}
	if err := d.DB.QueryRow(
		"SELECT COUNT(*) FROM call_link_health WHERE call_id = $1", callID,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("no-new-sample flush wrote rows: got %d want 2", n)
	}
}

func TestReadbackFromDB(t *testing.T) {
	d := setupTestDB(t)
	s := NewHealthStore(d)
	callID := insertCall(t, d, "555-3333", "555-4444")
	s.Init(callID)
	loss := float32(0.5)
	s.Record(callID, "555-3333", Sample{TS: time.Now(), LossPct: &loss})
	if err := s.FlushOnce(context.Background()); err != nil {
		t.Fatalf("FlushOnce: %v", err)
	}

	// Simulate post-restart: drop in-memory state.
	s.Evict(callID)

	got, err := s.Readback(context.Background(), callID, "555-3333", ringCapacity)
	if err != nil {
		t.Fatalf("Readback: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("readback len: got %d want 1", len(got))
	}
	if got[0].LossPct == nil || *got[0].LossPct != 0.5 {
		t.Fatalf("readback value mismatch: %+v", got[0])
	}
}

func TestCallLinkHealthSchemaV20(t *testing.T) {
	d := setupTestDB(t)

	// conference_id and peer columns exist.
	var colCount int
	if err := d.DB.QueryRow(
		`SELECT COUNT(*) FROM information_schema.columns
         WHERE table_name = 'call_link_health'
           AND column_name IN ('conference_id', 'peer')`,
	).Scan(&colCount); err != nil {
		t.Fatalf("query columns: %v", err)
	}
	if colCount != 2 {
		t.Fatalf("expected 2 new columns (conference_id, peer); got %d", colCount)
	}

	// call_id is now nullable.
	var isNullable string
	if err := d.DB.QueryRow(
		`SELECT is_nullable FROM information_schema.columns
         WHERE table_name = 'call_link_health' AND column_name = 'call_id'`,
	).Scan(&isNullable); err != nil {
		t.Fatalf("query call_id nullability: %v", err)
	}
	if isNullable != "YES" {
		t.Fatalf("call_id should be nullable after v20; got %q", isNullable)
	}

	// XOR CHECK: both set should fail.
	_, err := d.DB.Exec(
		`INSERT INTO call_link_health (call_id, conference_id, endpoint, ts)
         VALUES ($1, $2, $3, NOW())`,
		1, "00000000-0000-0000-0000-000000000000", "+15555550001",
	)
	if err == nil {
		t.Fatal("expected CHECK violation inserting with both call_id and conference_id set")
	}

	// XOR CHECK: neither set should fail.
	_, err = d.DB.Exec(
		`INSERT INTO call_link_health (call_id, conference_id, endpoint, ts)
         VALUES (NULL, NULL, $1, NOW())`,
		"+15555550001",
	)
	if err == nil {
		t.Fatal("expected CHECK violation inserting with neither call_id nor conference_id set")
	}
}
