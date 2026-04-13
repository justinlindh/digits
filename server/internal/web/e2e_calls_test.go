//go:build integration

package web

import (
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/device"
	"github.com/justinlindh/digits/server/internal/email"
	"github.com/justinlindh/digits/server/internal/household"
	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/pairing"
	"github.com/justinlindh/digits/server/internal/signaling"
)

// setupCallsTestServer creates a full server with householdStore wired in,
// which is required by handleCalls.
func setupCallsTestServer(t *testing.T) (*httptest.Server, *db.Database, *line.Store, *calls.Tracker, *auth.Store, *household.Store) {
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

	lineStore := line.NewStore(database)
	deviceStore := device.NewStore(database)
	hub := signaling.NewHub()
	tracker := calls.New(database)
	relay := signaling.NewRelay(hub, tracker, nil, nil)

	authStore := auth.NewStoreFromDB(database.DB)
	householdStore := household.NewStore(database.DB)
	pairingStore := pairing.NewStore(database.DB)

	googleAuth := auth.NewGoogleAuth("", "", "", "", authStore)
	emailSender := email.NewNoopSender()
	loginTmpl, err := template.New("").ParseFS(TemplateFS(), "templates/layout.html", "templates/login.html")
	if err != nil {
		t.Fatalf("parse login template: %v", err)
	}
	authHandlers := auth.NewHandlers(authStore, googleAuth, emailSender, "http://localhost", "", loginTmpl, false)

	h, err := NewHandler(lineStore, deviceStore, hub, tracker, relay, HandlerConfig{
		Addr:        ":0",
	}, authStore, authHandlers, googleAuth, householdStore, pairingStore, nil, emailSender, "", "")
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	srv := httptest.NewServer(h.Router())
	t.Cleanup(srv.Close)
	return srv, database, lineStore, tracker, authStore, householdStore
}

// TestCallsPageScopedToHousehold verifies that the /calls page only shows
// calls involving phones belonging to the authenticated user's household.
func TestCallsPageScopedToHousehold(t *testing.T) {
	// === Step 1: Set up test server with householdStore wired in ===
	srv, database, _, tracker, authStore, householdStore := setupCallsTestServer(t)

	// === Step 2: Create two users in two separate households ===
	userA, err := authStore.CreateUser("e2e-calls-a@example.com", "Calls User A", nil)
	if err != nil {
		userA, err = authStore.GetUserByEmail("e2e-calls-a@example.com")
		if err != nil {
			t.Fatalf("create/get user A: %v", err)
		}
	}
	userB, err := authStore.CreateUser("e2e-calls-b@example.com", "Calls User B", nil)
	if err != nil {
		userB, err = authStore.GetUserByEmail("e2e-calls-b@example.com")
		if err != nil {
			t.Fatalf("create/get user B: %v", err)
		}
	}

	hhA, err := householdStore.Create("Calls Family A", userA.ID)
	if err != nil {
		t.Fatalf("create household A: %v", err)
	}
	hhB, err := householdStore.Create("Calls Family B", userB.ID)
	if err != nil {
		t.Fatalf("create household B: %v", err)
	}
	// handleCalls redirects to /settings when call history is disabled (the
	// default). We care about scoping the rendered list, not the feature gate.
	if err := householdStore.SetCallHistoryEnabled(hhA.ID, true); err != nil {
		t.Fatalf("enable call history A: %v", err)
	}

	// === Step 3: Pair phones into each household ===
	pairingStore := pairing.NewStore(database.DB)
	hwA := "e2e-calls-hw-a"
	hwB := "e2e-calls-hw-b"
	phoneNumA := "5550001"
	phoneNumB := "5550002"

	codeA, err := pairingStore.GenerateCode(hwA)
	if err != nil {
		t.Fatalf("generate code A: %v", err)
	}
	if _, _, err := pairingStore.ClaimDevice(codeA, phoneNumA, "Phone A", hhA.ID); err != nil {
		t.Fatalf("claim phone A: %v", err)
	}

	codeB, err := pairingStore.GenerateCode(hwB)
	if err != nil {
		t.Fatalf("generate code B: %v", err)
	}
	if _, _, err := pairingStore.ClaimDevice(codeB, phoneNumB, "Phone B", hhB.ID); err != nil {
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
	if err := tracker.OnCallInitiated(phoneNumA, "5559999"); err != nil {
		t.Fatalf("initiate call A: %v", err)
	}
	if err := tracker.OnCallEnded(phoneNumA, "5559999"); err != nil {
		t.Fatalf("end call A: %v", err)
	}

	// Call B: household-B phone calls a different external number
	if err := tracker.OnCallInitiated(phoneNumB, "5558888"); err != nil {
		t.Fatalf("initiate call B: %v", err)
	}
	if err := tracker.OnCallEnded(phoneNumB, "5558888"); err != nil {
		t.Fatalf("end call B: %v", err)
	}

	// === Step 5: Create a session for userA ===
	tokenA, _, err := authStore.CreateSession(userA.ID, auth.SessionTTL)
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
