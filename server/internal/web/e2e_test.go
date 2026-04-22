//go:build integration

package web

import (
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/device"
	"github.com/justinlindh/digits/server/internal/email"
	"github.com/justinlindh/digits/server/internal/household"
	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/signaling"
)

func setupTestServer(t *testing.T) (*httptest.Server, *db.Database, *line.Store, *calls.Tracker, *auth.Store) {
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
	googleAuth := auth.NewGoogleAuth("", "", "", "", authStore)
	emailSender := email.NewNoopSender()
	loginTmpl, err := template.New("").ParseFS(TemplateFS(), "templates/layout-v2.html", "templates/_partials.html", "templates/login.html")
	if err != nil {
		t.Fatalf("parse login template: %v", err)
	}
	authHandlers := auth.NewHandlers(authStore, googleAuth, emailSender, "http://localhost", "", loginTmpl, false)

	h, err := NewHandler(Deps{
		LineStore:      lineStore,
		DeviceStore:    deviceStore,
		Hub:            hub,
		Tracker:        tracker,
		Relay:          relay,
		AuthStore:      authStore,
		AuthHandlers:   authHandlers,
		GoogleAuth:     googleAuth,
		HouseholdStore: householdStore,
	}, HandlerConfig{Addr: ":0"})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	srv := httptest.NewServer(h.Router())
	t.Cleanup(srv.Close)
	return srv, database, lineStore, tracker, authStore
}

// seedE2EHousehold creates a household owned by the test user (test@example.com,
// created on demand) and returns its UUID. The onboarding middleware and the
// lines.household_id FK both require a real household membership; tests that
// insert lines or hit routes behind the onboarding gate must call this.
func seedE2EHousehold(t *testing.T, database *db.Database, authStore *auth.Store) string {
	t.Helper()
	user, err := authStore.GetUserByEmail("test@example.com")
	if err != nil {
		user, err = authStore.CreateUser("test@example.com", "Test User", nil)
		if err != nil {
			t.Fatalf("create test user: %v", err)
		}
	}
	store := household.NewStore(database.DB)
	hh, err := store.Create("E2E Test Household", user.ID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE household_id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM households WHERE id = $1", hh.ID)
	})
	return hh.ID
}

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

func TestE2EFullCallFlow(t *testing.T) {
	srv, database, lineStore, tracker, authStore := setupTestServer(t)
	// Create authenticated client for protected route checks
	cookie := addSessionCookie(t, authStore)
	jar := &testCookieJar{cookie: cookie, url: srv.URL}
	authedClient := &http.Client{Jar: jar}

	// Register lines under a real household; the lines.household_id FK
	// rejects bogus UUIDs and the /api/status call goes through the
	// onboarding middleware, which needs a household_members row.
	hhID := seedE2EHousehold(t, database, authStore)
	if _, err := lineStore.Add("3140001", "Phone A", hhID); err != nil {
		t.Fatalf("add line A: %v", err)
	}
	if _, err := lineStore.Add("3140002", "Phone B", hhID); err != nil {
		t.Fatalf("add line B: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number IN ('3140001', '3140002')")
	})

	// Connect two phones
	ws1 := dialWS(t, srv)
	ws2 := dialWS(t, srv)

	// Both register. The WS handler requires a non-empty hardware_id; pairing
	// enforcement is skipped here because setupTestServer wires a nil
	// pairingStore, so any stable string works.
	sendMsg(t, ws1, signaling.Message{Type: signaling.TypeRegister, Number: "3140001", HardwareID: "e2e-hw-a"})
	sendMsg(t, ws2, signaling.Message{Type: signaling.TypeRegister, Number: "3140002", HardwareID: "e2e-hw-b"})

	// Give server time to register
	time.Sleep(50 * time.Millisecond)

	// Phone 1 calls Phone 2
	sendMsg(t, ws1, signaling.Message{Type: signaling.TypeCall, To: "3140002"})

	// Phone 2 should receive ring
	ring := recvMsg(t, ws2)
	if ring.Type != signaling.TypeRing {
		t.Fatalf("expected ring, got %s", ring.Type)
	}
	if ring.From != "3140001" {
		t.Fatalf("expected ring from 3140001, got %s", ring.From)
	}

	// Phone 2 answers
	sendMsg(t, ws2, signaling.Message{Type: signaling.TypeAnswer, To: "3140001"})

	// Phone 1 receives answer
	answer := recvMsg(t, ws1)
	if answer.Type != signaling.TypeAnswer {
		t.Fatalf("expected answer, got %s", answer.Type)
	}

	// Exchange SDP
	sendMsg(t, ws1, signaling.Message{Type: signaling.TypeSDP, To: "3140002", SDP: "offer-sdp-data"})
	sdp := recvMsg(t, ws2)
	if sdp.Type != signaling.TypeSDP || sdp.SDP != "offer-sdp-data" {
		t.Fatalf("expected sdp offer, got: %+v", sdp)
	}

	// Exchange ICE
	sendMsg(t, ws2, signaling.Message{Type: signaling.TypeICE, To: "3140001", Candidate: "candidate-data"})
	ice := recvMsg(t, ws1)
	if ice.Type != signaling.TypeICE || ice.Candidate != "candidate-data" {
		t.Fatalf("expected ice, got: %+v", ice)
	}

	// Phone 1 hangs up
	sendMsg(t, ws1, signaling.Message{Type: signaling.TypeHangup, To: "3140002"})
	hangup := recvMsg(t, ws2)
	if hangup.Type != signaling.TypeHangup {
		t.Fatalf("expected hangup, got %s", hangup.Type)
	}

	// Give tracker time to update DB
	time.Sleep(50 * time.Millisecond)

	// Verify call appears in history via HTTP (authenticated)
	resp, err := authedClient.Get(srv.URL + "/api/status")
	if err != nil {
		t.Fatalf("api/status: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Verify call in tracker history
	recentCalls, err := tracker.Recent(10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(recentCalls) == 0 {
		t.Fatal("expected at least 1 call in history")
	}
	found := false
	for _, c := range recentCalls {
		if c.Caller == "3140001" && c.Callee == "3140002" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("call 3140001→3140002 not found in history: %+v", recentCalls)
	}
}

func TestE2ECallToOfflinePhone(t *testing.T) {
	srv, _, _, _, _ := setupTestServer(t)

	ws1 := dialWS(t, srv)
	sendMsg(t, ws1, signaling.Message{Type: signaling.TypeRegister, Number: "3140001", HardwareID: "e2e-offline-a"})
	time.Sleep(30 * time.Millisecond)

	// Call a phone that's not connected
	sendMsg(t, ws1, signaling.Message{Type: signaling.TypeCall, To: "3140099"})

	errMsg := recvMsg(t, ws1)
	if errMsg.Type != signaling.TypeError {
		t.Fatalf("expected error, got %s", errMsg.Type)
	}
}

func TestE2ENoRegisterRejected(t *testing.T) {
	srv, _, _, _, _ := setupTestServer(t)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send a non-register message first
	sendMsg(t, conn, signaling.Message{Type: signaling.TypeCall, To: "3140002"})

	// Should receive error and connection close
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		// Connection closed is also acceptable
		return
	}
	msg, _ := signaling.ParseMessage(data)
	if msg != nil && msg.Type != signaling.TypeError {
		t.Fatalf("expected error message, got: %+v", msg)
	}
}

func TestE2EWebUIWithData(t *testing.T) {
	srv, database, lineStore, _, authStore := setupTestServer(t)

	hhID := seedE2EHousehold(t, database, authStore)
	if _, err := lineStore.Add("3140001", "Kitchen", hhID); err != nil {
		t.Fatalf("add kitchen line: %v", err)
	}
	if _, err := lineStore.Add("3140002", "Bedroom", hhID); err != nil {
		t.Fatalf("add bedroom line: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number IN ('3140001', '3140002')")
	})

	// Create a session cookie for authenticated requests
	cookie := addSessionCookie(t, authStore)
	jar := &testCookieJar{cookie: cookie, url: srv.URL}
	client := &http.Client{Jar: jar}

	// Dashboard should show phone count
	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Phones page should list phones
	resp2, err := client.Get(srv.URL + "/phones")
	if err != nil {
		t.Fatalf("GET /phones: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	var bodyBuf strings.Builder
	_, _ = io.Copy(&bodyBuf, resp2.Body)
	body := bodyBuf.String()
	if !strings.Contains(body, "3140001") || !strings.Contains(body, "3140002") {
		t.Fatalf("phones page missing registered phones")
	}
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
