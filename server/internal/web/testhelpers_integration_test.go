//go:build integration

package web

// Shared helpers for integration tests in package web. Every test file
// behind the `integration` build tag can call these.
//
//  setupHandler          - builds a *Handler with a real DB, no httptest server
//  setupTestServer       - setupHandler + httptest.NewServer for WS/HTTP round-trips
//  addSessionCookie      - creates test@example.com + a session cookie
//  setupAuthedHousehold  - addSessionCookie + a real household_members row
//                          (most protected handlers go through onboarding
//                          middleware which needs this)
//  seedE2EHousehold      - adds just the household row (older e2e_test style)
//  dialWS / sendMsg /
//  recvMsg / testCookieJar - HTTP/WS helpers for the e2e tests

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

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

// setupHandler builds a *Handler wired to a real Postgres (TEST_DATABASE_URL).
// It skips the test if the env var is unset, so `go test -tags=integration`
// is safe on a machine without a DB. Use when a test only needs the Handler
// value (e.g. calling a method directly); for tests that round-trip HTTP,
// prefer setupTestServer.
func setupHandler(t *testing.T) (*Handler, *db.Database, *auth.Store) {
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
	h, err := NewHandler(deps, HandlerConfig{Addr: ":8443"})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h, database, authStore
}

// setupTestServer builds the same Handler and wraps it in an httptest.Server.
// Returns the server plus the collaborators individual tests need to seed
// or assert against. The trailing *signaling.Hub return is used by WS tests
// that need to poll hub state via waitForRegister.
//
// PairingStore is intentionally nil here: the WS handler's register path
// emits a TypePairingCode message for any unpaired hardware_id when
// pairingStore is non-nil, which would sit in the test's ws read buffer
// ahead of the Ring/Answer/Error messages the e2e tests actually assert
// against. Tests that exercise the pairing flow should use a dedicated
// setup that seeds paired device rows.
func setupTestServer(t *testing.T) (*httptest.Server, *db.Database, *line.Store, *calls.Tracker, *auth.Store, *signaling.Hub) {
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
	deps.PairingStore = nil
	h, err := NewHandler(deps, HandlerConfig{Addr: ":0"})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	srv := httptest.NewServer(h.Router())
	t.Cleanup(srv.Close)
	return srv, database, deps.LineStore, deps.Tracker, authStore, deps.Hub
}

// waitForRegister polls hub.Get until the given number is registered or the
// deadline expires. Needed between two test phones on different WebSockets:
// each ws read goroutine handles its own register independently, so ws1 may
// start sending Call before ws2's register has run.
func waitForRegister(t *testing.T, hub *signaling.Hub, number string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hub.Get(number) != nil {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s to register", number)
}

// testDeps builds a Deps populated with real stores against the provided DB,
// suitable for integration tests. Callers that want to omit a particular
// collaborator can zero out the field after this returns.
//
// HealthStore is wired with the flusher disabled so tests don't need a
// separate lifecycle goroutine. Tests that want the real flusher running
// can swap the field for their own instance.
func testDeps(t *testing.T, database *db.Database) (Deps, *auth.Store) {
	t.Helper()
	lineStore := line.NewStore(database)
	deviceStore := device.NewStore(database)
	hub := signaling.NewHub()
	tracker := calls.New(database)
	healthStore := calls.NewHealthStore(database, calls.WithFlushDisabled(true))
	tracker.SetHealthStore(healthStore)
	relay := signaling.NewRelay(hub, tracker, nil, nil)
	relay.HealthStore = healthStore

	authStore := auth.NewStore(database.DB)
	householdStore := household.NewStore(database.DB)
	pairingStore := pairing.NewStore(database.DB)
	linkStore := household.NewLinkStore(database.DB)
	googleAuth := auth.NewGoogleAuth("", "", "", "", authStore)
	emailSender := email.NewNoopSender()
	loginTmpl, err := template.New("").ParseFS(TemplateFS(), "templates/layout-v2.html", "templates/_partials.html", "templates/login.html")
	if err != nil {
		t.Fatalf("parse login template: %v", err)
	}
	authHandlers := auth.NewHandlers(authStore, googleAuth, emailSender, "http://localhost", "", loginTmpl, false)

	return Deps{
		LineStore:      lineStore,
		DeviceStore:    deviceStore,
		Hub:            hub,
		Tracker:        tracker,
		Relay:          relay,
		HealthStore:    healthStore,
		AuthStore:      authStore,
		AuthHandlers:   authHandlers,
		GoogleAuth:     googleAuth,
		HouseholdStore: householdStore,
		PairingStore:   pairingStore,
		LinkStore:      linkStore,
		EmailSender:    emailSender,
	}, authStore
}

// addSessionCookie creates test@example.com on demand, opens a session, and
// returns a cookie suitable for attaching to authenticated requests.
func addSessionCookie(t *testing.T, store *auth.Store) *http.Cookie {
	t.Helper()
	user, err := store.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		user, err = store.CreateUser(context.Background(), "test@example.com", "Test User", nil)
		if err != nil {
			t.Fatalf("create test user: %v", err)
		}
	}
	token, _, err := store.CreateSession(context.Background(), user.ID, auth.SessionTTL)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return &http.Cookie{Name: auth.CookieName, Value: token}
}

// setupAuthedHousehold combines addSessionCookie with household creation so
// a test user has a household_members row. Onboarding middleware redirects
// any request without a household to /onboard, so every test that hits a
// protected route must seed one.
func setupAuthedHousehold(t *testing.T, h *Handler, database *db.Database, authStore *auth.Store) (*http.Cookie, *household.Household) {
	t.Helper()
	cookie := addSessionCookie(t, authStore)
	user, err := authStore.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		t.Fatalf("get test user: %v", err)
	}
	hh, err := h.householdStore.Create(context.Background(), "Handler Test Household", user.ID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE household_id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM households WHERE id = $1", hh.ID)
	})
	return cookie, hh
}

// setupLineWithConn adds a phone number to the given household, registers a
// fresh signaling.Conn for it on the handler's hub, and registers cleanup of
// both the lines row and the hub registration. Returns the line and the conn
// so callers can assert push payloads via conn.Send.
func setupLineWithConn(t *testing.T, h *Handler, database *db.Database, hh *household.Household, number, name string) (*line.Line, *signaling.Conn) {
	t.Helper()
	lineStore := line.NewStore(database)
	ln, err := lineStore.Add(context.Background(), number, name, hh.ID)
	if err != nil {
		t.Fatalf("add line %s: %v", number, err)
	}
	conn := &signaling.Conn{Send: make(chan []byte, 10)}
	h.Hub().Register(number, conn)
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM lines WHERE id = $1", ln.ID)
	})
	return ln, conn
}

// seedE2EHousehold is the older e2e_test variant of setupAuthedHousehold;
// kept around for tests that already depend on it.
func seedE2EHousehold(t *testing.T, database *db.Database, authStore *auth.Store) string {
	t.Helper()
	user, err := authStore.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		user, err = authStore.CreateUser(context.Background(), "test@example.com", "Test User", nil)
		if err != nil {
			t.Fatalf("create test user: %v", err)
		}
	}
	store := household.NewStore(database.DB)
	hh, err := store.Create(context.Background(), "E2E Test Household", user.ID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE household_id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM households WHERE id = $1", hh.ID)
	})
	return hh.ID
}

// dialWS opens a WebSocket against the test server's /ws endpoint.
func dialWS(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// sendMsg marshals a signaling.Message to JSON and writes it to conn.
func sendMsg(t *testing.T, conn *websocket.Conn, msg signaling.Message) {
	t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// recvMsg reads the next frame from conn with a 2s deadline and parses it
// as a signaling.Message.
func recvMsg(t *testing.T, conn *websocket.Conn) *signaling.Message {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	msg, err := signaling.ParseMessage(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return msg
}

// seedUnrelatedUser creates a fresh user + household + line owned by that
// household, registers cleanup, and returns the user. Use when a test needs
// a principal whose household owns no conference member line.
func seedUnrelatedUser(t *testing.T, env callsTestEnv, label string) *auth.User {
	t.Helper()
	num := nextPhone()
	hw := label + "-" + num
	email := label + "-" + num + "@example.com"
	hhName := label + " " + num
	t.Cleanup(func() {
		db := env.database.DB
		_, _ = db.Exec("DELETE FROM devices WHERE hardware_id = $1", hw)
		_, _ = db.Exec("DELETE FROM lines WHERE number = $1", num)
		_, _ = db.Exec("DELETE FROM household_members WHERE household_id IN (SELECT id FROM households WHERE name = $1)", hhName)
		_, _ = db.Exec("DELETE FROM households WHERE name = $1", hhName)
		_, _ = db.Exec("DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE email = $1)", email)
		_, _ = db.Exec("DELETE FROM users WHERE email = $1", email)
	})
	u, err := env.authStore.CreateUser(context.Background(), email, label, nil)
	if err != nil {
		t.Fatalf("seedUnrelatedUser %s: CreateUser: %v", label, err)
	}
	hh, err := env.householdStore.Create(context.Background(), hhName, u.ID)
	if err != nil {
		t.Fatalf("seedUnrelatedUser %s: Create household: %v", label, err)
	}
	code, err := env.pairingStore.GenerateCode(context.Background(), hw)
	if err != nil {
		t.Fatalf("seedUnrelatedUser %s: GenerateCode: %v", label, err)
	}
	if _, _, err := env.pairingStore.ClaimDevice(context.Background(), code, num, "Phone "+label, hh.ID); err != nil {
		t.Fatalf("seedUnrelatedUser %s: ClaimDevice: %v", label, err)
	}
	return u
}

// startConference seeds two 2-party calls (host->A, host->B where host is
// numA, A is numB, B is numC), then merges them into a conference. Returns
// the conference UUID. Caller owns cleanup via t.Cleanup on any rows
// created (newLHEnv already cleans up calls table).
func startConference(t *testing.T, s lhSetup) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	tr := s.env.tracker
	if _, err := tr.OnCallInitiated(ctx, s.numA, s.numB); err != nil {
		t.Fatalf("OnCallInitiated A->B: %v", err)
	}
	if _, err := tr.OnCallInitiated(ctx, s.numA, s.numC); err != nil {
		t.Fatalf("OnCallInitiated A->C: %v", err)
	}
	if err := tr.OnCallAnswered(ctx, s.numA, s.numB); err != nil {
		t.Fatalf("OnCallAnswered A->B: %v", err)
	}
	if err := tr.OnCallAnswered(ctx, s.numA, s.numC); err != nil {
		t.Fatalf("OnCallAnswered A->C: %v", err)
	}
	originatingCallID := tr.CallIDForPair(ctx, s.numA, s.numB)
	conf, err := tr.CreateConferencePersistent(ctx, s.numA, originatingCallID, []string{s.numB, s.numC})
	if err != nil {
		t.Fatalf("CreateConferencePersistent: %v", err)
	}
	return conf.ID
}

// testCookieJar is a minimal cookie jar that attaches a single cookie to all requests.
type testCookieJar struct {
	cookie *http.Cookie
	url    string
}

func (j *testCookieJar) SetCookies(u *neturl.URL, cookies []*http.Cookie) {}
func (j *testCookieJar) Cookies(u *neturl.URL) []*http.Cookie {
	return []*http.Cookie{j.cookie}
}
