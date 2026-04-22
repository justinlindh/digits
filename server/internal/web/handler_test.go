//go:build integration

package web

import (
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

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

	lineStore := line.NewStore(database)
	deviceStore := device.NewStore(database)
	hub := signaling.NewHub()
	tracker := calls.New(database)
	relay := signaling.NewRelay(hub, tracker, nil, nil)

	authStore := auth.NewStoreFromDB(database.DB)
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

	h, err := NewHandler(lineStore, deviceStore, hub, tracker, relay, HandlerConfig{
		Addr:        ":8443",
	}, authStore, authHandlers, googleAuth, householdStore, pairingStore, linkStore, emailSender, "", "", nil)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h, database, authStore
}

// addSessionCookie creates a test user and session, returning a cookie for authenticated requests.
func addSessionCookie(t *testing.T, store *auth.Store) *http.Cookie {
	t.Helper()
	user, err := store.GetUserByEmail("test@example.com")
	if err != nil {
		user, err = store.CreateUser("test@example.com", "Test User", nil)
		if err != nil {
			t.Fatalf("create test user: %v", err)
		}
	}
	token, _, err := store.CreateSession(user.ID, auth.SessionTTL)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return &http.Cookie{
		Name:  auth.CookieName,
		Value: token,
	}
}

// setupAuthedHousehold combines addSessionCookie with household creation so
// a test user has a household_members row. Onboarding middleware redirects
// any request without a household to /onboard, so every test that hits a
// protected route must seed one. Returns the session cookie and the created
// household. Cleanup for the household row and its member row is registered
// via t.Cleanup.
func setupAuthedHousehold(t *testing.T, h *Handler, database *db.Database, authStore *auth.Store) (*http.Cookie, *household.Household) {
	t.Helper()
	cookie := addSessionCookie(t, authStore)
	user, err := authStore.GetUserByEmail("test@example.com")
	if err != nil {
		t.Fatalf("get test user: %v", err)
	}
	hh, err := h.householdStore.Create("Handler Test Household", user.ID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE household_id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM households WHERE id = $1", hh.ID)
	})
	return cookie, hh
}

func TestDashboardReturns200(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, hh.Name) {
		t.Errorf("dashboard missing household name %q", hh.Name)
	}
}

func TestPhonesPageReturns200(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)
	req := httptest.NewRequest(http.MethodGet, "/phones", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestPhonesPage_PairPanelTitle(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)
	req := httptest.NewRequest(http.MethodGet, "/phones", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "Pair a new handset") {
		t.Errorf("phones page missing new pair panel title")
	}
	if strings.Contains(body, "Pair a device") {
		t.Errorf("phones page still shows old pair panel title")
	}
}

func TestPhonesPage_HandsetNameField(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)
	req := httptest.NewRequest(http.MethodGet, "/phones", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	body := w.Body.String()
	for _, want := range []string{
		"Handset name",
		"Kitchen · Grandma&#39;s bedroom · Garage",
		"Most families name handsets by where they live",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("phones page missing %q", want)
		}
	}
	if strings.Contains(body, ">Line name<") {
		t.Errorf("phones page still shows old 'Line name' label")
	}
}

func TestLinksPage_InviteFriendButton(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)
	req := httptest.NewRequest(http.MethodGet, "/links", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "Invite a friend") {
		t.Errorf("links page missing new invite CTA")
	}
	if strings.Contains(body, "Generate invite code") {
		t.Errorf("links page still shows old CTA")
	}
}

func TestAddPhoneViaHTMX(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)
	form := url.Values{"number": {"3140001"}, "name": {"Test Phone"}}
	req := httptest.NewRequest(http.MethodPost, "/phones", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "3140001") {
		t.Errorf("response missing phone number")
	}
}

func TestAddPhoneInvalidNumber(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)
	form := url.Values{"number": {"123"}, "name": {"Bad Phone"}}
	req := httptest.NewRequest(http.MethodPost, "/phones", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	// Should return the table with an error message (still 200 for htmx)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestDeletePhone(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	lineStore := line.NewStore(database)
	_, err := lineStore.Add("3140001", "Test Phone", hh.ID)
	if err != nil {
		t.Fatalf("add test line: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number = '3140001'")
	})

	req := httptest.NewRequest(http.MethodPost, "/phones/3140001/delete", nil)
	req.Header.Set("HX-Request", "true")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	// Line should be gone
	if _, err := lineStore.GetByNumber("3140001"); err == nil {
		t.Error("line should have been deleted")
	}
}

func TestCallsPageReturns200(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	if err := h.householdStore.SetCallHistoryEnabled(hh.ID, true); err != nil {
		t.Fatalf("enable call history: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/calls", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestSettingsPageReturns200(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Settings") {
		t.Errorf("settings page missing expected content")
	}
}

func TestAPIStatusReturnsJSON(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Errorf("expected JSON content type")
	}
}

func TestSettingsTimezonePost(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)

	user, _ := authStore.GetUserByEmail("test@example.com")
	householdStore := h.householdStore
	hh, err := householdStore.Create("TZ Test Family", user.ID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE household_id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM households WHERE id = $1", hh.ID)
	})

	form := url.Values{"timezone": {"America/Chicago"}}
	req := httptest.NewRequest(http.MethodPost, "/settings/timezone", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/settings?saved=1" {
		t.Errorf("redirect location = %q, want /settings?saved=1", loc)
	}

	got, err := householdStore.GetByID(hh.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Timezone != "America/Chicago" {
		t.Errorf("timezone = %q, want America/Chicago", got.Timezone)
	}
}

func TestSettingsTimezonePost_Invalid(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)

	user, _ := authStore.GetUserByEmail("test@example.com")
	householdStore := h.householdStore
	hh, err := householdStore.Create("TZ Invalid Family", user.ID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE household_id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM households WHERE id = $1", hh.ID)
	})

	form := url.Values{"timezone": {"Fake/Zone"}}
	req := httptest.NewRequest(http.MethodPost, "/settings/timezone", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", w.Code)
	}

	got, err := householdStore.GetByID(hh.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Timezone != "UTC" {
		t.Errorf("timezone = %q, want UTC (invalid should be rejected)", got.Timezone)
	}
}

func TestNotFound(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)
	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	// Unauthenticated: redirects to login; authenticated: the dashboard handler returns 404 for unknown paths
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}


// setupPairedDevice creates a paired device via the pairing flow and returns
// the hardware ID and plaintext device token.
func setupPairedDevice(t *testing.T, database *db.Database, pairingStore *pairing.Store, householdStore *household.Store, authStore *auth.Store) (hardwareID, token string) {
	t.Helper()

	hardwareID = fmt.Sprintf("test-hw-%d", time.Now().UnixNano())
	number := fmt.Sprintf("99%05d", time.Now().UnixNano()%100000)

	// Create a household for the line
	user, err := authStore.GetUserByEmail("test@example.com")
	if err != nil {
		user, err = authStore.CreateUser("test@example.com", "Test User", nil)
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
	}
	hh, err := householdStore.Create("Test Household", user.ID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}

	// Generate pairing code (creates the device row)
	code, err := pairingStore.GenerateCode(hardwareID)
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}

	// Claim the device (pairs it, sets hashed token)
	token, _, err = pairingStore.ClaimDevice(code, number, "Test Phone", hh.ID)
	if err != nil {
		t.Fatalf("claim device: %v", err)
	}

	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM devices WHERE hardware_id = $1", hardwareID)
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number = $1", number)
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE household_id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM households WHERE id = $1", hh.ID)
	})

	return hardwareID, token
}

func TestPhoneRestartOnline(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)

	_, _ = database.DB.Exec(`INSERT INTO lines (number, name, household_id) VALUES ('3140001', 'Test Phone', $1) ON CONFLICT DO NOTHING`, hh.ID)
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number = '3140001'")
	})

	conn := &signaling.Conn{Send: make(chan []byte, 10)}
	h.Hub().Register("3140001", conn)

	form := url.Values{"mode": {"service"}}
	req := httptest.NewRequest("POST", "/phones/3140001/restart", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	select {
	case data := <-conn.Send:
		msg, _ := signaling.ParseMessage(data)
		if msg.Type != signaling.TypeRestart {
			t.Fatalf("expected restart message, got %s", msg.Type)
		}
		if msg.RestartMode != "service" {
			t.Fatalf("expected mode=service, got %s", msg.RestartMode)
		}
	default:
		t.Fatal("device did not receive restart message")
	}
}

func TestPhonesPage_FirmwareColumn(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	// Seed one line so the lines table renders (the Firmware column header
	// only appears when .Lines is non-empty).
	_, err := h.lineStore.Add("2456390", "Test Line", hh.ID)
	if err != nil {
		t.Fatalf("add line: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/phones", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, ">Firmware<") {
		t.Errorf("phones table missing Firmware column header")
	}
}

func TestPhoneRestartOffline(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)

	_, _ = database.DB.Exec(`INSERT INTO lines (number, name, household_id) VALUES ('3140001', 'Test Phone', $1) ON CONFLICT DO NOTHING`, hh.ID)
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number = '3140001'")
	})

	form := url.Values{"mode": {"service"}}
	req := httptest.NewRequest("POST", "/phones/3140001/restart", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != 502 {
		t.Fatalf("expected 502 for offline device, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPhoneRestartInvalidMode(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)

	_, _ = database.DB.Exec(`INSERT INTO lines (number, name, household_id) VALUES ('3140001', 'Test Phone', $1) ON CONFLICT DO NOTHING`, hh.ID)
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number = '3140001'")
	})

	conn := &signaling.Conn{Send: make(chan []byte, 10)}
	h.Hub().Register("3140001", conn)

	form := url.Values{"mode": {"explode"}}
	req := httptest.NewRequest("POST", "/phones/3140001/restart", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400 for invalid mode, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPhoneOnlineStatus(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)

	_, _ = database.DB.Exec(`INSERT INTO lines (number, name, household_id) VALUES ('3140001', 'Test Phone', $1) ON CONFLICT DO NOTHING`, hh.ID)
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number = '3140001'")
	})

	req := httptest.NewRequest("GET", "/phones/3140001/online", nil)
	req.Header.Set("Accept", "application/json")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"online":false`) {
		t.Fatalf("expected online:false, got %s", w.Body.String())
	}

	conn := &signaling.Conn{Send: make(chan []byte, 10)}
	h.Hub().Register("3140001", conn)

	req = httptest.NewRequest("GET", "/phones/3140001/online", nil)
	req.Header.Set("Accept", "application/json")
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"online":true`) {
		t.Fatalf("expected online:true, got %s", w.Body.String())
	}
}

// setupVoiceStyleLine creates the test user's household, inserts a line with
// the default voice_style, and registers cleanup. Returns a function that
// reads the current voice_style straight from the DB so tests can assert
// final state without reaching into the line store abstraction.
func setupVoiceStyleLine(t *testing.T, h *Handler, database *db.Database, authStore *auth.Store) (readVoiceStyle func() string) {
	t.Helper()
	user, err := authStore.GetUserByEmail("test@example.com")
	if err != nil {
		t.Fatalf("get test user: %v", err)
	}
	hh, err := h.householdStore.Create("Voice Style Test", user.ID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	lineStore := line.NewStore(database)
	if _, err := lineStore.Add("3140001", "Test Phone", hh.ID); err != nil {
		t.Fatalf("add line: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number = '3140001'")
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE household_id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM households WHERE id = $1", hh.ID)
	})
	return func() string {
		var raw string
		// COALESCE to match the server-side default: an absent voice_style
		// key (fresh line, never edited) reads back as copper.
		if err := database.DB.QueryRow(`SELECT COALESCE(settings->>'voice_style', 'copper') FROM lines WHERE number = '3140001'`).Scan(&raw); err != nil {
			t.Fatalf("read voice_style: %v", err)
		}
		return raw
	}
}

func postVoiceStyle(t *testing.T, h *Handler, cookie *http.Cookie, value string, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"voice_style": {value}}
	req := httptest.NewRequest(http.MethodPost, "/phones/3140001/voice-style", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	return w
}

func TestPhoneVoiceStyleEmptyReturns400(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	read := setupVoiceStyleLine(t, h, database, authStore)

	w := postVoiceStyle(t, h, cookie, "", false)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty voice_style, got %d: %s", w.Code, w.Body.String())
	}
	// Must not have written anything — default is copper on insert.
	if got := read(); got != "copper" {
		t.Fatalf("expected voice_style untouched (copper), got %q", got)
	}
}

func TestPhoneVoiceStyleUpdatePersistsAndRedirects(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	read := setupVoiceStyleLine(t, h, database, authStore)

	w := postVoiceStyle(t, h, cookie, "modern", false)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/phones/3140001" {
		t.Fatalf("expected redirect to /phones/3140001, got %q", loc)
	}
	if got := read(); got != "modern" {
		t.Fatalf("expected voice_style=modern in db, got %q", got)
	}
}

func TestPhoneVoiceStyleHTMXReturnsPartialWithSelection(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	w := postVoiceStyle(t, h, cookie, "modern", true)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="voice-style-section"`) {
		t.Fatalf("htmx response missing voice-style-section wrapper:\n%s", body)
	}
	// The newly-selected radio must carry `checked`, the other must not.
	modernIdx := strings.Index(body, `value="modern"`)
	copperIdx := strings.Index(body, `value="copper"`)
	if modernIdx < 0 || copperIdx < 0 {
		t.Fatalf("htmx response missing radios:\n%s", body)
	}
	// Walk forward from each input to the end of its tag to check for `checked`.
	modernTag := body[modernIdx:strings.Index(body[modernIdx:], ">")+modernIdx]
	copperTag := body[copperIdx:strings.Index(body[copperIdx:], ">")+copperIdx]
	if !strings.Contains(modernTag, "checked") {
		t.Errorf("modern radio not marked checked after save: %q", modernTag)
	}
	if strings.Contains(copperTag, "checked") {
		t.Errorf("copper radio still marked checked after switching to modern: %q", copperTag)
	}
}

func TestPhoneVoiceStyleUnknownValueNormalizesToCopper(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	read := setupVoiceStyleLine(t, h, database, authStore)

	// First move the line away from the default so we can observe the coercion.
	if w := postVoiceStyle(t, h, cookie, "modern", false); w.Code != http.StatusSeeOther {
		t.Fatalf("seed modern failed: %d %s", w.Code, w.Body.String())
	}
	if got := read(); got != "modern" {
		t.Fatalf("seed expected modern, got %q", got)
	}

	w := postVoiceStyle(t, h, cookie, "disco", false)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 for unknown value (normalized), got %d: %s", w.Code, w.Body.String())
	}
	if got := read(); got != "copper" {
		t.Fatalf("expected unknown value to normalize to copper, got %q", got)
	}
}

func readSilentMode(t *testing.T, database *db.Database) bool {
	t.Helper()
	var raw bool
	if err := database.DB.QueryRow(
		`SELECT COALESCE((settings->>'silent_mode')::bool, false) FROM lines WHERE number = '3140001'`,
	).Scan(&raw); err != nil {
		t.Fatalf("read silent_mode: %v", err)
	}
	return raw
}

func postSilentMode(t *testing.T, h *Handler, cookie *http.Cookie, value string, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"silent_mode": {value}}
	req := httptest.NewRequest(http.MethodPost, "/phones/3140001/silent-mode", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	return w
}

func TestPhoneSilentModeOnPersistsAndRedirects(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	w := postSilentMode(t, h, cookie, "on", false)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/phones/3140001" {
		t.Fatalf("expected redirect to /phones/3140001, got %q", loc)
	}
	if !readSilentMode(t, database) {
		t.Fatal("expected silent_mode=true in db")
	}
}

func TestPhoneSilentModeOffPersists(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	if w := postSilentMode(t, h, cookie, "on", false); w.Code != http.StatusSeeOther {
		t.Fatalf("setup on: got %d", w.Code)
	}
	if !readSilentMode(t, database) {
		t.Fatal("setup failed: expected silent_mode=true before turning off")
	}
	if w := postSilentMode(t, h, cookie, "off", false); w.Code != http.StatusSeeOther {
		t.Fatalf("off: got %d", w.Code)
	}
	if readSilentMode(t, database) {
		t.Fatal("expected silent_mode=false in db after turning off")
	}
}

func TestPhoneSilentModeMissingFieldTreatedAsOff(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	if w := postSilentMode(t, h, cookie, "on", false); w.Code != http.StatusSeeOther {
		t.Fatalf("setup on: got %d", w.Code)
	}

	form := url.Values{}
	req := httptest.NewRequest(http.MethodPost, "/phones/3140001/silent-mode", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}
	if readSilentMode(t, database) {
		t.Fatal("expected silent_mode=false when form omits the key")
	}
}

func TestPhoneSilentModeHTMXReturnsPartial(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	w := postSilentMode(t, h, cookie, "on", true)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="silent-mode-section"`) {
		t.Fatalf("htmx response missing silent-mode-section wrapper:\n%s", body)
	}
	if !strings.Contains(body, `name="silent_mode"`) {
		t.Fatalf("htmx response missing silent_mode input:\n%s", body)
	}
	checkboxIdx := strings.Index(body, `name="silent_mode"`)
	if checkboxIdx < 0 {
		t.Fatalf("missing checkbox:\n%s", body)
	}
	tag := body[checkboxIdx:strings.Index(body[checkboxIdx:], ">")+checkboxIdx]
	if !strings.Contains(tag, "checked") {
		t.Errorf("expected checkbox checked after turning on: %q", tag)
	}
}

func TestPhoneSilentModeMissingLineReturns404(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	user, err := authStore.GetUserByEmail("test@example.com")
	if err != nil {
		t.Fatalf("get test user: %v", err)
	}
	hh, err := h.householdStore.Create("Silent Mode Missing", user.ID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE household_id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM households WHERE id = $1", hh.ID)
	})

	w := postSilentMode(t, h, cookie, "on", false)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing line, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPhoneSilentModePushesToConnectedDevice(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	conn := &signaling.Conn{Send: make(chan []byte, 10)}
	h.Hub().Register("3140001", conn)

	if w := postSilentMode(t, h, cookie, "on", false); w.Code != http.StatusSeeOther {
		t.Fatalf("save failed: %d %s", w.Code, w.Body.String())
	}

	select {
	case data := <-conn.Send:
		msg, err := signaling.ParseMessage(data)
		if err != nil {
			t.Fatalf("parse pushed message: %v", err)
		}
		if msg.Type != signaling.TypeLineSettings {
			t.Fatalf("expected %s push, got %s", signaling.TypeLineSettings, msg.Type)
		}
		if msg.LineSettings == nil || !msg.LineSettings.SilentMode {
			t.Fatalf("expected silent_mode=true line_settings, got %+v", msg.LineSettings)
		}
	case <-time.After(time.Second):
		t.Fatal("device did not receive line_settings push")
	}
}

func TestPhoneSilentModeNoOpSkipsPush(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	conn := &signaling.Conn{Send: make(chan []byte, 10)}
	h.Hub().Register("3140001", conn)

	// Line defaults to silent_mode=false on insert. Saving false again must be a no-op.
	if w := postSilentMode(t, h, cookie, "off", false); w.Code != http.StatusSeeOther {
		t.Fatalf("save failed: %d %s", w.Code, w.Body.String())
	}
	select {
	case data := <-conn.Send:
		t.Fatalf("expected no push on no-op save, got message: %s", string(data))
	case <-time.After(100 * time.Millisecond):
		// Expected: no push.
	}
}

func TestPhoneVoiceStyleMissingLineReturns404(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	user, err := authStore.GetUserByEmail("test@example.com")
	if err != nil {
		t.Fatalf("get test user: %v", err)
	}
	hh, err := h.householdStore.Create("Voice Style Missing", user.ID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE household_id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM households WHERE id = $1", hh.ID)
	})

	w := postVoiceStyle(t, h, cookie, "modern", false)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing line, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPhoneVoiceStylePushesToConnectedDevice(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	conn := &signaling.Conn{Send: make(chan []byte, 10)}
	h.Hub().Register("3140001", conn)

	if w := postVoiceStyle(t, h, cookie, "modern", false); w.Code != http.StatusSeeOther {
		t.Fatalf("save failed: %d %s", w.Code, w.Body.String())
	}

	select {
	case data := <-conn.Send:
		msg, err := signaling.ParseMessage(data)
		if err != nil {
			t.Fatalf("parse pushed message: %v", err)
		}
		if msg.Type != signaling.TypeLineSettings {
			t.Fatalf("expected %s push, got %s", signaling.TypeLineSettings, msg.Type)
		}
		if msg.LineSettings == nil || msg.LineSettings.VoiceStyle != "modern" {
			t.Fatalf("expected modern line_settings, got %+v", msg.LineSettings)
		}
	case <-time.After(time.Second):
		t.Fatal("device did not receive line_settings push")
	}
}

func TestPhoneVoiceStyleNoOpSkipsPush(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	conn := &signaling.Conn{Send: make(chan []byte, 10)}
	h.Hub().Register("3140001", conn)

	// Line defaults to copper on insert — saving copper again must be a no-op.
	if w := postVoiceStyle(t, h, cookie, "copper", false); w.Code != http.StatusSeeOther {
		t.Fatalf("save failed: %d %s", w.Code, w.Body.String())
	}
	select {
	case data := <-conn.Send:
		t.Fatalf("expected no push on no-op save, got message: %s", string(data))
	case <-time.After(100 * time.Millisecond):
		// Expected: no push.
	}
}

func TestWSRegister_MissingHardwareID(t *testing.T) {
	h, _, _ := setupHandler(t)
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	ws := dialWS(t, srv)
	sendMsg(t, ws, signaling.Message{
		Type:   signaling.TypeRegister,
		Number: "1001",
	})

	msg := recvMsg(t, ws)
	if msg.Type != signaling.TypeError {
		t.Fatalf("expected error message, got %q", msg.Type)
	}
	if msg.Error != "hardware_id required" {
		t.Errorf("expected 'hardware_id required', got %q", msg.Error)
	}
}

func TestWSRegister_PairedDevice_MissingToken(t *testing.T) {
	h, database, authStore := setupHandler(t)
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	hwID, _ := setupPairedDevice(t, database, h.pairingStore, h.householdStore, authStore)

	ws := dialWS(t, srv)
	sendMsg(t, ws, signaling.Message{
		Type:       signaling.TypeRegister,
		Number:     "1001",
		HardwareID: hwID,
	})

	msg := recvMsg(t, ws)
	if msg.Type != signaling.TypeError {
		t.Fatalf("expected error message, got %q", msg.Type)
	}
	if msg.Error != "device_token required" {
		t.Errorf("expected 'device_token required', got %q", msg.Error)
	}
}

func TestWSRegister_PairedDevice_WrongToken(t *testing.T) {
	h, database, authStore := setupHandler(t)
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	hwID, _ := setupPairedDevice(t, database, h.pairingStore, h.householdStore, authStore)

	ws := dialWS(t, srv)
	sendMsg(t, ws, signaling.Message{
		Type:        signaling.TypeRegister,
		Number:      "1001",
		HardwareID:  hwID,
		DeviceToken: "wrong-token-value",
	})

	msg := recvMsg(t, ws)
	if msg.Type != signaling.TypeError {
		t.Fatalf("expected error message, got %q", msg.Type)
	}
	if msg.Error != "invalid device_token" {
		t.Errorf("expected 'invalid device_token', got %q", msg.Error)
	}
}

func TestWSRegister_PairedDevice_CorrectToken(t *testing.T) {
	h, database, authStore := setupHandler(t)
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	hwID, token := setupPairedDevice(t, database, h.pairingStore, h.householdStore, authStore)

	ws := dialWS(t, srv)
	sendMsg(t, ws, signaling.Message{
		Type:        signaling.TypeRegister,
		Number:      "1001",
		HardwareID:  hwID,
		DeviceToken: token,
	})

	// The connection should stay open. If there was an auth error, we'd get
	// an error message. Try reading with a short deadline; no error message
	// means auth succeeded.
	_ = ws.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, err := ws.ReadMessage()
	if err == nil {
		t.Fatal("expected no message (timeout), but got one")
	}
	// A timeout (deadline exceeded) means no error was sent, which is success.
	if !strings.Contains(err.Error(), "i/o timeout") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
}

func TestCallsPage_CallLogTitle(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	if err := h.householdStore.SetCallHistoryEnabled(hh.ID, true); err != nil {
		t.Fatalf("enable call history: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/calls", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, ">Call log<") {
		t.Errorf("calls page missing 'Call log' heading")
	}
	if strings.Contains(body, ">Call history<") {
		t.Errorf("calls page still has old 'Call history' heading")
	}
}

func TestSettingsPage_CallLogLabel(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "Call log") {
		t.Errorf("settings page missing 'Call log'")
	}
}

func TestSettingsPage_StickyNav(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	body := w.Body.String()
	for _, want := range []string{
		`class="settings-layout"`,
		`class="settings-nav"`,
		`href="#account"`,
		`href="#theme"`,
		`href="#privacy"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page missing %q", want)
		}
	}
}

func TestSettingsPage_ThemeSwatches(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	body := w.Body.String()
	for _, want := range []string{
		`class="theme-card`,
		`theme-card__swatch`,
		`#f5f1ea`,
		`#2e231b`,
		`#c48b3a`,
		// Dialup swatches render actual --dialup-chrome-l / --dialup-blue-dark
		// / --dialup-gold tokens so the preview matches the live theme.
		`#ece9d8`,
		`#003da7`,
		`#ffcc00`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("settings theme section missing %q", want)
		}
	}
}

func TestSettingsPage_PrivacyCopy(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "kids deserve the same phone privacy you grew up with") {
		t.Errorf("settings privacy copy not updated")
	}
	if strings.Contains(body, "kids deserve the same phone privacy you had growing up") {
		t.Errorf("settings page still shows old privacy copy")
	}
}

// seedLinkedFamily creates a second household with one line and links it to the
// primary household via an invite/accept handshake. Returns the second household
// so tests can assert its name appears in /links renderings.
func seedLinkedFamily(t *testing.T, h *Handler, database *db.Database, authStore *auth.Store, primaryHouseholdID, primaryUserID, otherName, lineNumber, lineName string) *household.Household {
	t.Helper()
	// Ensure the other user does not exist from a prior run before creating.
	_, _ = database.DB.Exec("DELETE FROM users WHERE email = 'test-other@example.com'")
	// Create a second user + household.
	otherUser, err := authStore.CreateUser("test-other@example.com", "Other Test User", nil)
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}
	otherHH, err := h.householdStore.Create(otherName, otherUser.ID)
	if err != nil {
		t.Fatalf("create other household: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE household_id = $1", otherHH.ID)
		_, _ = database.DB.Exec("DELETE FROM household_links WHERE household_a_id = $1 OR household_b_id = $1", otherHH.ID)
		_, _ = database.DB.Exec("DELETE FROM households WHERE id = $1", otherHH.ID)
		_, _ = database.DB.Exec("DELETE FROM users WHERE id = $1", otherUser.ID)
	})
	// Seed a line for the other household.
	if _, err := h.lineStore.Add(lineNumber, lineName, otherHH.ID); err != nil {
		t.Fatalf("seed line on other hh: %v", err)
	}
	// Link: primary invites, other accepts.
	invite, err := h.linkStore.CreateInvite(primaryHouseholdID, primaryUserID)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if _, err := h.linkStore.AcceptInvite(invite.InviteCode, otherUser.ID, otherHH.ID); err != nil {
		t.Fatalf("accept invite: %v", err)
	}
	return otherHH
}

func TestLinksPage_Neighborhood(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	user, err := authStore.GetUserByEmail("test@example.com")
	if err != nil {
		t.Fatalf("get test user: %v", err)
	}
	seedLinkedFamily(t, h, database, authStore, hh.ID, user.ID, "Grandma Lindh", "2180042", "Grandma")

	req := httptest.NewRequest(http.MethodGet, "/links", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	body := w.Body.String()
	for _, want := range []string{
		`class="neighborhood`,
		`class="neighborhood__row"`,
		`class="neighborhood__identity"`,
		`class="neighborhood__lines"`,
		"Grandma Lindh",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("links page missing %q", want)
		}
	}
}

func TestDashboard_HasRoomCards(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	if _, err := h.lineStore.Add("2456390", "Kitchen", hh.ID); err != nil {
		t.Fatalf("seed line: %v", err)
	}
	if _, err := h.lineStore.Add("2486881", "Living room", hh.ID); err != nil {
		t.Fatalf("seed line: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	body := w.Body.String()
	for _, want := range []string{
		`class="rooms"`,
		`class="rooms__card`,
		"Kitchen",
		"Living room",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	if strings.Contains(body, `class="strip"`) {
		t.Errorf("dashboard still renders old KPI strip")
	}
}

func TestDashboard_TodayPanelGatedByHistoryFlag(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	_ = hh

	// Default: call history disabled. Today panel must not render.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if strings.Contains(w.Body.String(), `id="today-panel"`) {
		t.Errorf("Today panel rendered while history disabled")
	}

	// Enable history.
	if err := h.householdStore.SetCallHistoryEnabled(hh.ID, true); err != nil {
		t.Fatalf("enable call history: %v", err)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(cookie)
	w2 := httptest.NewRecorder()
	h.Router().ServeHTTP(w2, req2)
	if !strings.Contains(w2.Body.String(), `id="today-panel"`) {
		t.Errorf("Today panel not rendered when history enabled")
	}
}

func TestDashboard_ConnectedFamiliesChipRow(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	user, err := authStore.GetUserByEmail("test@example.com")
	if err != nil {
		t.Fatalf("get test user: %v", err)
	}
	seedLinkedFamily(t, h, database, authStore, hh.ID, user.ID, "Grandma Lindh", "2180042", "Grandma")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	body := w.Body.String()
	for _, want := range []string{
		`class="connected-row"`,
		`class="connected-row__chip"`,
		"Grandma Lindh",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
}

func TestLinksPage_InvitePostcard(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)
	req := httptest.NewRequest(http.MethodGet, "/links?created=DEMO-123", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	body := w.Body.String()
	for _, want := range []string{
		`class="postcard"`,
		`class="postcard__code num"`,
		"DEMO-123",
		"Paste it into",
		"line number",
		"FAMILY MAIL",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("links page missing %q", want)
		}
	}
}

// TestLinksPage_MultiplePendingInvites verifies that a household can create
// multiple concurrent pending invites (no artificial cap). Regression guard
// for a cap that used to reject the second invite with "household already
// has a pending invite".
func TestLinksPage_MultiplePendingInvites(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	user, err := authStore.GetUserByEmail("test@example.com")
	if err != nil {
		t.Fatalf("get test user: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := h.linkStore.CreateInvite(hh.ID, user.ID); err != nil {
			t.Fatalf("CreateInvite #%d: %v", i+1, err)
		}
	}
	pending, err := h.linkStore.GetPendingForHousehold(hh.ID)
	if err != nil {
		t.Fatalf("GetPendingForHousehold: %v", err)
	}
	if len(pending) != 3 {
		t.Errorf("expected 3 pending invites, got %d", len(pending))
	}

	req := httptest.NewRequest(http.MethodGet, "/links", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "Pending invites sent") {
		t.Errorf("links page missing Pending invites heading")
	}
	if strings.Count(body, `data-confirm-action="/links/`) < 3 {
		t.Errorf("expected at least 3 revoke triggers for pending invites, got fewer in body")
	}
}

func TestSettingsCRTModePost(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)

	user, err := authStore.GetUserByEmail("test@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	// Reset CRTMode to the default so the assertion is meaningful even when
	// the shared test user was mutated by a prior test in the same run.
	if err := authStore.SetCRTMode(user.ID, auth.CRTModeConnecting); err != nil {
		t.Fatalf("reset CRTMode: %v", err)
	}

	form := url.Values{"crt_mode": {"all"}}
	req := httptest.NewRequest(http.MethodPost, "/settings/crt-mode", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/settings?saved=1" {
		t.Errorf("redirect = %q, want /settings?saved=1", loc)
	}

	got, err := authStore.GetUserByID(user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.CRTMode != auth.CRTModeAll {
		t.Errorf("CRTMode = %q, want %q", got.CRTMode, auth.CRTModeAll)
	}
}

func TestSettingsCRTModePost_Invalid(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)

	user, err := authStore.GetUserByEmail("test@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	// Reset CRTMode to the default so we can assert the invalid POST does
	// not change it, regardless of state left by earlier tests.
	if err := authStore.SetCRTMode(user.ID, auth.CRTModeConnecting); err != nil {
		t.Fatalf("reset CRTMode: %v", err)
	}

	form := url.Values{"crt_mode": {"bogus"}}
	req := httptest.NewRequest(http.MethodPost, "/settings/crt-mode", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", w.Code)
	}

	got, err := authStore.GetUserByID(user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	// Default is 'connecting' for new users; invalid POST should leave it unchanged.
	if got.CRTMode != auth.CRTModeConnecting {
		t.Errorf("CRTMode = %q, want %q (unchanged after invalid POST)", got.CRTMode, auth.CRTModeConnecting)
	}
}

func TestPhonesListShowsSilentBadgeWhenSilent(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	if w := postSilentMode(t, h, cookie, "on", false); w.Code != http.StatusSeeOther {
		t.Fatalf("setup: got %d", w.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/phones", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /phones: got %d, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `class="phone-silent"`) {
		t.Errorf("expected phone-silent badge in /phones HTML, body:\n%s", w.Body.String())
	}
}

func TestPhonesListOmitsSilentBadgeWhenNotSilent(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	req := httptest.NewRequest(http.MethodGet, "/phones", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if strings.Contains(w.Body.String(), `phone-silent`) {
		t.Errorf("unexpected silent badge in /phones HTML when silent mode off")
	}
}
