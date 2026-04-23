//go:build integration

package calls_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/justinlindh/digits/server/internal/calls"
)

type fakeHealthLifecycle struct {
	inits      []int64
	evicts     []int64
	confInits  []uuid.UUID
	confEvicts []uuid.UUID
}

func (f *fakeHealthLifecycle) Init(id int64)                { f.inits = append(f.inits, id) }
func (f *fakeHealthLifecycle) Evict(id int64)               { f.evicts = append(f.evicts, id) }
func (f *fakeHealthLifecycle) InitConference(id uuid.UUID)  { f.confInits = append(f.confInits, id) }
func (f *fakeHealthLifecycle) EvictConference(id uuid.UUID) { f.confEvicts = append(f.confEvicts, id) }

func TestTrackerBusyWithConference_Integration(t *testing.T) {
	d := openTestDB(t)

	tr := calls.New(d)
	id, err := tr.OnCallInitiated(context.Background(), "5550001", "5550002")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	if !tr.Busy(context.Background(), "5550001") {
		t.Fatalf("expected 5550001 busy from 2-party call")
	}

	// End the 2-party call so 5550001 is no longer in tr.active, then
	// put 5550001 in a conference. tr.Busy should still return true.
	if err := tr.OnCallEnded(context.Background(), "5550001", "5550002"); err != nil {
		t.Fatalf("OnCallEnded: %v", err)
	}
	if tr.Busy(context.Background(), "5550001") {
		t.Fatalf("expected 5550001 NOT busy after call ended")
	}

	_, err = tr.Conferences().CreateConference("5550001", id, []string{"5550010", "5550011"})
	if err != nil {
		t.Fatalf("CreateConference: %v", err)
	}
	if !tr.Busy(context.Background(), "5550001") {
		t.Fatalf("expected 5550001 busy via conference")
	}
	if !tr.Busy(context.Background(), "5550010") {
		t.Fatalf("expected 5550010 busy via conference")
	}
	if tr.Busy(context.Background(), "5550099") {
		t.Fatalf("unexpected busy for 5550099")
	}
}

func TestConferencePersistence_Integration(t *testing.T) {
	d := openTestDB(t)

	tr := calls.New(d)
	callID, err := tr.OnCallInitiated(context.Background(), "5550010", "5550011")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	// Simulate the add-leg: host dials the third party while B is on hold.
	if _, err := tr.OnCallInitiated(context.Background(), "5550010", "5550012"); err != nil {
		t.Fatalf("OnCallInitiated add-leg: %v", err)
	}

	conf, err := tr.CreateConferencePersistent(context.Background(), "5550010", callID, []string{"5550011", "5550012"})
	if err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}

	var dbHost string
	err = d.DB.QueryRow("SELECT host_phone FROM conferences WHERE id = $1", conf.ID).Scan(&dbHost)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if dbHost != "5550010" {
		t.Fatalf("expected host 5550010, got %s", dbHost)
	}

	var memberCount int
	err = d.DB.QueryRow("SELECT COUNT(*) FROM conference_members WHERE conference_id = $1", conf.ID).Scan(&memberCount)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if memberCount != 3 {
		t.Fatalf("expected 3 members persisted, got %d", memberCount)
	}

	// End it
	if err := tr.EndConferencePersistent(context.Background(), conf.ID, "test_end"); err != nil {
		t.Fatalf("EndConferencePersistent: %v", err)
	}
	var state, endReason string
	err = d.DB.QueryRow("SELECT state, end_reason FROM conferences WHERE id = $1", conf.ID).Scan(&state, &endReason)
	if err != nil {
		t.Fatalf("select after end: %v", err)
	}
	if state != "ended" || endReason != "test_end" {
		t.Fatalf("expected state=ended end_reason=test_end, got state=%s end_reason=%s", state, endReason)
	}
}

func TestDropMemberCreatesContinuationCall_Integration(t *testing.T) {
	d := openTestDB(t)

	tr := calls.New(d)
	callID, _ := tr.OnCallInitiated(context.Background(), "5550100", "5550101")
	// Simulate add-leg before merge.
	_, _ = tr.OnCallInitiated(context.Background(), "5550100", "5550102")
	conf, _ := tr.CreateConferencePersistent(context.Background(), "5550100", callID, []string{"5550101", "5550102"})

	remaining, ended, err := tr.DropMemberPersistent(context.Background(), conf.ID, "5550101", "hangup")
	if err != nil {
		t.Fatalf("DropMemberPersistent: %v", err)
	}
	if !ended {
		t.Fatalf("expected conference ended")
	}
	if len(remaining) != 2 {
		t.Fatalf("expected 2 remaining, got %d", len(remaining))
	}

	// A new calls row must exist with originating_conference_id set and status = 'connected'
	var count int
	err = d.DB.QueryRow(
		`SELECT COUNT(*) FROM calls WHERE originating_conference_id = $1 AND status = 'connected'`,
		conf.ID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count continuation calls: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 continuation calls row, got %d", count)
	}

	// Verify surviving members are busy via tr.active and Tracker.Busy reports them so.
	for _, p := range remaining {
		if !tr.Busy(context.Background(), p) {
			t.Fatalf("expected surviving member %s to be busy after continuation", p)
		}
	}
	// The dropped member is not busy.
	if tr.Busy(context.Background(), "5550101") {
		t.Fatalf("dropped member should not be busy")
	}
}

func TestConferenceLifecycleHooks(t *testing.T) {
	d := openTestDB(t)
	tr := calls.New(d)
	h := &fakeHealthLifecycle{}
	tr.SetHealthStore(h)

	ctx := context.Background()
	hostNum := "+15555550001"
	m2 := "+15555550002"
	m3 := "+15555550003"

	// Seed two 2-party calls between the host and the two added members.
	for _, callee := range []string{m2, m3} {
		if _, err := tr.OnCallInitiated(ctx, hostNum, callee); err != nil {
			t.Fatalf("OnCallInitiated %s->%s: %v", hostNum, callee, err)
		}
		if err := tr.OnCallAnswered(ctx, hostNum, callee); err != nil {
			t.Fatalf("OnCallAnswered %s->%s: %v", hostNum, callee, err)
		}
	}

	originatingCallID := tr.CallIDForPair(ctx, hostNum, m2)
	if originatingCallID == 0 {
		t.Fatal("CallIDForPair returned 0 for originating pair")
	}

	// Seed fired 2-party Init twice; confirm before creating the conference
	// so a regression that swapped InitConference with Init is catchable.
	if len(h.inits) != 2 {
		t.Fatalf("expected 2 Init calls from seeding; got %d", len(h.inits))
	}

	conf, err := tr.CreateConferencePersistent(ctx, hostNum, originatingCallID, []string{m2, m3})
	if err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}

	// CreateConferencePersistent must NOT have incremented Init (2-party
	// hooks stay separate from conference hooks).
	if len(h.inits) != 2 {
		t.Fatalf("CreateConferencePersistent should not fire Init; got %d total Init calls", len(h.inits))
	}

	if len(h.confInits) != 1 || h.confInits[0] != conf.ID {
		t.Fatalf("InitConference calls: got %v want [%s]", h.confInits, conf.ID)
	}

	if err := tr.EndConferencePersistent(ctx, conf.ID, "host_hangup"); err != nil {
		t.Fatalf("EndConferencePersistent: %v", err)
	}

	if len(h.confEvicts) != 1 || h.confEvicts[0] != conf.ID {
		t.Fatalf("EvictConference calls: got %v want [%s]", h.confEvicts, conf.ID)
	}
}

func TestDropMemberFiresEvictConference(t *testing.T) {
	d := openTestDB(t)
	tr := calls.New(d)
	h := &fakeHealthLifecycle{}
	tr.SetHealthStore(h)

	ctx := context.Background()
	hostNum := "+15555550001"
	m2 := "+15555550002"
	m3 := "+15555550003"

	for _, callee := range []string{m2, m3} {
		if _, err := tr.OnCallInitiated(ctx, hostNum, callee); err != nil {
			t.Fatalf("OnCallInitiated: %v", err)
		}
		if err := tr.OnCallAnswered(ctx, hostNum, callee); err != nil {
			t.Fatalf("OnCallAnswered: %v", err)
		}
	}
	originatingCallID := tr.CallIDForPair(ctx, hostNum, m2)
	conf, err := tr.CreateConferencePersistent(ctx, hostNum, originatingCallID, []string{m2, m3})
	if err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}

	_, ended, err := tr.DropMemberPersistent(ctx, conf.ID, m3, "kick")
	if err != nil {
		t.Fatalf("DropMemberPersistent: %v", err)
	}
	if !ended {
		t.Fatal("v1 DropMember should end the conference (3 -> 2 terminates)")
	}

	if len(h.confEvicts) != 1 || h.confEvicts[0] != conf.ID {
		t.Fatalf("EvictConference from DropMember: got %v want [%s]", h.confEvicts, conf.ID)
	}
}

func TestCreateConferenceEvictsActiveEntries_Integration(t *testing.T) {
	d := openTestDB(t)

	tr := calls.New(d)
	callID, err := tr.OnCallInitiated(context.Background(), "5550200", "5550201")
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}

	// Pre-condition: 2-party call is active, both phones busy, InCall true.
	if !tr.InCall(context.Background(), "5550200", "5550201") {
		t.Fatalf("expected InCall true before conference")
	}
	for _, p := range []string{"5550200", "5550201"} {
		if !tr.Busy(context.Background(), p) {
			t.Fatalf("expected %s busy before conference", p)
		}
	}

	// Add a second active call so we can verify only conference-related entries get evicted.
	if _, err := tr.OnCallInitiated(context.Background(), "5550300", "5550301"); err != nil {
		t.Fatalf("second OnCallInitiated: %v", err)
	}

	// Simulate add-leg: host dials 5550202 while 5550201 is on hold.
	if _, err := tr.OnCallInitiated(context.Background(), "5550200", "5550202"); err != nil {
		t.Fatalf("OnCallInitiated add-leg: %v", err)
	}

	_, err = tr.CreateConferencePersistent(context.Background(), "5550200", callID, []string{"5550201", "5550202"})
	if err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}

	// After merge: the 2-party A<->B entry must be gone from the active map.
	if tr.InCall(context.Background(), "5550200", "5550201") {
		t.Fatalf("expected 2-party A<->B entry evicted from active map")
	}
	// Unrelated 2-party call must still be present.
	if !tr.InCall(context.Background(), "5550300", "5550301") {
		t.Fatalf("expected unrelated 2-party call to survive")
	}
	// All three conference members should be Busy (via conference).
	for _, p := range []string{"5550200", "5550201", "5550202"} {
		if !tr.Busy(context.Background(), p) {
			t.Fatalf("expected conference member %s busy", p)
		}
	}
}

func TestConferenceKicksSchemaV22_Integration(t *testing.T) {
	d := openTestDB(t)

	rows, err := d.DB.Query(`SELECT column_name, data_type, is_nullable
		FROM information_schema.columns WHERE table_name = 'conference_kicks'
		ORDER BY ordinal_position`)
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	defer func() { _ = rows.Close() }()

	type col struct{ name, dataType, nullable string }
	var got []col
	for rows.Next() {
		var c col
		if err := rows.Scan(&c.name, &c.dataType, &c.nullable); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("conference_kicks table not found")
	}

	// Cascade delete: dropping a conference wipes its kicks.
	confID := uuid.New()
	var originatingCallID int64
	if err := d.DB.QueryRow(
		`INSERT INTO calls (caller, callee, status) VALUES ($1, $2, 'initiated') RETURNING id`,
		"+15555550001", "+15555550002",
	).Scan(&originatingCallID); err != nil {
		t.Fatalf("seed call: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.DB.Exec("DELETE FROM calls WHERE id = $1", originatingCallID)
	})
	if _, err := d.DB.Exec(
		`INSERT INTO conferences (id, host_phone, originating_call_id, state) VALUES ($1, $2, $3, 'active')`,
		confID, "+15555550001", originatingCallID,
	); err != nil {
		t.Fatalf("seed conference: %v", err)
	}
	var hostUserID string
	if err := d.DB.QueryRow(
		`INSERT INTO users (email, name) VALUES ($1, $2) RETURNING id`,
		"kick-test-"+confID.String()+"@example.com", "Kick Test",
	).Scan(&hostUserID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.DB.Exec("DELETE FROM users WHERE id = $1", hostUserID)
	})

	if _, err := d.DB.Exec(
		`INSERT INTO conference_kicks (conference_id, kicked_phone, kicked_by_user_id) VALUES ($1, $2, $3)`,
		confID, "+15555550002", hostUserID,
	); err != nil {
		t.Fatalf("insert kick row: %v", err)
	}
	var count int
	if err := d.DB.QueryRow(`SELECT COUNT(*) FROM conference_kicks WHERE conference_id = $1`, confID).Scan(&count); err != nil {
		t.Fatalf("count kicks: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 kick row; got %d", count)
	}
	if _, err := d.DB.Exec(`DELETE FROM conferences WHERE id = $1`, confID); err != nil {
		t.Fatalf("delete conference: %v", err)
	}
	if err := d.DB.QueryRow(`SELECT COUNT(*) FROM conference_kicks WHERE conference_id = $1`, confID).Scan(&count); err != nil {
		t.Fatalf("count kicks after cascade: %v", err)
	}
	if count != 0 {
		t.Fatalf("cascade delete broken; got %d rows want 0", count)
	}
}

func TestRecordKick_Integration(t *testing.T) {
	d := openTestDB(t)
	tr := calls.New(d)
	ctx := context.Background()

	if _, err := tr.OnCallInitiated(ctx, "+15555550101", "+15555550102"); err != nil {
		t.Fatalf("seed call 1: %v", err)
	}
	if _, err := tr.OnCallInitiated(ctx, "+15555550101", "+15555550103"); err != nil {
		t.Fatalf("seed call 2: %v", err)
	}
	_ = tr.OnCallAnswered(ctx, "+15555550101", "+15555550102")
	_ = tr.OnCallAnswered(ctx, "+15555550101", "+15555550103")
	originatingCallID := tr.CallIDForPair(ctx, "+15555550101", "+15555550102")
	conf, err := tr.CreateConferencePersistent(ctx, "+15555550101", originatingCallID, []string{"+15555550102", "+15555550103"})
	if err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}

	var hostUserID string
	email := "recordkick-" + conf.ID.String() + "@example.com"
	if err := d.DB.QueryRow(
		`INSERT INTO users (email, name) VALUES ($1, $2) RETURNING id`,
		email, "Host",
	).Scan(&hostUserID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = d.DB.Exec("DELETE FROM users WHERE id = $1", hostUserID) })

	if err := tr.RecordKick(ctx, conf.ID, "+15555550103", hostUserID); err != nil {
		t.Fatalf("RecordKick: %v", err)
	}

	var (
		dbConfID     string
		dbKickedPh   string
		dbKickedByID string
	)
	if err := d.DB.QueryRow(
		`SELECT conference_id, kicked_phone, kicked_by_user_id FROM conference_kicks WHERE conference_id = $1`,
		conf.ID,
	).Scan(&dbConfID, &dbKickedPh, &dbKickedByID); err != nil {
		t.Fatalf("readback kick row: %v", err)
	}
	if dbConfID != conf.ID.String() {
		t.Errorf("conference_id: got %s want %s", dbConfID, conf.ID)
	}
	if dbKickedPh != "+15555550103" {
		t.Errorf("kicked_phone: got %s want +15555550103", dbKickedPh)
	}
	if dbKickedByID != hostUserID {
		t.Errorf("kicked_by_user_id: got %s want %s", dbKickedByID, hostUserID)
	}
}

func TestGetConferenceByID_Integration(t *testing.T) {
	d := openTestDB(t)
	tr := calls.New(d)
	ctx := context.Background()

	// Seed two calls, create a conference.
	if _, err := tr.OnCallInitiated(ctx, "+15555550001", "+15555550002"); err != nil {
		t.Fatalf("seed call 1: %v", err)
	}
	if _, err := tr.OnCallInitiated(ctx, "+15555550001", "+15555550003"); err != nil {
		t.Fatalf("seed call 2: %v", err)
	}
	if err := tr.OnCallAnswered(ctx, "+15555550001", "+15555550002"); err != nil {
		t.Fatalf("answer call 1: %v", err)
	}
	if err := tr.OnCallAnswered(ctx, "+15555550001", "+15555550003"); err != nil {
		t.Fatalf("answer call 2: %v", err)
	}
	originatingCallID := tr.CallIDForPair(ctx, "+15555550001", "+15555550002")
	conf, err := tr.CreateConferencePersistent(ctx, "+15555550001", originatingCallID, []string{"+15555550002", "+15555550003"})
	if err != nil {
		t.Fatalf("create conference: %v", err)
	}

	// Active conference fetch.
	got, err := tr.GetConferenceByID(ctx, conf.ID)
	if err != nil {
		t.Fatalf("GetConferenceByID: %v", err)
	}
	if got == nil {
		t.Fatal("got nil conference")
	}
	if got.ID != conf.ID {
		t.Fatalf("ID: got %s want %s", got.ID, conf.ID)
	}
	if got.Host != "+15555550001" {
		t.Fatalf("Host: got %s want +15555550001", got.Host)
	}
	if len(got.Members) != 3 {
		t.Fatalf("Members len: got %d want 3", len(got.Members))
	}
	// Host sorts first, added members alphabetically.
	if got.Members[0] != "+15555550001" {
		t.Fatalf("Members[0]: got %s want host", got.Members[0])
	}

	// Unknown ID returns (nil, nil).
	got, err = tr.GetConferenceByID(ctx, uuid.New())
	if err != nil {
		t.Fatalf("GetConferenceByID unknown: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for unknown id; got %v", got)
	}

	// Ended conference is still retrievable.
	if err := tr.EndConferencePersistent(ctx, conf.ID, "host_hangup"); err != nil {
		t.Fatalf("EndConferencePersistent: %v", err)
	}
	got, err = tr.GetConferenceByID(ctx, conf.ID)
	if err != nil {
		t.Fatalf("GetConferenceByID after end: %v", err)
	}
	if got == nil {
		t.Fatal("ended conference not retrievable")
	}
	if got.EndReason != "host_hangup" {
		t.Fatalf("EndReason: got %q want host_hangup", got.EndReason)
	}
	if got.EndedAt == nil {
		t.Fatal("EndedAt should be populated on an ended conference")
	}
}
