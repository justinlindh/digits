//go:build integration

package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/household"
	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/pairing"
)

// callsTestEnv bundles the pieces that integration tests for the calls-related
// endpoints need. Callers that only need a subset can ignore fields.
type callsTestEnv struct {
	srv            *httptest.Server
	database       *db.Database
	lineStore      *line.Store
	tracker        *calls.Tracker
	authStore      *auth.Store
	householdStore *household.Store
	linkStore      *household.LinkStore
	pairingStore   *pairing.Store
	healthStore    *calls.HealthStore
	handler        *Handler
}

// setupCallsTestServer creates a full server with all stores wired via
// testDeps, returning a callsTestEnv bundle for the test body.
func setupCallsTestServer(t *testing.T) callsTestEnv {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	database, err := db.Open(dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	deps, authStore := testDeps(t, database)
	h, err := NewHandler(deps, HandlerConfig{Addr: ":0"})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	srv := httptest.NewServer(h.Router())
	t.Cleanup(srv.Close)
	return callsTestEnv{
		srv:            srv,
		database:       database,
		lineStore:      deps.LineStore,
		tracker:        deps.Tracker,
		authStore:      authStore,
		householdStore: deps.HouseholdStore,
		linkStore:      deps.LinkStore,
		pairingStore:   deps.PairingStore,
		healthStore:    deps.HealthStore,
		handler:        h,
	}
}

// TestCallsPageScopedToHousehold verifies that the /calls page only shows
// calls involving phones belonging to the authenticated user's household.
func TestCallsPageScopedToHousehold(t *testing.T) {
	// === Step 1: Set up test server with householdStore wired in ===
	env := setupCallsTestServer(t)
	srv := env.srv
	database := env.database
	tracker := env.tracker
	authStore := env.authStore
	householdStore := env.householdStore

	// === Step 2: Create two users in two separate households ===
	userA, err := authStore.CreateUser(context.Background(), "e2e-calls-a@example.com", "Calls User A", nil)
	if err != nil {
		userA, err = authStore.GetUserByEmail(context.Background(), "e2e-calls-a@example.com")
		if err != nil {
			t.Fatalf("create/get user A: %v", err)
		}
	}
	markUserOnboarded(t, authStore, userA.ID)
	userB, err := authStore.CreateUser(context.Background(), "e2e-calls-b@example.com", "Calls User B", nil)
	if err != nil {
		userB, err = authStore.GetUserByEmail(context.Background(), "e2e-calls-b@example.com")
		if err != nil {
			t.Fatalf("create/get user B: %v", err)
		}
	}
	markUserOnboarded(t, authStore, userB.ID)

	hhA, err := householdStore.Create(context.Background(), "Calls Family A", userA.ID)
	if err != nil {
		t.Fatalf("create household A: %v", err)
	}
	hhB, err := householdStore.Create(context.Background(), "Calls Family B", userB.ID)
	if err != nil {
		t.Fatalf("create household B: %v", err)
	}
	// handleCalls redirects to /settings when call history is disabled (the
	// default). We care about scoping the rendered list, not the feature gate.
	if err := householdStore.SetCallHistoryEnabled(context.Background(), hhA.ID, true); err != nil {
		t.Fatalf("enable call history A: %v", err)
	}

	// === Step 3: Pair phones into each household ===
	pairingStore := env.pairingStore
	hwA := "e2e-calls-hw-a"
	hwB := "e2e-calls-hw-b"
	phoneNumA := "5550001"
	phoneNumB := "5550002"

	codeA, err := pairingStore.GenerateCode(context.Background(), hwA)
	if err != nil {
		t.Fatalf("generate code A: %v", err)
	}
	if _, _, err := pairingStore.ClaimDevice(context.Background(), codeA, phoneNumA, "Phone A", hhA.ID); err != nil {
		t.Fatalf("claim phone A: %v", err)
	}

	codeB, err := pairingStore.GenerateCode(context.Background(), hwB)
	if err != nil {
		t.Fatalf("generate code B: %v", err)
	}
	if _, _, err := pairingStore.ClaimDevice(context.Background(), codeB, phoneNumB, "Phone B", hhB.ID); err != nil {
		t.Fatalf("claim phone B: %v", err)
	}

	// Register cleanup
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM calls WHERE caller IN ($1, $2) OR callee IN ($1, $2)", phoneNumA, phoneNumB)
		_, _ = database.DB.Exec("DELETE FROM devices WHERE hardware_id IN ($1, $2)", hwA, hwB)
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number IN ($1, $2)", phoneNumA, phoneNumB)
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE user_id IN ($1, $2)", userA.ID, userB.ID)
		_, _ = database.DB.Exec("DELETE FROM households WHERE id IN ($1, $2)", hhA.ID, hhB.ID)
		_, _ = database.DB.Exec("DELETE FROM sessions WHERE user_id IN ($1, $2)", userA.ID, userB.ID)
		_, _ = database.DB.Exec("DELETE FROM users WHERE id IN ($1, $2)", userA.ID, userB.ID)
	})

	// === Step 4: Insert call records via tracker ===
	// Call A: household-A phone calls an external number
	if _, err := tracker.OnCallInitiated(context.Background(), phoneNumA, "5559999"); err != nil {
		t.Fatalf("initiate call A: %v", err)
	}
	if err := tracker.OnCallEnded(context.Background(), phoneNumA, "5559999"); err != nil {
		t.Fatalf("end call A: %v", err)
	}

	// Call B: household-B phone calls a different external number
	if _, err := tracker.OnCallInitiated(context.Background(), phoneNumB, "5558888"); err != nil {
		t.Fatalf("initiate call B: %v", err)
	}
	if err := tracker.OnCallEnded(context.Background(), phoneNumB, "5558888"); err != nil {
		t.Fatalf("end call B: %v", err)
	}

	// === Step 5: Create a session for userA ===
	tokenA, _, err := authStore.CreateSession(context.Background(), userA.ID, auth.SessionTTL)
	if err != nil {
		t.Fatalf("create session for user A: %v", err)
	}
	cookieA := &http.Cookie{
		Name:  auth.CookieName,
		Value: tokenA,
	}
	jarA := &testCookieJar{cookie: cookieA, url: srv.URL}
	clientA := &http.Client{Jar: jarA}

	// === Step 6: GET /calls as userA ===
	resp, err := clientA.Get(srv.URL + "/calls")
	if err != nil {
		t.Fatalf("GET /calls: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var bodyBuf strings.Builder
	if _, err := io.Copy(&bodyBuf, resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := bodyBuf.String()

	// Calls template renders numbers through line.FormatNumber, which turns
	// a 7-digit number into XXX-XXXX. Assert against the formatted form so
	// the test stays honest about what the user actually sees.
	displayA := line.FormatNumber(phoneNumA)
	displayB := line.FormatNumber(phoneNumB)

	// === Step 7: userA's household phone number should appear ===
	if !strings.Contains(body, displayA) {
		t.Errorf("expected /calls page to contain household-A phone %s, but it didn't\nbody snippet: %s",
			displayA, truncate(body, 500))
	}

	// === Step 8: userB's household phone number should NOT appear ===
	if strings.Contains(body, displayB) {
		t.Errorf("expected /calls page to NOT contain household-B phone %s, but it did\nbody snippet: %s",
			displayB, truncate(body, 500))
	}

	t.Logf("✓ /calls page correctly scoped: shows %s, hides %s", displayA, displayB)
}

// TestCallsPageRenders3WayConference verifies that a completed conference
// renders as a single unified entry with the chip--conf status chip and
// all participant numbers visible.
func TestCallsPageRenders3WayConference(t *testing.T) {
	env := setupCallsTestServer(t)
	srv := env.srv
	database := env.database
	tracker := env.tracker
	authStore := env.authStore
	householdStore := env.householdStore

	user, err := authStore.CreateUser(context.Background(), "e2e-calls-conf@example.com", "Conf User", nil)
	if err != nil {
		user, err = authStore.GetUserByEmail(context.Background(), "e2e-calls-conf@example.com")
		if err != nil {
			t.Fatalf("create/get user: %v", err)
		}
	}
	markUserOnboarded(t, authStore, user.ID)

	hh, err := householdStore.Create(context.Background(), "Conf Family", user.ID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	if err := householdStore.SetCallHistoryEnabled(context.Background(), hh.ID, true); err != nil {
		t.Fatalf("enable call history: %v", err)
	}

	pairingStore := pairing.NewStore(database.DB)
	phones := []string{"5556001", "5556002", "5556003"}
	hwIDs := []string{"e2e-conf-hw-1", "e2e-conf-hw-2", "e2e-conf-hw-3"}

	for i, num := range phones {
		code, err := pairingStore.GenerateCode(context.Background(), hwIDs[i])
		if err != nil {
			t.Fatalf("generate code %d: %v", i, err)
		}
		if _, _, err := pairingStore.ClaimDevice(context.Background(), code, num, "Phone "+num, hh.ID); err != nil {
			t.Fatalf("claim phone %d: %v", i, err)
		}
	}

	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM conference_members WHERE phone = ANY($1::text[])", "{5556001,5556002,5556003}")
		_, _ = database.DB.Exec("DELETE FROM conferences WHERE host_phone = ANY($1::text[])", "{5556001,5556002,5556003}")
		_, _ = database.DB.Exec("DELETE FROM calls WHERE caller = ANY($1::text[]) OR callee = ANY($1::text[])", "{5556001,5556002,5556003}")
		_, _ = database.DB.Exec("DELETE FROM devices WHERE hardware_id = ANY($1::text[])", "{e2e-conf-hw-1,e2e-conf-hw-2,e2e-conf-hw-3}")
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number = ANY($1::text[])", "{5556001,5556002,5556003}")
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE user_id = $1", user.ID)
		_, _ = database.DB.Exec("DELETE FROM households WHERE id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM sessions WHERE user_id = $1", user.ID)
		_, _ = database.DB.Exec("DELETE FROM users WHERE id = $1", user.ID)
	})

	// Create a 3-way call: initiate A->B, add-leg A->C, merge, then end.
	callID, err := tracker.OnCallInitiated(context.Background(), phones[0], phones[1])
	if err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	if _, err := tracker.OnCallInitiated(context.Background(), phones[0], phones[2]); err != nil {
		t.Fatalf("OnCallInitiated add-leg: %v", err)
	}
	conf, err := tracker.CreateConferencePersistent(context.Background(), phones[0], callID, []string{phones[1], phones[2]})
	if err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}
	if err := tracker.EndConferencePersistent(context.Background(), conf.ID, "host_hangup"); err != nil {
		t.Fatalf("EndConferencePersistent: %v", err)
	}

	token, _, err := authStore.CreateSession(context.Background(), user.ID, auth.SessionTTL)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	jar := &testCookieJar{cookie: &http.Cookie{Name: auth.CookieName, Value: token}, url: srv.URL}
	client := &http.Client{Jar: jar}

	resp, err := client.Get(srv.URL + "/calls")
	if err != nil {
		t.Fatalf("GET /calls: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var bodyBuf strings.Builder
	if _, err := io.Copy(&bodyBuf, resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := bodyBuf.String()

	// The page should contain the "3-way" chip.
	if !strings.Contains(body, "chip--conf") {
		t.Errorf("expected chip--conf class in body (3-way conference chip)\nbody snippet: %s", truncate(body, 800))
	}
	if !strings.Contains(body, "3-way") {
		t.Errorf("expected '3-way' text in body\nbody snippet: %s", truncate(body, 800))
	}

	// All three participant phone numbers should be visible.
	for _, num := range phones {
		display := line.FormatNumber(num)
		if !strings.Contains(body, display) {
			t.Errorf("expected phone %s in body\nbody snippet: %s", display, truncate(body, 800))
		}
	}

	// Merged pre-merge legs must NOT appear as plain call rows (no arrow between A and B
	// that is not inside a conference row). We verify this by checking that there is no
	// "→" separator in the body (which would indicate a 2-party row rendered), since all
	// pre-merge calls should be excluded.
	// Note: the mobile layout uses "→" for 2-party calls; conference rows do not.
	if strings.Contains(body, "→") {
		t.Errorf("expected no 2-party arrow (→) in body — merged legs should be hidden\nbody snippet: %s", truncate(body, 800))
	}

	t.Logf("✓ /calls page renders 3-way conference with chip--conf and all participant numbers")
}

func TestCallsPageConferenceRowLinksToLive(t *testing.T) {
	s := newLHEnv(t)
	ctx := context.Background()
	confID := startConference(t, s)
	// End the conference so it shows up in /calls history (active ones
	// aren't in the historical list).
	if err := s.env.tracker.EndConferencePersistent(ctx, confID, "host_hangup"); err != nil {
		t.Fatalf("end: %v", err)
	}

	// /calls needs CallHistoryEnabled. Enable it for userA's household.
	if err := s.env.householdStore.SetCallHistoryEnabled(ctx, s.hhAID, true); err != nil {
		t.Fatalf("enable call history: %v", err)
	}

	client := authedClient(t, s, s.userA)
	req, _ := http.NewRequest(http.MethodGet, s.env.srv.URL+"/calls", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", resp.StatusCode, string(body))
	}
	wantLink := `href="/conference/live/` + confID.String() + `"`
	if !strings.Contains(string(body), wantLink) {
		t.Errorf("expected /calls to link conference row to %s", wantLink)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
