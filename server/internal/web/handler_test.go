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
	relay := signaling.NewRelay(hub, tracker, nil)

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

func TestDashboardReturns200(t *testing.T) {
	h, _, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Network Overview") {
		t.Errorf("dashboard missing expected content")
	}
}

func TestPhonesPageReturns200(t *testing.T) {
	h, _, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	req := httptest.NewRequest(http.MethodGet, "/phones", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestAddPhoneViaHTMX(t *testing.T) {
	h, _, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
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
	h, _, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
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
	cookie := addSessionCookie(t, authStore)
	lineStore := line.NewStore(database)
	// We need a household to add a line; for this test we just insert directly
	_, err := lineStore.Add("3140001", "Test Phone", "00000000-0000-0000-0000-000000000000")
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
	h, _, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	req := httptest.NewRequest(http.MethodGet, "/calls", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestSettingsPageReturns200(t *testing.T) {
	h, _, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "SIGNALD_ADDR") {
		t.Errorf("settings page missing env var reference")
	}
}

func TestAPIStatusReturnsJSON(t *testing.T) {
	h, _, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
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
	h, _, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
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
