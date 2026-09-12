//go:build integration

package admin

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/justinlindh/digits/server/internal/db"
)

func testStore(t *testing.T) (*Store, *db.Database) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set, skipping DB tests")
	}
	database, err := db.Open(dsn)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	// CASCADE follows every FK (calls and conferences reference each other),
	// and a failure is fatal: leaked rows would skew the counts asserted here.
	cleanup := func() {
		if _, err := database.DB.Exec("TRUNCATE users, households, lines, devices, calls, conferences CASCADE"); err != nil {
			t.Fatalf("cleanup: %v", err)
		}
	}
	cleanup()
	t.Cleanup(func() {
		cleanup()
		_ = database.Close()
	})
	return NewStore(database.DB), database
}

func mustExec(t *testing.T, database *db.Database, q string, args ...any) {
	t.Helper()
	if _, err := database.DB.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func mustScan(t *testing.T, database *db.Database, q string, args ...any) string {
	t.Helper()
	var id string
	if err := database.DB.QueryRow(q, args...).Scan(&id); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return id
}

// seedFixture builds two households. Alpha has two members, two lines with one
// paired device and one unpaired device, and three calls inside the window
// plus one before it. Beta has one member, one line, no devices, and one call
// in the window whose callee is an alpha line (so it counts for both).
func seedFixture(t *testing.T, database *db.Database, now time.Time) {
	t.Helper()
	alice := mustScan(t, database, `INSERT INTO users (email, name, created_at, last_login_at) VALUES ('alice@example.com', 'Alice', $1, $2) RETURNING id`, now.Add(-72*time.Hour), now.Add(-time.Hour))
	bob := mustScan(t, database, `INSERT INTO users (email, name, created_at) VALUES ('bob@example.com', 'Bob', $1) RETURNING id`, now.Add(-48*time.Hour))
	carol := mustScan(t, database, `INSERT INTO users (email, name, created_at, disabled_at) VALUES ('carol@example.com', 'Carol', $1, $2) RETURNING id`, now.Add(-24*time.Hour), now.Add(-time.Hour))

	alpha := mustScan(t, database, `INSERT INTO households (name, created_at) VALUES ('Alpha', $1) RETURNING id`, now.Add(-72*time.Hour))
	beta := mustScan(t, database, `INSERT INTO households (name, created_at) VALUES ('Beta', $1) RETURNING id`, now.Add(-24*time.Hour))
	mustExec(t, database, `INSERT INTO household_members (user_id, household_id) VALUES ($1, $2), ($3, $2), ($4, $5)`, alice, alpha, bob, carol, beta)

	l1 := mustScan(t, database, `INSERT INTO lines (number, name, household_id) VALUES ('1000001', 'Kitchen', $1) RETURNING id`, alpha)
	l2 := mustScan(t, database, `INSERT INTO lines (number, name, household_id) VALUES ('1000002', 'Den', $1) RETURNING id`, alpha)
	mustExec(t, database, `INSERT INTO lines (number, name, household_id) VALUES ('2000001', 'Hall', $1)`, beta)

	mustExec(t, database, `INSERT INTO devices (line_id, hardware_id, paired_at) VALUES ($1, 'hw-1', $2)`, l1, now.Add(-time.Hour))
	mustExec(t, database, `INSERT INTO devices (line_id, hardware_id) VALUES ($1, 'hw-2')`, l2)

	// Alpha-internal calls: two today, one yesterday, one eight days ago.
	mustExec(t, database, `INSERT INTO calls (caller, callee, status, started_at, answered_at, duration_s) VALUES
		('1000001', '1000002', 'ended', $1, $1, 30),
		('1000002', '1000001', 'ended', $2, $2, 45),
		('1000001', '1000002', 'missed', $3, NULL, 0),
		('1000001', '1000002', 'ended', $4, $4, 10)`,
		now.Add(-time.Hour), now.Add(-2*time.Hour), now.Add(-26*time.Hour), now.Add(-8*24*time.Hour))
	// Cross-household call: beta calls alpha.
	mustExec(t, database, `INSERT INTO calls (caller, callee, status, started_at, answered_at, duration_s) VALUES ('2000001', '1000001', 'ended', $1, $1, 60)`, now.Add(-3*time.Hour))
}

func TestTotals(t *testing.T) {
	s, database := testStore(t)
	now := time.Now()
	seedFixture(t, database, now)

	got, err := s.Totals(context.Background(), now.Add(-7*24*time.Hour))
	if err != nil {
		t.Fatalf("Totals: %v", err)
	}
	want := Totals{Users: 3, Households: 2, Lines: 3, PairedDevices: 1, Calls: 4}
	if got != want {
		t.Errorf("Totals = %+v, want %+v", got, want)
	}
}

func TestAccounts(t *testing.T) {
	s, database := testStore(t)
	now := time.Now()
	seedFixture(t, database, now)

	got, err := s.Accounts(context.Background())
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d accounts, want 3", len(got))
	}
	// Newest first.
	if got[0].Email != "carol@example.com" || got[2].Email != "alice@example.com" {
		t.Errorf("order = [%s %s %s], want newest first", got[0].Email, got[1].Email, got[2].Email)
	}
	alice := got[2]
	if alice.Name != "Alice" || alice.LastLoginAt == nil {
		t.Errorf("alice = %+v, want Name=Alice and LastLoginAt set", alice)
	}
	if len(alice.Households) != 1 || alice.Households[0] != "Alpha" {
		t.Errorf("alice.Households = %v, want [Alpha]", alice.Households)
	}
	if got[1].LastLoginAt != nil {
		t.Errorf("bob.LastLoginAt = %v, want nil", got[1].LastLoginAt)
	}
	if got[0].DisabledAt == nil {
		t.Error("carol.DisabledAt = nil, want the fixture's timestamp")
	}
	if alice.DisabledAt != nil {
		t.Errorf("alice.DisabledAt = %v, want nil", alice.DisabledAt)
	}
}

func TestHouseholds(t *testing.T) {
	s, database := testStore(t)
	now := time.Now()
	seedFixture(t, database, now)

	got, err := s.Households(context.Background(), now.Add(-7*24*time.Hour))
	if err != nil {
		t.Fatalf("Households: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d households, want 2", len(got))
	}
	alpha, beta := got[0], got[1]
	if alpha.Name != "Alpha" || beta.Name != "Beta" {
		t.Fatalf("order = [%s %s], want oldest first", alpha.Name, beta.Name)
	}
	if len(alpha.Members) != 2 || alpha.Members[0] != "alice@example.com" || alpha.Members[1] != "bob@example.com" {
		t.Errorf("alpha.Members = %v", alpha.Members)
	}
	if len(alpha.Lines) != 2 || alpha.Lines[0] != "1000001" || alpha.Lines[1] != "1000002" {
		t.Errorf("alpha.Lines = %v", alpha.Lines)
	}
	if alpha.PairedDevices != 1 || alpha.Calls != 4 {
		t.Errorf("alpha devices=%d calls=%d, want 1 and 4", alpha.PairedDevices, alpha.Calls)
	}
	if len(beta.Members) != 1 || len(beta.Lines) != 1 || beta.PairedDevices != 0 || beta.Calls != 1 {
		t.Errorf("beta = %+v", beta)
	}
}

func TestCallsPerDay(t *testing.T) {
	s, database := testStore(t)
	// Fix the clock mid-day so the -26h call lands on the previous calendar day
	// in UTC and none of the offsets straddle midnight.
	now := time.Date(2030, 1, 10, 12, 0, 0, 0, time.UTC)
	seedFixture(t, database, now)

	got, err := s.CallsPerDay(context.Background(), now.Add(-7*24*time.Hour), now, time.UTC)
	if err != nil {
		t.Fatalf("CallsPerDay: %v", err)
	}
	if len(got) != 8 {
		t.Fatalf("got %d days, want 8 (window start through today inclusive)", len(got))
	}
	today := got[len(got)-1]
	if !today.Day.Equal(time.Date(2030, 1, 10, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("last day = %v, want 2030-01-10", today.Day)
	}
	if today.Calls != 3 || today.Answered != 3 || today.DurationS != 135 {
		t.Errorf("today = %+v, want 3 calls, 3 answered, 135s", today)
	}
	yesterday := got[len(got)-2]
	if yesterday.Calls != 1 || yesterday.Answered != 0 {
		t.Errorf("yesterday = %+v, want 1 call, 0 answered", yesterday)
	}
	if got[0].Calls != 0 {
		t.Errorf("window start = %+v, want zero calls", got[0])
	}
}
