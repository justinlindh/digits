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
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
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
	env   callsTestEnv
	userA *auth.User
	userB *auth.User
	userC *auth.User
	hhAID string
	hhBID string
	hhCID string
	numA  string // phone number owned by household A
	numB  string // phone number owned by household B
	numC  string // phone number owned by household C
	hwA   string
	hwB   string
	hwC   string
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
		// Conference-scoped link-health rows. Phase 2 flush writes rows with
		// conference_id set; the existing call_id-scoped delete above does
		// not catch them.
		_, _ = db.Exec("DELETE FROM call_link_health WHERE conference_id IN (SELECT id FROM conferences WHERE host_phone IN ($1,$2,$3))", numA, numB, numC)
		_, _ = db.Exec("DELETE FROM calls WHERE caller IN ($1,$2,$3) OR callee IN ($1,$2,$3)", numA, numB, numC)
		// conference_members before conferences (FK cascade would also work,
		// but keeping the deletes explicit matches the pattern).
		_, _ = db.Exec("DELETE FROM conference_members WHERE conference_id IN (SELECT id FROM conferences WHERE host_phone IN ($1,$2,$3))", numA, numB, numC)
		_, _ = db.Exec("DELETE FROM conferences WHERE host_phone IN ($1,$2,$3)", numA, numB, numC)
		_, _ = db.Exec("DELETE FROM devices WHERE hardware_id IN ($1,$2,$3)", hwA, hwB, hwC)
		_, _ = db.Exec("DELETE FROM lines WHERE number IN ($1,$2,$3)", numA, numB, numC)
		_, _ = db.Exec("DELETE FROM household_links WHERE household_a_id IN (SELECT id FROM households WHERE name IN ($1,$2,$3)) OR household_b_id IN (SELECT id FROM households WHERE name IN ($1,$2,$3))", hhNameA, hhNameB, hhNameC)
		_, _ = db.Exec("DELETE FROM household_members WHERE household_id IN (SELECT id FROM households WHERE name IN ($1,$2,$3))", hhNameA, hhNameB, hhNameC)
		_, _ = db.Exec("DELETE FROM households WHERE name IN ($1,$2,$3)", hhNameA, hhNameB, hhNameC)
		_, _ = db.Exec("DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE email IN ($1,$2,$3))", emailA, emailB, emailC)
		_, _ = db.Exec("DELETE FROM users WHERE email IN ($1,$2,$3)", emailA, emailB, emailC)
	})

	// Create users.
	userA, err := env.authStore.CreateUser(context.Background(), emailA, "LH User A", nil)
	if err != nil {
		t.Fatalf("create user A: %v", err)
	}
	userB, err := env.authStore.CreateUser(context.Background(), emailB, "LH User B", nil)
	if err != nil {
		t.Fatalf("create user B: %v", err)
	}
	userC, err := env.authStore.CreateUser(context.Background(), emailC, "LH User C", nil)
	if err != nil {
		t.Fatalf("create user C: %v", err)
	}

	// Create households.
	hhA, err := env.householdStore.Create(context.Background(), hhNameA, userA.ID)
	if err != nil {
		t.Fatalf("create household A: %v", err)
	}
	hhB, err := env.householdStore.Create(context.Background(), hhNameB, userB.ID)
	if err != nil {
		t.Fatalf("create household B: %v", err)
	}
	hhC, err := env.householdStore.Create(context.Background(), hhNameC, userC.ID)
	if err != nil {
		t.Fatalf("create household C: %v", err)
	}

	// Pair phones so each household owns its line.
	codeA, err := env.pairingStore.GenerateCode(context.Background(), hwA)
	if err != nil {
		t.Fatalf("generate code A: %v", err)
	}
	if _, _, err := env.pairingStore.ClaimDevice(context.Background(), codeA, numA, "Phone A", hhA.ID); err != nil {
		t.Fatalf("claim phone A: %v", err)
	}

	codeB, err := env.pairingStore.GenerateCode(context.Background(), hwB)
	if err != nil {
		t.Fatalf("generate code B: %v", err)
	}
	if _, _, err := env.pairingStore.ClaimDevice(context.Background(), codeB, numB, "Phone B", hhB.ID); err != nil {
		t.Fatalf("claim phone B: %v", err)
	}

	codeC, err := env.pairingStore.GenerateCode(context.Background(), hwC)
	if err != nil {
		t.Fatalf("generate code C: %v", err)
	}
	if _, _, err := env.pairingStore.ClaimDevice(context.Background(), codeC, numC, "Phone C", hhC.ID); err != nil {
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
	token, _, err := s.env.authStore.CreateSession(context.Background(), user.ID, auth.SessionTTL)
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

	callID, err := s.env.tracker.OnCallInitiated(context.Background(), s.numA, s.numB)
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

	callID, err := s.env.tracker.OnCallInitiated(context.Background(), s.numA, s.numB)
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

	callID, err := s.env.tracker.OnCallInitiated(context.Background(), s.numA, s.numB)
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
	invite, err := s.env.linkStore.CreateInvite(context.Background(), s.hhAID, s.userA.ID)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if _, err := s.env.linkStore.AcceptInvite(context.Background(), invite.InviteCode, s.userB.ID, s.hhBID); err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}

	// Call is between B and C — A is NOT an endpoint owner.
	callID, err := s.env.tracker.OnCallInitiated(context.Background(), s.numB, s.numC)
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

	callID, err := s.env.tracker.OnCallInitiated(context.Background(), s.numA, s.numB)
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

// startCall initiates a call via the tracker. Returns the call id.
func startCall(t *testing.T, s lhSetup, caller, callee string) int64 {
	t.Helper()
	id, err := s.env.tracker.OnCallInitiated(context.Background(), caller, callee)
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	return id
}

// disconnectURL builds the force-disconnect endpoint URL for a call ID.
func disconnectURL(s lhSetup, callID int64) string {
	return fmt.Sprintf("%s/api/call/%s/disconnect", s.env.srv.URL, strconv.FormatInt(callID, 10))
}

// TestLinkHealth_DBFallbackAfterEvict: samples flushed to DB before eviction
// are returned via the Readback path even after in-memory state is cleared.
func TestLinkHealth_DBFallbackAfterEvict(t *testing.T) {
	s := newLHEnv(t)

	callID, err := s.env.tracker.OnCallInitiated(context.Background(), s.numA, s.numB)
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

// TestForceDisconnect_WritesAuditAndTearsDown verifies that a successful
// force-disconnect sets force_ended_by, marks status=ended, and sets ended_at.
func TestForceDisconnect_WritesAuditAndTearsDown(t *testing.T) {
	s := newLHEnv(t)
	callID := startCall(t, s, s.numA, s.numB)

	// Record a sample so the endpoint has something to observe.
	loss := float32(0.5)
	s.env.healthStore.Record(callID, s.numA, calls.Sample{TS: time.Now(), LossPct: &loss})

	client := authedClient(t, s, s.userA)
	postResp, err := client.Post(disconnectURL(s, callID), "application/json", nil)
	if err != nil {
		t.Fatalf("post disconnect: %v", err)
	}
	defer func() { _ = postResp.Body.Close() }()
	if postResp.StatusCode != http.StatusOK {
		t.Fatalf("disconnect: got %d want 200", postResp.StatusCode)
	}

	// Audit column set to user A's id.
	var forceEndedBy sql.NullString
	if err := s.env.database.DB.QueryRow(
		"SELECT force_ended_by FROM calls WHERE id = $1", callID,
	).Scan(&forceEndedBy); err != nil {
		t.Fatalf("scan force_ended_by: %v", err)
	}
	if !forceEndedBy.Valid || forceEndedBy.String != s.userA.ID {
		t.Fatalf("force_ended_by: got (%v,%q) want user %s", forceEndedBy.Valid, forceEndedBy.String, s.userA.ID)
	}

	// Call row is ended.
	var status string
	var endedAt sql.NullTime
	if err := s.env.database.DB.QueryRow(
		"SELECT status, ended_at FROM calls WHERE id = $1", callID,
	).Scan(&status, &endedAt); err != nil {
		t.Fatalf("scan call: %v", err)
	}
	if status != "ended" {
		t.Fatalf("status: got %q want ended", status)
	}
	if !endedAt.Valid {
		t.Fatal("ended_at is NULL")
	}
}

// TestForceDisconnect_UnauthorizedGets404 verifies that a user from an
// unrelated household cannot force-disconnect a call, and that the audit
// column is not written.
func TestForceDisconnect_UnauthorizedGets404(t *testing.T) {
	s := newLHEnv(t)
	callID := startCall(t, s, s.numA, s.numB)

	// User C is in an unrelated household.
	client := authedClient(t, s, s.userC)
	resp, err := client.Post(disconnectURL(s, callID), "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d want 404", resp.StatusCode)
	}

	// Audit column should be unset.
	var forceEndedBy sql.NullString
	if err := s.env.database.DB.QueryRow(
		"SELECT force_ended_by FROM calls WHERE id = $1", callID,
	).Scan(&forceEndedBy); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if forceEndedBy.Valid {
		t.Fatalf("unauthorized request wrote to audit: %q", forceEndedBy.String)
	}
}

// TestForceDisconnect_IdempotentOnAlreadyEnded verifies that a second
// force-disconnect from a different owner returns 200 but does NOT overwrite
// the force_ended_by audit column set by the first caller.
func TestForceDisconnect_IdempotentOnAlreadyEnded(t *testing.T) {
	s := newLHEnv(t)
	callID := startCall(t, s, s.numA, s.numB)

	// First disconnect: user A.
	clientA := authedClient(t, s, s.userA)
	r1, err := clientA.Post(disconnectURL(s, callID), "application/json", nil)
	if err != nil {
		t.Fatalf("first post: %v", err)
	}
	_ = r1.Body.Close()
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("first: got %d want 200", r1.StatusCode)
	}

	// Second POST by user B -- still an owner (callee side). Should succeed
	// but NOT overwrite the audit column.
	clientB := authedClient(t, s, s.userB)
	r2, err := clientB.Post(disconnectURL(s, callID), "application/json", nil)
	if err != nil {
		t.Fatalf("second post: %v", err)
	}
	_ = r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("second: got %d want 200", r2.StatusCode)
	}

	// Audit column still names user A.
	var forceEndedBy sql.NullString
	if err := s.env.database.DB.QueryRow(
		"SELECT force_ended_by FROM calls WHERE id = $1", callID,
	).Scan(&forceEndedBy); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !forceEndedBy.Valid || forceEndedBy.String != s.userA.ID {
		t.Fatalf("audit overwritten by second caller: got (%v,%q) want user A %s",
			forceEndedBy.Valid, forceEndedBy.String, s.userA.ID)
	}
}

// authedGet issues an authenticated GET request to path on s.env.srv and
// returns the response. The caller is responsible for closing the body.
// Redirects are not followed so auth-failure 303s are distinguishable from 200s.
func authedGet(t *testing.T, s lhSetup, user *auth.User, path string) *http.Response {
	t.Helper()
	client := authedClient(t, s, user)
	resp, err := client.Get(s.env.srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func TestCallLiveDetail_OwnerRenders(t *testing.T) {
	s := newLHEnv(t)
	callID := startCall(t, s, s.numA, s.numB)
	// Seed a sample so the initial snapshot renders with data.
	loss := float32(0.5)
	s.env.healthStore.Record(callID, s.numA, calls.Sample{TS: time.Now(), LossPct: &loss})

	resp := authedGet(t, s, s.userA, "/call/live/"+strconv.FormatInt(callID, 10))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	// Both endpoint numbers appear in the .deck-card__num rendering.
	if !strings.Contains(bodyStr, s.numA) {
		t.Fatalf("body missing caller number %q", s.numA)
	}
	if !strings.Contains(bodyStr, s.numB) {
		t.Fatalf("body missing callee number %q", s.numB)
	}
	// SSE stream URL is wired.
	expectedSSE := "/api/call/" + strconv.FormatInt(callID, 10) + "/link-health/stream"
	if !strings.Contains(bodyStr, expectedSSE) {
		t.Fatalf("body missing SSE stream URL %q", expectedSSE)
	}
	// End-call button rendered (call is live).
	if !strings.Contains(bodyStr, "deck-kill") {
		t.Fatal("body missing deck-kill (End-call) button")
	}
}

func TestCallLiveDetail_UnrelatedHouseholdGets404(t *testing.T) {
	s := newLHEnv(t)
	callID := startCall(t, s, s.numA, s.numB)

	resp := authedGet(t, s, s.userC, "/call/live/"+strconv.FormatInt(callID, 10))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d want 404", resp.StatusCode)
	}
}

func TestCallLiveDetail_EndedCallStillRenders(t *testing.T) {
	s := newLHEnv(t)
	callID := startCall(t, s, s.numA, s.numB)
	// End the call.
	if err := s.env.tracker.OnCallEnded(context.Background(), s.numA, s.numB); err != nil {
		t.Fatalf("OnCallEnded: %v", err)
	}

	resp := authedGet(t, s, s.userA, "/call/live/"+strconv.FormatInt(callID, 10))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200 (postmortem view)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)
	// Terminal state should NOT auto-connect the SSE stream.
	if strings.Contains(bodyStr, "sse-connect=") {
		t.Fatal("ended-call page should not wire SSE auto-connect")
	}
	// End-call button should not render.
	if strings.Contains(bodyStr, "deck-kill") {
		t.Fatal("ended-call page should not render End-call button")
	}
	// Terminal state chip should be visible.
	if !strings.Contains(bodyStr, "deck-ended-chip") && !strings.Contains(bodyStr, "deck-ended") {
		t.Fatal("ended-call page missing terminal-state indicator")
	}
}

func TestDashboardLineCardLinksToCallLive(t *testing.T) {
	s := newLHEnv(t)
	callID := startCall(t, s, s.numA, s.numB)

	resp := authedGet(t, s, s.userA, "/")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)
	expected := `href="/call/live/` + strconv.FormatInt(callID, 10) + `"`
	if !strings.Contains(bodyStr, expected) {
		t.Fatalf("dashboard missing link %q", expected)
	}
}

func TestConferenceLinkHealthJSON(t *testing.T) {
	env := newLHEnv(t)
	ctx := context.Background()
	tr := env.env.tracker

	// Seed two 2-party calls and merge into a conference. All three phones
	// are owned by separate households (A, B, C); A is host.
	if _, err := tr.OnCallInitiated(ctx, env.numA, env.numB); err != nil {
		t.Fatalf("seed call A->B: %v", err)
	}
	if _, err := tr.OnCallInitiated(ctx, env.numA, env.numC); err != nil {
		t.Fatalf("seed call A->C: %v", err)
	}
	_ = tr.OnCallAnswered(ctx, env.numA, env.numB)
	_ = tr.OnCallAnswered(ctx, env.numA, env.numC)
	originatingCallID := tr.CallIDForPair(ctx, env.numA, env.numB)
	conf, err := tr.CreateConferencePersistent(ctx, env.numA, originatingCallID, []string{env.numB, env.numC})
	if err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}

	// Record one per-edge sample so the JSON carries a non-empty window.
	loss := float32(1.5)
	env.env.healthStore.RecordEdge(conf.ID, env.numA, env.numB,
		calls.Sample{TS: time.Unix(0, 1), LossPct: &loss})

	// GET as user A.
	token, _, err := env.env.authStore.CreateSession(ctx, env.userA.ID, auth.SessionTTL)
	if err != nil {
		t.Fatalf("create session A: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet,
		env.env.srv.URL+"/api/conference/"+conf.ID.String()+"/link-health", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	resp, err := env.env.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", resp.StatusCode, string(body))
	}
	var got ConferenceLinkHealthResp
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, string(body))
	}
	if got.ConfID != conf.ID {
		t.Fatalf("ConfID: got %s want %s", got.ConfID, conf.ID)
	}
	if got.Ended {
		t.Errorf("expected Ended=false for a live conference; got true")
	}
	if len(got.Members) != 3 {
		t.Fatalf("Members len: got %d want 3", len(got.Members))
	}
	if len(got.Edges) != 6 {
		t.Fatalf("Edges len: got %d want 6 (3x2 directed edges)", len(got.Edges))
	}

	// Host flag.
	var hostCount int
	for _, m := range got.Members {
		if m.IsHost {
			hostCount++
			if m.Number != env.numA {
				t.Errorf("IsHost on wrong member: got %s want %s", m.Number, env.numA)
			}
		}
	}
	if hostCount != 1 {
		t.Errorf("exactly one host expected; got %d", hostCount)
	}

	// Host -> numB edge carries the recorded sample.
	var foundHostToB bool
	for _, e := range got.Edges {
		if e.From == env.numA && e.Peer == env.numB {
			foundHostToB = true
			if e.Latest == nil || e.Latest.LossPct == nil || *e.Latest.LossPct != 1.5 {
				t.Errorf("A->B edge latest lossPct not preserved: %+v", e.Latest)
			}
		}
	}
	if !foundHostToB {
		t.Errorf("A->B edge missing from response")
	}

	// 404 for a user whose household owns no conference member (parallels
	// TestRequireConferenceOwnership userD case, but via HTTP).
	numD := nextPhone()
	hwD := "json-d-" + numD
	emailD := "json-d-" + numD + "@example.com"
	hhNameD := "JSON D " + numD
	t.Cleanup(func() {
		db := env.env.database.DB
		_, _ = db.Exec("DELETE FROM devices WHERE hardware_id = $1", hwD)
		_, _ = db.Exec("DELETE FROM lines WHERE number = $1", numD)
		_, _ = db.Exec("DELETE FROM household_members WHERE household_id IN (SELECT id FROM households WHERE name = $1)", hhNameD)
		_, _ = db.Exec("DELETE FROM households WHERE name = $1", hhNameD)
		_, _ = db.Exec("DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE email = $1)", emailD)
		_, _ = db.Exec("DELETE FROM users WHERE email = $1", emailD)
	})
	userD, err := env.env.authStore.CreateUser(ctx, emailD, "JSON D", nil)
	if err != nil {
		t.Fatalf("create user D: %v", err)
	}
	hhD, err := env.env.householdStore.Create(ctx, hhNameD, userD.ID)
	if err != nil {
		t.Fatalf("create household D: %v", err)
	}
	codeD, err := env.env.pairingStore.GenerateCode(ctx, hwD)
	if err != nil {
		t.Fatalf("gen code D: %v", err)
	}
	if _, _, err := env.env.pairingStore.ClaimDevice(ctx, codeD, numD, "Phone D", hhD.ID); err != nil {
		t.Fatalf("claim D: %v", err)
	}

	tokenD, _, err := env.env.authStore.CreateSession(ctx, userD.ID, auth.SessionTTL)
	if err != nil {
		t.Fatalf("create session D: %v", err)
	}
	reqD, _ := http.NewRequest(http.MethodGet,
		env.env.srv.URL+"/api/conference/"+conf.ID.String()+"/link-health", nil)
	reqD.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tokenD})
	respD, err := env.env.srv.Client().Do(reqD)
	if err != nil {
		t.Fatalf("do D: %v", err)
	}
	_ = respD.Body.Close()
	if respD.StatusCode != http.StatusNotFound {
		t.Errorf("unrelated user: got %d want 404", respD.StatusCode)
	}
}

// TestRequireConferenceOwnership verifies the conference-scope auth helper.
// Any household that directly owns at least one member may observe; others
// get 404; unknown conferences get 404; linked households do NOT grant
// observation (parity with requireCallEndpointOwnership).
func TestRequireConferenceOwnership(t *testing.T) {
	env := newLHEnv(t)
	ctx := context.Background()
	tr := env.env.tracker

	// Seed two 2-party calls and merge into a conference.
	if _, err := tr.OnCallInitiated(ctx, env.numA, env.numB); err != nil {
		t.Fatalf("seed call A->B: %v", err)
	}
	if _, err := tr.OnCallInitiated(ctx, env.numA, env.numC); err != nil {
		t.Fatalf("seed call A->C: %v", err)
	}
	_ = tr.OnCallAnswered(ctx, env.numA, env.numB)
	_ = tr.OnCallAnswered(ctx, env.numA, env.numC)
	originatingCallID := tr.CallIDForPair(ctx, env.numA, env.numB)
	conf, err := tr.CreateConferencePersistent(ctx, env.numA, originatingCallID, []string{env.numB, env.numC})
	if err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}

	// Use the Handler that shares state with the tracker used to seed data above.
	h := env.env.handler

	check := func(label, userID string, wantOK bool, wantCode int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/conference/live/"+conf.ID.String(), nil)
		ctx2 := context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: userID, Email: "x", Name: "x"})
		req = req.WithContext(ctx2)
		rec := httptest.NewRecorder()
		_, _, _, ok := h.requireConferenceOwnership(rec, req, conf.ID)
		if ok != wantOK {
			t.Errorf("%s: ok=%v want %v", label, ok, wantOK)
		}
		if !wantOK && rec.Code != wantCode {
			t.Errorf("%s: code=%d want %d", label, rec.Code, wantCode)
		}
		if wantOK && rec.Code != http.StatusOK && rec.Code != 0 {
			// httptest.NewRecorder starts at 200 implicitly; if nothing was written,
			// rec.Code is 0, which is also acceptable for a success path that did
			// not invoke WriteHeader.
			t.Errorf("%s: success path wrote unexpected code %d", label, rec.Code)
		}
	}

	// Household A owns the host line: expect ok.
	check("userA (host household)", env.userA.ID, true, 0)
	// Household B owns an added-member line: expect ok.
	check("userB (member household)", env.userB.ID, true, 0)
	// Household C owns an added-member line: expect ok.
	check("userC (member household)", env.userC.ID, true, 0)

	// A user whose household owns no conference member gets 404.
	numD := nextPhone()
	hwD := "ownership-d-" + numD
	emailD := "ownership-d-" + numD + "@example.com"
	hhNameD := "Ownership D " + numD

	// Register cleanup BEFORE any fallible DB work so partial-setup failures
	// don't leak rows. All DELETEs are idempotent against missing rows.
	t.Cleanup(func() {
		db := env.env.database.DB
		_, _ = db.Exec("DELETE FROM devices WHERE hardware_id = $1", hwD)
		_, _ = db.Exec("DELETE FROM lines WHERE number = $1", numD)
		_, _ = db.Exec("DELETE FROM household_members WHERE household_id IN (SELECT id FROM households WHERE name = $1)", hhNameD)
		_, _ = db.Exec("DELETE FROM households WHERE name = $1", hhNameD)
		_, _ = db.Exec("DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE email = $1)", emailD)
		_, _ = db.Exec("DELETE FROM users WHERE email = $1", emailD)
	})

	userD, err := env.env.authStore.CreateUser(context.Background(), emailD, "User D", nil)
	if err != nil {
		t.Fatalf("create user D: %v", err)
	}
	hhD, err := env.env.householdStore.Create(context.Background(), hhNameD, userD.ID)
	if err != nil {
		t.Fatalf("create household D: %v", err)
	}
	// Seed a line owned by D so ownedLinesForUser returns non-empty (proving
	// the 404 is due to lack of conference membership, not lack of any line).
	codeD, err := env.env.pairingStore.GenerateCode(context.Background(), hwD)
	if err != nil {
		t.Fatalf("gen code D: %v", err)
	}
	if _, _, err := env.env.pairingStore.ClaimDevice(context.Background(), codeD, numD, "Phone D", hhD.ID); err != nil {
		t.Fatalf("claim D: %v", err)
	}

	check("userD (unrelated household)", userD.ID, false, http.StatusNotFound)

	// Unknown conference ID returns 404.
	unknownID := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/conference/live/"+unknownID.String(), nil)
	reqCtx := context.WithValue(req.Context(), auth.UserContextKey, &auth.User{ID: env.userA.ID, Email: "x", Name: "x"})
	req = req.WithContext(reqCtx)
	rec := httptest.NewRecorder()
	_, _, _, ok := h.requireConferenceOwnership(rec, req, unknownID)
	if ok {
		t.Error("unknown conference id: should not be ok")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown conference id: code=%d want 404", rec.Code)
	}
}

func TestConferenceLiveDetailPage_OwnerRenders(t *testing.T) {
	s := newLHEnv(t)
	ctx := context.Background()
	tr := s.env.tracker
	if _, err := tr.OnCallInitiated(ctx, s.numA, s.numB); err != nil {
		t.Fatalf("seed call A->B: %v", err)
	}
	if _, err := tr.OnCallInitiated(ctx, s.numA, s.numC); err != nil {
		t.Fatalf("seed call A->C: %v", err)
	}
	_ = tr.OnCallAnswered(ctx, s.numA, s.numB)
	_ = tr.OnCallAnswered(ctx, s.numA, s.numC)
	originatingCallID := tr.CallIDForPair(ctx, s.numA, s.numB)
	conf, err := tr.CreateConferencePersistent(ctx, s.numA, originatingCallID, []string{s.numB, s.numC})
	if err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}

	client := authedClient(t, s, s.userA)
	req, _ := http.NewRequest(http.MethodGet, s.env.srv.URL+"/conference/live/"+conf.ID.String(), nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", resp.StatusCode, string(body))
	}
	bodyStr := string(body)
	for _, want := range []string{
		"/api/conference/" + conf.ID.String() + "/link-health/stream",
		s.numA, s.numB, s.numC,
		"deck-matrix",
	} {
		if !strings.Contains(bodyStr, want) {
			t.Errorf("expected body to contain %q", want)
		}
	}
}

func TestConferenceLiveDetailPage_EndedRendersTerminalNoSSE(t *testing.T) {
	s := newLHEnv(t)
	ctx := context.Background()
	tr := s.env.tracker
	if _, err := tr.OnCallInitiated(ctx, s.numA, s.numB); err != nil {
		t.Fatalf("seed call A->B: %v", err)
	}
	if _, err := tr.OnCallInitiated(ctx, s.numA, s.numC); err != nil {
		t.Fatalf("seed call A->C: %v", err)
	}
	_ = tr.OnCallAnswered(ctx, s.numA, s.numB)
	_ = tr.OnCallAnswered(ctx, s.numA, s.numC)
	originatingCallID := tr.CallIDForPair(ctx, s.numA, s.numB)
	conf, err := tr.CreateConferencePersistent(ctx, s.numA, originatingCallID, []string{s.numB, s.numC})
	if err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}
	if err := tr.EndConferencePersistent(ctx, conf.ID, "host_hangup"); err != nil {
		t.Fatalf("end: %v", err)
	}

	client := authedClient(t, s, s.userA)
	req, _ := http.NewRequest(http.MethodGet, s.env.srv.URL+"/conference/live/"+conf.ID.String(), nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ended status: got %d want 200 (terminal render); body=%s", resp.StatusCode, string(body))
	}
	bodyStr := string(body)
	if strings.Contains(bodyStr, `sse-connect="/api/conference/`) {
		t.Error("ended page should NOT wire the SSE stream")
	}
	if !strings.Contains(bodyStr, "deck-ended-chip") {
		t.Error("ended page should show the terminal chip")
	}
}

func TestConferenceLiveDetailPage_UnknownUUID_404(t *testing.T) {
	s := newLHEnv(t)
	client := authedClient(t, s, s.userA)
	req, _ := http.NewRequest(http.MethodGet, s.env.srv.URL+"/conference/live/"+uuid.New().String(), nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown uuid: got %d want 404", resp.StatusCode)
	}
}

// TestConferenceLinkHealthJSON_DBFallback verifies that after a conference
// ends and its in-memory rings are evicted, ReadbackEdge restores the edge
// window from the flushed DB rows so the JSON response still carries
// historical samples.
func TestConferenceLinkHealthJSON_DBFallback(t *testing.T) {
	env := newLHEnv(t)
	ctx := context.Background()
	tr := env.env.tracker

	if _, err := tr.OnCallInitiated(ctx, env.numA, env.numB); err != nil {
		t.Fatalf("seed call A->B: %v", err)
	}
	if _, err := tr.OnCallInitiated(ctx, env.numA, env.numC); err != nil {
		t.Fatalf("seed call A->C: %v", err)
	}
	_ = tr.OnCallAnswered(ctx, env.numA, env.numB)
	_ = tr.OnCallAnswered(ctx, env.numA, env.numC)
	originatingCallID := tr.CallIDForPair(ctx, env.numA, env.numB)
	conf, err := tr.CreateConferencePersistent(ctx, env.numA, originatingCallID, []string{env.numB, env.numC})
	if err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}

	// Record + flush one per-edge sample, then end the conference so the
	// in-memory ring is evicted. Subsequent reads must come from the DB.
	loss := float32(2.5)
	env.env.healthStore.RecordEdge(conf.ID, env.numA, env.numB,
		calls.Sample{TS: time.Unix(0, 1000), LossPct: &loss})
	if err := env.env.healthStore.FlushOnce(ctx); err != nil {
		t.Fatalf("FlushOnce: %v", err)
	}
	if err := tr.EndConferencePersistent(ctx, conf.ID, "host_hangup"); err != nil {
		t.Fatalf("EndConferencePersistent: %v", err)
	}

	// Confirm the in-memory window is now empty.
	if w := env.env.healthStore.WindowEdge(conf.ID, env.numA, env.numB); len(w) != 0 {
		t.Fatalf("expected in-memory window to be empty post-evict; got %d", len(w))
	}

	// GET the JSON endpoint as user A. The A->B edge should carry the
	// flushed sample from the DB fallback.
	token, _, err := env.env.authStore.CreateSession(ctx, env.userA.ID, auth.SessionTTL)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet,
		env.env.srv.URL+"/api/conference/"+conf.ID.String()+"/link-health", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	resp, err := env.env.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", resp.StatusCode, string(body))
	}

	var got ConferenceLinkHealthResp
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !got.Ended {
		t.Error("expected Ended=true for an ended conference")
	}

	var foundHostToB bool
	for _, e := range got.Edges {
		if e.From == env.numA && e.Peer == env.numB {
			foundHostToB = true
			if e.Latest == nil {
				t.Errorf("A->B edge Latest should be populated from DB fallback")
			} else if e.Latest.LossPct == nil || *e.Latest.LossPct != 2.5 {
				t.Errorf("A->B edge LossPct not preserved: %+v", e.Latest)
			}
			if len(e.Window) == 0 {
				t.Errorf("A->B edge Window should be non-empty from DB fallback")
			}
		}
	}
	if !foundHostToB {
		t.Errorf("A->B edge missing from response")
	}
}
