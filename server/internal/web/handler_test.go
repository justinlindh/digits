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
	googleAuth := auth.NewGoogleAuth("", "", "", "", authStore)
	emailSender := email.NewNoopSender()
	loginTmpl, err := template.New("").ParseFS(TemplateFS(), "templates/layout-v2.html", "templates/login.html")
	if err != nil {
		t.Fatalf("parse login template: %v", err)
	}
	authHandlers := auth.NewHandlers(authStore, googleAuth, emailSender, "http://localhost", "", loginTmpl, false)

	h, err := NewHandler(lineStore, deviceStore, hub, tracker, relay, HandlerConfig{
		Addr:        ":8443",
	}, authStore, authHandlers, googleAuth, householdStore, pairingStore, nil, emailSender, "", "")
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
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Household dashboard") {
		t.Errorf("dashboard missing expected content")
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
