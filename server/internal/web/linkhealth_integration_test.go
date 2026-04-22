//go:build integration

package web

// Integration tests for GET /api/call/{id}/link-health.
//
// Security boundary assertions:
//   - The owner of either call endpoint (caller OR callee) can read.
//   - A user in a linked household but NOT an endpoint-owning household is denied.
//   - Nonexistent call IDs return 404.
//   - Unauthenticated requests are redirected (not 200).
//   - After a call ends and in-memory state is evicted, the DB-fallback read
//     path returns the samples that were flushed before eviction.
//
// All tests require TEST_DATABASE_URL (skipped otherwise via setupCallsTestServer).
// Each test uses distinct phone numbers and cleans up on exit so runs are
// idempotent and -count=2 safe.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/calls"
)

// phoneCounter gives each test its own non-overlapping number range so
// concurrent or repeated runs don't collide.
var phoneCounter atomic.Int64

func init() {
	// Start well above numbers used in TestCallsPageScopedToHousehold (55500xx).
	phoneCounter.Store(5551000)
}

func nextPhone() string {
	n := phoneCounter.Add(1)
	return fmt.Sprintf("%07d", n)
}

// lhSetup holds the per-test state created by newLHEnv.
type lhSetup struct {
	env    callsTestEnv
	userA  *auth.User
	userB  *auth.User
	userC  *auth.User
	hhAID  string
	hhBID  string
	hhCID  string
	numA   string // phone number owned by household A
	numB   string // phone number owned by household B
	numC   string // phone number owned by household C
	hwA    string
	hwB    string
	hwC    string
}

// newLHEnv creates three users in three independent households, each with one
// line, and registers cleanup. Users A/B/C own lines numA/numB/numC.
//
// Cleanup is registered BEFORE any setup DB work so that partial-setup
// failures do not leak rows into the shared test database.
func newLHEnv(t *testing.T) lhSetup {
	t.Helper()

	// Derive all unique identifiers up front, before touching the DB, so that
	// cleanup can reference them whether or not setup completed.
	seq := phoneCounter.Add(3) // reserve three numbers atomically
	numA := fmt.Sprintf("%07d", seq-2)
	numB := fmt.Sprintf("%07d", seq-1)
	numC := fmt.Sprintf("%07d", seq)
	hwA := "lh-hw-" + numA
	hwB := "lh-hw-" + numB
	hwC := "lh-hw-" + numC
	emailA := "lh-a-" + numA + "@example.com"
	emailB := "lh-b-" + numB + "@example.com"
	emailC := "lh-c-" + numC + "@example.com"
	hhNameA := "LH Family A " + numA
	hhNameB := "LH Family B " + numB
	hhNameC := "LH Family C " + numC

	env := setupCallsTestServer(t)
	db := env.database.DB

	// Register cleanup IMMEDIATELY — before any partial setup can fail.
	// All DELETEs are idempotent against missing rows.
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM call_link_health WHERE call_id IN (SELECT id FROM calls WHERE caller IN ($1,$2,$3) OR callee IN ($1,$2,$3))", numA, numB, numC)
		_, _ = db.Exec("DELETE FROM calls WHERE caller IN ($1,$2,$3) OR callee IN ($1,$2,$3)", numA, numB, numC)
		_, _ = db.Exec("DELETE FROM devices WHERE hardware_id IN ($1,$2,$3)", hwA, hwB, hwC)
		_, _ = db.Exec("DELETE FROM lines WHERE number IN ($1,$2,$3)", numA, numB, numC)
		_, _ = db.Exec("DELETE FROM household_links WHERE household_a_id IN (SELECT id FROM households WHERE name IN ($1,$2,$3)) OR household_b_id IN (SELECT id FROM households WHERE name IN ($1,$2,$3))", hhNameA, hhNameB, hhNameC)
		_, _ = db.Exec("DELETE FROM household_members WHERE household_id IN (SELECT id FROM households WHERE name IN ($1,$2,$3))", hhNameA, hhNameB, hhNameC)
		_, _ = db.Exec("DELETE FROM households WHERE name IN ($1,$2,$3)", hhNameA, hhNameB, hhNameC)
		_, _ = db.Exec("DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE email IN ($1,$2,$3))", emailA, emailB, emailC)
		_, _ = db.Exec("DELETE FROM users WHERE email IN ($1,$2,$3)", emailA, emailB, emailC)
	})

	// Create users.
	userA, err := env.authStore.CreateUser(emailA, "LH User A", nil)
	if err != nil {
		t.Fatalf("create user A: %v", err)
	}
	userB, err := env.authStore.CreateUser(emailB, "LH User B", nil)
	if err != nil {
		t.Fatalf("create user B: %v", err)
	}
	userC, err := env.authStore.CreateUser(emailC, "LH User C", nil)
	if err != nil {
		t.Fatalf("create user C: %v", err)
	}

	// Create households.
	hhA, err := env.householdStore.Create(hhNameA, userA.ID)
	if err != nil {
		t.Fatalf("create household A: %v", err)
	}
	hhB, err := env.householdStore.Create(hhNameB, userB.ID)
	if err != nil {
		t.Fatalf("create household B: %v", err)
	}
	hhC, err := env.householdStore.Create(hhNameC, userC.ID)
	if err != nil {
		t.Fatalf("create household C: %v", err)
	}

	// Pair phones so each household owns its line.
	codeA, err := env.pairingStore.GenerateCode(hwA)
	if err != nil {
		t.Fatalf("generate code A: %v", err)
	}
	if _, _, err := env.pairingStore.ClaimDevice(codeA, numA, "Phone A", hhA.ID); err != nil {
		t.Fatalf("claim phone A: %v", err)
	}

	codeB, err := env.pairingStore.GenerateCode(hwB)
	if err != nil {
		t.Fatalf("generate code B: %v", err)
	}
	if _, _, err := env.pairingStore.ClaimDevice(codeB, numB, "Phone B", hhB.ID); err != nil {
		t.Fatalf("claim phone B: %v", err)
	}

	codeC, err := env.pairingStore.GenerateCode(hwC)
	if err != nil {
		t.Fatalf("generate code C: %v", err)
	}
	if _, _, err := env.pairingStore.ClaimDevice(codeC, numC, "Phone C", hhC.ID); err != nil {
		t.Fatalf("claim phone C: %v", err)
	}

	return lhSetup{
		env:   env,
		userA: userA, userB: userB, userC: userC,
		hhAID: hhA.ID, hhBID: hhB.ID, hhCID: hhC.ID,
		numA: numA, numB: numB, numC: numC,
		hwA: hwA, hwB: hwB, hwC: hwC,
	}
}

// authedClient returns an http.Client with a valid session cookie for user.
// The client does NOT follow redirects so auth-failure 303s are distinguishable
// from 200 successes.
func authedClient(t *testing.T, s lhSetup, user *auth.User) *http.Client {
	t.Helper()
	token, _, err := s.env.authStore.CreateSession(user.ID, auth.SessionTTL)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	cookie := &http.Cookie{Name: auth.CookieName, Value: token}
	jar := &testCookieJar{cookie: cookie, url: s.env.srv.URL}
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // don't follow redirects
		},
	}
}

// linkHealthURL builds the endpoint URL for a call ID.
func linkHealthURL(s lhSetup, callID int64) string {
	return fmt.Sprintf("%s/api/call/%d/link-health", s.env.srv.URL, callID)
}

// recordSample inserts one link-health sample for the given call+endpoint.
func recordSample(s lhSetup, callID int64, endpoint string, lossPct float32) {
	loss := lossPct
	s.env.healthStore.Record(callID, endpoint, calls.Sample{
		TS:      time.Now(),
		LossPct: &loss,
	})
}

// --- Tests ---

// TestLinkHealth_OwnerReadsOwnCall: caller endpoint owner (user A) can read
// the call A->B and sees the caller's telemetry window.
func TestLinkHealth_OwnerReadsOwnCall(t *testing.T) {
	s := newLHEnv(t)

	callID, err := s.env.tracker.OnCallInitiated(s.numA, s.numB)
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	recordSample(s, callID, s.numA, 0.5)

	client := authedClient(t, s, s.userA)
	resp, err := client.Get(linkHealthURL(s, callID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("caller owner: got %d want 200", resp.StatusCode)
	}

	var body LinkHealthResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.CallID != callID {
		t.Fatalf("call_id mismatch: got %d want %d", body.CallID, callID)
	}
	if len(body.Caller.Window) != 1 {
		t.Fatalf("caller.window: got %d samples want 1", len(body.Caller.Window))
	}
}

// TestLinkHealth_CalleeOwnerReadsOwnCall: callee endpoint owner (user B) can
// read call A->B.
func TestLinkHealth_CalleeOwnerReadsOwnCall(t *testing.T) {
	s := newLHEnv(t)

	callID, err := s.env.tracker.OnCallInitiated(s.numA, s.numB)
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	recordSample(s, callID, s.numB, 0.2)

	client := authedClient(t, s, s.userB)
	resp, err := client.Get(linkHealthURL(s, callID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("callee owner: got %d want 200", resp.StatusCode)
	}
}

// TestLinkHealth_UnrelatedHouseholdGets404: user C (unrelated to A and B)
// cannot read call A->B.
func TestLinkHealth_UnrelatedHouseholdGets404(t *testing.T) {
	s := newLHEnv(t)

	callID, err := s.env.tracker.OnCallInitiated(s.numA, s.numB)
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}

	client := authedClient(t, s, s.userC)
	resp, err := client.Get(linkHealthURL(s, callID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unrelated household: got %d want 404", resp.StatusCode)
	}
}

// TestLinkHealth_LinkedHouseholdStillGets404: user A's household is linked to
// household B, but call B->C does NOT involve A's household. Linked access
// must not grant read permission.
func TestLinkHealth_LinkedHouseholdStillGets404(t *testing.T) {
	s := newLHEnv(t)

	// Link household A to household B.
	invite, err := s.env.linkStore.CreateInvite(s.hhAID, s.userA.ID)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if _, err := s.env.linkStore.AcceptInvite(invite.InviteCode, s.userB.ID, s.hhBID); err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}

	// Call is between B and C — A is NOT an endpoint owner.
	callID, err := s.env.tracker.OnCallInitiated(s.numB, s.numC)
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}

	client := authedClient(t, s, s.userA)
	resp, err := client.Get(linkHealthURL(s, callID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("linked household access: got %d want 404", resp.StatusCode)
	}
}

// TestLinkHealth_NonexistentCallIs404: a call ID that was never created returns
// 404 and is indistinguishable from an unauthorized access.
func TestLinkHealth_NonexistentCallIs404(t *testing.T) {
	s := newLHEnv(t)

	// Compute an ID that is guaranteed unused: MAX(id)+10000. This stays within
	// INT range (< 2^31) and exercises the sql.ErrNoRows -> zero-Call -> 404
	// branch, not the int32-overflow driver error path.
	var maxID int64
	_ = s.env.database.DB.QueryRow("SELECT COALESCE(MAX(id), 0) FROM calls").Scan(&maxID)
	unknownID := maxID + 10000

	client := authedClient(t, s, s.userA)
	resp, err := client.Get(fmt.Sprintf("%s/api/call/%d/link-health", s.env.srv.URL, unknownID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("nonexistent call: got %d want 404", resp.StatusCode)
	}
}

// TestLinkHealth_UnauthenticatedRedirectsOr401: no session cookie must not
// return 200. The auth middleware redirects (303) to /auth/login.
func TestLinkHealth_UnauthenticatedRedirectsOr401(t *testing.T) {
	s := newLHEnv(t)

	callID, err := s.env.tracker.OnCallInitiated(s.numA, s.numB)
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}

	// Unauthenticated client — no jar, no redirect-following.
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(linkHealthURL(s, callID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("unauthenticated request: got 200, must not expose telemetry")
	}
}

// TestLinkHealth_DBFallbackAfterEvict: samples flushed to DB before eviction
// are returned via the Readback path even after in-memory state is cleared.
func TestLinkHealth_DBFallbackAfterEvict(t *testing.T) {
	s := newLHEnv(t)

	callID, err := s.env.tracker.OnCallInitiated(s.numA, s.numB)
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	recordSample(s, callID, s.numA, 0.9)

	// Flush to DB so the sample is persisted.
	if err := s.env.healthStore.FlushOnce(context.Background()); err != nil {
		t.Fatalf("FlushOnce: %v", err)
	}

	// Evict simulates a server restart or post-call-end state.
	s.env.healthStore.Evict(callID)

	client := authedClient(t, s, s.userA)
	resp, err := client.Get(linkHealthURL(s, callID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post-evict read: got %d want 200", resp.StatusCode)
	}

	var body LinkHealthResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Caller.Window) != 1 {
		t.Fatalf("post-evict window: got %d samples want 1 (DB fallback must serve the flushed sample)", len(body.Caller.Window))
	}
}
