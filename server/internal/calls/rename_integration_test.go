//go:build integration

package calls_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/justinlindh/digits/server/internal/calls"
)

// TestRenameNumber_Integration verifies that RenameNumber rewrites a phone
// number across every table its transaction touches: calls.caller,
// calls.callee, conferences.host_phone, conference_members.phone, and
// conference_kicks.kicked_phone. A missed table or a wrong column would
// silently strand call history under the old number, so each one is asserted
// explicitly.
func TestRenameNumber_Integration(t *testing.T) {
	d := openTestDB(t)
	tr := calls.New(d.DB)
	ctx := context.Background()

	const (
		oldNum = "+15557770001"
		newNum = "+15557770099"
		peer   = "+15557770002"
	)

	// calls.caller = old (outbound leg) and calls.callee = old (inbound leg).
	var outboundID, inboundID int64
	if err := d.DB.QueryRow(
		`INSERT INTO calls (caller, callee, status) VALUES ($1, $2, 'initiated') RETURNING id`,
		oldNum, peer,
	).Scan(&outboundID); err != nil {
		t.Fatalf("seed outbound call: %v", err)
	}
	if err := d.DB.QueryRow(
		`INSERT INTO calls (caller, callee, status) VALUES ($1, $2, 'initiated') RETURNING id`,
		peer, oldNum,
	).Scan(&inboundID); err != nil {
		t.Fatalf("seed inbound call: %v", err)
	}

	// conferences.host_phone = old.
	confID := uuid.New()
	if _, err := d.DB.Exec(
		`INSERT INTO conferences (id, host_phone, originating_call_id, state) VALUES ($1, $2, $3, 'active')`,
		confID, oldNum, outboundID,
	); err != nil {
		t.Fatalf("seed conference: %v", err)
	}

	// conference_members.phone = old.
	if _, err := d.DB.Exec(
		`INSERT INTO conference_members (conference_id, phone, role) VALUES ($1, $2, 'host')`,
		confID, oldNum,
	); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	// conference_kicks.kicked_phone = old (needs a user for the FK).
	var userID string
	if err := d.DB.QueryRow(
		`INSERT INTO users (email, name) VALUES ($1, $2) RETURNING id`,
		"rename-"+confID.String()+"@example.com", "Rename Test",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = d.DB.Exec("DELETE FROM users WHERE id = $1", userID) })
	if _, err := d.DB.Exec(
		`INSERT INTO conference_kicks (conference_id, kicked_phone, kicked_by_user_id) VALUES ($1, $2, $3)`,
		confID, oldNum, userID,
	); err != nil {
		t.Fatalf("seed kick: %v", err)
	}

	if err := tr.RenameNumber(ctx, oldNum, newNum); err != nil {
		t.Fatalf("RenameNumber: %v", err)
	}

	// Assert against the specific rows this test seeded (by id / conference id)
	// rather than table-wide counts: the integration suite shares one database,
	// so a global count would be perturbed by other tests' rows.
	assertPhone := func(query string, args []any, want, what string) {
		t.Helper()
		var got string
		if err := d.DB.QueryRow(query, args...).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", what, err)
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", what, got, want)
		}
	}
	assertScopedCount := func(query string, args []any, want int, what string) {
		t.Helper()
		var got int
		if err := d.DB.QueryRow(query, args...).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", what, err)
		}
		if got != want {
			t.Errorf("%s: got %d rows, want %d", what, got, want)
		}
	}

	assertPhone(`SELECT caller FROM calls WHERE id = $1`, []any{outboundID}, newNum, "calls.caller renamed")
	assertPhone(`SELECT callee FROM calls WHERE id = $1`, []any{inboundID}, newNum, "calls.callee renamed")
	assertPhone(`SELECT host_phone FROM conferences WHERE id = $1`, []any{confID}, newNum, "conferences.host_phone renamed")
	assertScopedCount(`SELECT COUNT(*) FROM conference_members WHERE conference_id = $1 AND phone = $2`, []any{confID, newNum}, 1, "conference_members.phone renamed")
	assertScopedCount(`SELECT COUNT(*) FROM conference_members WHERE conference_id = $1 AND phone = $2`, []any{confID, oldNum}, 0, "conference_members still holding old phone")
	assertScopedCount(`SELECT COUNT(*) FROM conference_kicks WHERE conference_id = $1 AND kicked_phone = $2`, []any{confID, newNum}, 1, "conference_kicks.kicked_phone renamed")
	assertScopedCount(`SELECT COUNT(*) FROM conference_kicks WHERE conference_id = $1 AND kicked_phone = $2`, []any{confID, oldNum}, 0, "conference_kicks still holding old phone")
}
