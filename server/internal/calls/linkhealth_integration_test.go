//go:build integration

package calls

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
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

func TestWriteSample2PartyPostV20(t *testing.T) {
	d := setupTestDB(t)

	// Seed a call row so the FK is satisfied.
	var callID int64
	if err := d.DB.QueryRow(
		`INSERT INTO calls (caller, callee, status) VALUES ($1, $2, 'initiated') RETURNING id`,
		"+15555550001", "+15555550002",
	).Scan(&callID); err != nil {
		t.Fatalf("seed call: %v", err)
	}

	s := NewHealthStore(d)
	loss := float32(1.5)
	sample := Sample{TS: time.Unix(0, 1), LossPct: &loss, ConnType: "host"}

	if err := s.writeSample(context.Background(),
		SessionKey{CallID: callID},
		endpointKey{From: "+15555550001"},
		sample,
	); err != nil {
		t.Fatalf("writeSample: %v", err)
	}

	var (
		dbCallID sql.NullInt64
		dbConfID sql.NullString
		dbEp     string
		dbPeer   sql.NullString
	)
	if err := d.DB.QueryRow(
		`SELECT call_id, conference_id, endpoint, peer FROM call_link_health
		 WHERE call_id = $1`, callID,
	).Scan(&dbCallID, &dbConfID, &dbEp, &dbPeer); err != nil {
		t.Fatalf("readback row: %v", err)
	}
	if !dbCallID.Valid || dbCallID.Int64 != callID {
		t.Fatalf("call_id not persisted correctly: %v", dbCallID)
	}
	if dbConfID.Valid {
		t.Fatalf("conference_id should be NULL for 2-party rows; got %v", dbConfID)
	}
	if dbPeer.Valid {
		t.Fatalf("peer should be NULL for 2-party rows; got %v", dbPeer)
	}
	if dbEp != "+15555550001" {
		t.Fatalf("endpoint: got %q want +15555550001", dbEp)
	}

	// Idempotent: second write of the same (call, endpoint, ts) is silently skipped.
	if err := s.writeSample(context.Background(),
		SessionKey{CallID: callID},
		endpointKey{From: "+15555550001"},
		sample,
	); err != nil {
		t.Fatalf("second writeSample: %v", err)
	}
	var rowCount int
	if err := d.DB.QueryRow(
		`SELECT COUNT(*) FROM call_link_health WHERE call_id = $1`, callID,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("ON CONFLICT DO NOTHING broken; got %d rows want 1", rowCount)
	}
}

func TestWriteSampleConferencePostV20(t *testing.T) {
	d := setupTestDB(t)

	confID := uuid.New()
	var originatingCallID int64
	if err := d.DB.QueryRow(
		`INSERT INTO calls (caller, callee, status) VALUES ($1, $2, 'initiated') RETURNING id`,
		"+15555550001", "+15555550002",
	).Scan(&originatingCallID); err != nil {
		t.Fatalf("seed originating call: %v", err)
	}
	if _, err := d.DB.Exec(
		`INSERT INTO conferences (id, host_phone, originating_call_id, state) VALUES ($1, $2, $3, 'active')`,
		confID, "+15555550001", originatingCallID,
	); err != nil {
		t.Fatalf("seed conference: %v", err)
	}

	s := NewHealthStore(d)
	jitter := float32(4.2)
	sample := Sample{TS: time.Unix(0, 1), JitterMs: &jitter}

	if err := s.writeSample(context.Background(),
		SessionKey{ConfID: confID},
		endpointKey{From: "+15555550001", Peer: "+15555550002"},
		sample,
	); err != nil {
		t.Fatalf("writeSample conference: %v", err)
	}

	var (
		dbCallID sql.NullInt64
		dbConfID sql.NullString
		dbEp     string
		dbPeer   sql.NullString
	)
	if err := d.DB.QueryRow(
		`SELECT call_id, conference_id, endpoint, peer FROM call_link_health
		 WHERE conference_id = $1`, confID,
	).Scan(&dbCallID, &dbConfID, &dbEp, &dbPeer); err != nil {
		t.Fatalf("readback row: %v", err)
	}
	if dbCallID.Valid {
		t.Fatalf("call_id should be NULL for conference rows; got %v", dbCallID)
	}
	if !dbConfID.Valid || dbConfID.String != confID.String() {
		t.Fatalf("conference_id: got %v want %s", dbConfID, confID)
	}
	if !dbPeer.Valid || dbPeer.String != "+15555550002" {
		t.Fatalf("peer: got %v want +15555550002", dbPeer)
	}
	if dbEp != "+15555550001" {
		t.Fatalf("endpoint: got %q want +15555550001", dbEp)
	}

	// Cascade delete: dropping the conference wipes the sample row.
	if _, err := d.DB.Exec(`DELETE FROM conferences WHERE id = $1`, confID); err != nil {
		t.Fatalf("delete conference: %v", err)
	}
	var rowCount int
	if err := d.DB.QueryRow(
		`SELECT COUNT(*) FROM call_link_health WHERE conference_id = $1`, confID,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count rows after cascade: %v", err)
	}
	if rowCount != 0 {
		t.Fatalf("cascade delete broken; got %d rows want 0", rowCount)
	}
}

func TestWriteSampleConferenceIdempotent(t *testing.T) {
	d := setupTestDB(t)

	confID := uuid.New()
	var originatingCallID int64
	if err := d.DB.QueryRow(
		`INSERT INTO calls (caller, callee, status) VALUES ($1, $2, 'initiated') RETURNING id`,
		"+15555550001", "+15555550002",
	).Scan(&originatingCallID); err != nil {
		t.Fatalf("seed originating call: %v", err)
	}
	if _, err := d.DB.Exec(
		`INSERT INTO conferences (id, host_phone, originating_call_id, state) VALUES ($1, $2, $3, 'active')`,
		confID, "+15555550001", originatingCallID,
	); err != nil {
		t.Fatalf("seed conference: %v", err)
	}

	s := NewHealthStore(d)
	loss := float32(2.0)
	sample := Sample{TS: time.Unix(0, 1), LossPct: &loss}

	for i := 0; i < 2; i++ {
		if err := s.writeSample(context.Background(),
			SessionKey{ConfID: confID},
			endpointKey{From: "+15555550001", Peer: "+15555550002"},
			sample,
		); err != nil {
			t.Fatalf("writeSample iteration %d: %v", i, err)
		}
	}

	var rowCount int
	if err := d.DB.QueryRow(
		`SELECT COUNT(*) FROM call_link_health WHERE conference_id = $1`, confID,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("conference ON CONFLICT DO NOTHING broken; got %d rows want 1", rowCount)
	}
}

func TestWriteSampleConferenceRejectsEmptyPeer(t *testing.T) {
	d := setupTestDB(t)

	confID := uuid.New()
	var originatingCallID int64
	if err := d.DB.QueryRow(
		`INSERT INTO calls (caller, callee, status) VALUES ($1, $2, 'initiated') RETURNING id`,
		"+15555550001", "+15555550002",
	).Scan(&originatingCallID); err != nil {
		t.Fatalf("seed originating call: %v", err)
	}
	if _, err := d.DB.Exec(
		`INSERT INTO conferences (id, host_phone, originating_call_id, state) VALUES ($1, $2, $3, 'active')`,
		confID, "+15555550001", originatingCallID,
	); err != nil {
		t.Fatalf("seed conference: %v", err)
	}

	s := NewHealthStore(d)
	err := s.writeSample(context.Background(),
		SessionKey{ConfID: confID},
		endpointKey{From: "+15555550001", Peer: ""},
		Sample{TS: time.Unix(0, 1)},
	)
	if err == nil {
		t.Fatal("writeSample with empty Peer on conference key must return an error")
	}

	// No row should have been inserted.
	var rowCount int
	if err := d.DB.QueryRow(
		`SELECT COUNT(*) FROM call_link_health WHERE conference_id = $1`, confID,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rowCount != 0 {
		t.Fatalf("no row should be inserted when empty-peer rejected; got %d", rowCount)
	}
}

func TestFlushOnceWritesConferenceRows(t *testing.T) {
	d := setupTestDB(t)

	confID := uuid.New()
	var originatingCallID int64
	if err := d.DB.QueryRow(
		`INSERT INTO calls (caller, callee, status) VALUES ($1, $2, 'initiated') RETURNING id`,
		"+15555550001", "+15555550002",
	).Scan(&originatingCallID); err != nil {
		t.Fatalf("seed call: %v", err)
	}
	if _, err := d.DB.Exec(
		`INSERT INTO conferences (id, host_phone, originating_call_id, state) VALUES ($1, $2, $3, 'active')`,
		confID, "+15555550001", originatingCallID,
	); err != nil {
		t.Fatalf("seed conference: %v", err)
	}

	s := NewHealthStore(d)
	s.InitConference(confID)

	loss := float32(1.1)
	s.RecordEdge(confID, "+15555550001", "+15555550002",
		Sample{TS: time.Unix(0, 1000), LossPct: &loss, ConnType: "host"})
	jitter := float32(2.2)
	s.RecordEdge(confID, "+15555550002", "+15555550001",
		Sample{TS: time.Unix(0, 2000), JitterMs: &jitter, ConnType: "srflx"})

	if err := s.FlushOnce(context.Background()); err != nil {
		t.Fatalf("FlushOnce: %v", err)
	}

	var count int
	if err := d.DB.QueryRow(
		`SELECT COUNT(*) FROM call_link_health WHERE conference_id = $1`, confID,
	).Scan(&count); err != nil {
		t.Fatalf("count conference rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 conference rows after flush; got %d", count)
	}

	// Sanity: both edges land with correct (endpoint, peer) pairs.
	rows, err := d.DB.Query(
		`SELECT endpoint, peer FROM call_link_health
		 WHERE conference_id = $1 ORDER BY ts`, confID,
	)
	if err != nil {
		t.Fatalf("select conference rows: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var ep, peer string
		if err := rows.Scan(&ep, &peer); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, ep+"|"+peer)
	}
	want := []string{"+15555550001|+15555550002", "+15555550002|+15555550001"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("edges: got %v want %v", got, want)
	}
}
