//go:build integration

package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/household"
	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/pairing"
	"github.com/justinlindh/digits/server/internal/signaling"
	"github.com/justinlindh/digits/server/internal/updates"
)

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
	_, err := lineStore.Add(context.Background(), "3140001", "Test Phone", hh.ID)
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
	if _, err := lineStore.GetByNumber(context.Background(), "3140001"); err == nil {
		t.Error("line should have been deleted")
	}
}

func TestCallsPageReturns200(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	if err := h.householdStore.SetCallHistoryEnabled(context.Background(), hh.ID, true); err != nil {
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

func TestHandleCalls_BadCursor_Returns400(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	if err := h.householdStore.SetCallHistoryEnabled(context.Background(), hh.ID, true); err != nil {
		t.Fatalf("enable call history: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/calls?before=!!!not-base64!!!", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad cursor: got %d, want 400", w.Code)
	}
}

func TestHandleCalls_ValidCursor_Returns200(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	if err := h.householdStore.SetCallHistoryEnabled(context.Background(), hh.ID, true); err != nil {
		t.Fatalf("enable call history: %v", err)
	}

	// Seed a small number of calls so we have at least one entry to build a cursor from.
	seedEndedCallsForCursorTest(t, h, database, hh, 3)

	// Build a cursor directly from the most recent entry, bypassing template rendering.
	lines, err := h.lineStore.ListByHousehold(context.Background(), hh.ID)
	if err != nil {
		t.Fatalf("list lines: %v", err)
	}
	numbers := make([]string, 0, len(lines))
	for _, l := range lines {
		numbers = append(numbers, l.Number)
	}
	entries, err := h.tracker.RecentHistoryForPhones(context.Background(), numbers, nil, 10)
	if err != nil {
		t.Fatalf("recent history: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one seeded entry")
	}
	cursor := calls.CursorForEntry(entries[0]).Encode()

	req := httptest.NewRequest(http.MethodGet, "/calls?before="+cursor, nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("valid cursor: got %d, want 200", w.Code)
	}
}

// seedEndedCallsForCursorTest inserts n plain ended calls between two phone
// numbers on hh's line list, with monotonically increasing timestamps. If hh
// has fewer than 2 lines, two lines are seeded first. Used by handler tests
// that need real call history.
func seedEndedCallsForCursorTest(t *testing.T, h *Handler, database *db.Database, hh *household.Household, n int) {
	t.Helper()
	lines, err := h.lineStore.ListByHousehold(context.Background(), hh.ID)
	if err != nil {
		t.Fatalf("list lines: %v", err)
	}
	if len(lines) < 2 {
		seedNumbers := []string{"3149001", "3149002"}
		for _, num := range seedNumbers {
			ln, err := h.lineStore.Add(context.Background(), num, "Cursor Test "+num, hh.ID)
			if err != nil {
				t.Fatalf("seed line %s: %v", num, err)
			}
			id := ln.ID
			t.Cleanup(func() {
				_, _ = database.DB.Exec("DELETE FROM lines WHERE id = $1", id)
			})
		}
		lines, err = h.lineStore.ListByHousehold(context.Background(), hh.ID)
		if err != nil {
			t.Fatalf("list lines after seed: %v", err)
		}
	}
	if len(lines) < 2 {
		t.Fatalf("seed needs at least 2 lines on the household, got %d", len(lines))
	}
	a, b := lines[0].Number, lines[1].Number
	for i := 0; i < n; i++ {
		if _, err := h.tracker.OnCallInitiated(context.Background(), a, b); err != nil {
			t.Fatalf("OnCallInitiated[%d]: %v", i, err)
		}
		if err := h.tracker.OnCallEnded(context.Background(), a, b); err != nil {
			t.Fatalf("OnCallEnded[%d]: %v", i, err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM calls WHERE caller = $1 OR callee = $1 OR caller = $2 OR callee = $2", a, b)
	})
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

	user, _ := authStore.GetUserByEmail(context.Background(), "test@example.com")
	householdStore := h.householdStore
	hh, err := householdStore.Create(context.Background(), "TZ Test Family", user.ID)
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

	got, err := householdStore.GetByID(context.Background(), hh.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Timezone != "America/Chicago" {
		t.Errorf("timezone = %q, want America/Chicago", got.Timezone)
	}
}

func TestSettingsPageRendersSavedTimezone(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)

	if err := h.householdStore.SetTimezone(context.Background(), hh.ID, "America/Chicago"); err != nil {
		t.Fatalf("SetTimezone: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /settings = %d, want 200", w.Code)
	}
	body := w.Body.String()

	// Saved option must be server-side marked as selected so it survives
	// page load regardless of client-side JS.
	wantSelected := `value="America/Chicago" selected`
	if !strings.Contains(body, wantSelected) {
		t.Errorf("settings page missing %q", wantSelected)
	}

	// Any other option that happens to match the browser-detected zone
	// must not be server-rendered as selected. Check a zone the test
	// household definitely isn't set to.
	dontWant := `value="America/Los_Angeles" selected`
	if strings.Contains(body, dontWant) {
		t.Errorf("settings page unexpectedly contains %q", dontWant)
	}
}

func TestSettingsTimezonePost_Invalid(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)

	user, _ := authStore.GetUserByEmail(context.Background(), "test@example.com")
	householdStore := h.householdStore
	hh, err := householdStore.Create(context.Background(), "TZ Invalid Family", user.ID)
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

	got, err := householdStore.GetByID(context.Background(), hh.ID)
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
	user, err := authStore.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		user, err = authStore.CreateUser(context.Background(), "test@example.com", "Test User", nil)
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
	}
	markUserOnboarded(t, authStore, user.ID)
	hh, err := householdStore.Create(context.Background(), "Test Household", user.ID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}

	// Generate pairing code (creates the device row)
	code, err := pairingStore.GenerateCode(context.Background(), hardwareID)
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}

	// Claim the device (pairs it, sets hashed token)
	token, _, err = pairingStore.ClaimDevice(context.Background(), code, number, "Test Phone", hh.ID)
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
	h.hub.Register("3140001", conn)

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
	_, err := h.lineStore.Add(context.Background(), "2456390", "Test Line", hh.ID)
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
	h.hub.Register("3140001", conn)

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
	h.hub.Register("3140001", conn)

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
	user, err := authStore.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		t.Fatalf("get test user: %v", err)
	}
	hh, err := h.householdStore.Create(context.Background(), "Voice Style Test", user.ID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	lineStore := line.NewStore(database)
	if _, err := lineStore.Add(context.Background(), "3140001", "Test Phone", hh.ID); err != nil {
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
	modernTag := body[modernIdx : strings.Index(body[modernIdx:], ">")+modernIdx]
	copperTag := body[copperIdx : strings.Index(body[copperIdx:], ">")+copperIdx]
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
	tag := body[checkboxIdx : strings.Index(body[checkboxIdx:], ">")+checkboxIdx]
	if !strings.Contains(tag, "checked") {
		t.Errorf("expected checkbox checked after turning on: %q", tag)
	}
}

func TestPhoneSilentModeMissingLineReturns404(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	user, err := authStore.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		t.Fatalf("get test user: %v", err)
	}
	hh, err := h.householdStore.Create(context.Background(), "Silent Mode Missing", user.ID)
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
	h.hub.Register("3140001", conn)

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
	h.hub.Register("3140001", conn)

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
	user, err := authStore.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		t.Fatalf("get test user: %v", err)
	}
	hh, err := h.householdStore.Create(context.Background(), "Voice Style Missing", user.ID)
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
	h.hub.Register("3140001", conn)

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
	h.hub.Register("3140001", conn)

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
	if err := h.householdStore.SetCallHistoryEnabled(context.Background(), hh.ID, true); err != nil {
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
	otherUser, err := authStore.CreateUser(context.Background(), "test-other@example.com", "Other Test User", nil)
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}
	markUserOnboarded(t, authStore, otherUser.ID)
	otherHH, err := h.householdStore.Create(context.Background(), otherName, otherUser.ID)
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
	if _, err := h.lineStore.Add(context.Background(), lineNumber, lineName, otherHH.ID); err != nil {
		t.Fatalf("seed line on other hh: %v", err)
	}
	// Link: primary invites, other accepts.
	invite, err := h.linkStore.CreateInvite(context.Background(), primaryHouseholdID, primaryUserID)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if _, err := h.linkStore.AcceptInvite(context.Background(), invite.InviteCode, otherUser.ID, otherHH.ID); err != nil {
		t.Fatalf("accept invite: %v", err)
	}
	return otherHH
}

func TestLinksPage_Neighborhood(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	user, err := authStore.GetUserByEmail(context.Background(), "test@example.com")
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
	if _, err := h.lineStore.Add(context.Background(), "2456390", "Kitchen", hh.ID); err != nil {
		t.Fatalf("seed line: %v", err)
	}
	if _, err := h.lineStore.Add(context.Background(), "2486881", "Living room", hh.ID); err != nil {
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
	if err := h.householdStore.SetCallHistoryEnabled(context.Background(), hh.ID, true); err != nil {
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
	user, err := authStore.GetUserByEmail(context.Background(), "test@example.com")
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
	user, err := authStore.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		t.Fatalf("get test user: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := h.linkStore.CreateInvite(context.Background(), hh.ID, user.ID); err != nil {
			t.Fatalf("CreateInvite #%d: %v", i+1, err)
		}
	}
	pending, err := h.linkStore.GetPendingForHousehold(context.Background(), hh.ID)
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

	user, err := authStore.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	// Reset CRTMode to the default so the assertion is meaningful even when
	// the shared test user was mutated by a prior test in the same run.
	if err := authStore.SetCRTMode(context.Background(), user.ID, auth.CRTModeConnecting); err != nil {
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

	got, err := authStore.GetUserByID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.CRTMode != auth.CRTModeAll {
		t.Errorf("CRTMode = %q, want %q", got.CRTMode, auth.CRTModeAll)
	}
}

func TestSettingsAppearancePost(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)

	user, err := authStore.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	// Reset Appearance to the default so the assertion is meaningful even
	// when the shared test user was mutated by a prior test in the same run.
	if err := authStore.SetAppearance(context.Background(), user.ID, auth.AppearanceDay); err != nil {
		t.Fatalf("reset Appearance: %v", err)
	}

	form := url.Values{"appearance": {"night"}}
	req := httptest.NewRequest(http.MethodPost, "/settings/appearance", strings.NewReader(form.Encode()))
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

	got, err := authStore.GetUserByID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.Appearance != auth.AppearanceNight {
		t.Errorf("Appearance = %q, want %q", got.Appearance, auth.AppearanceNight)
	}
}

func TestSettingsAppearancePost_Invalid(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)

	user, err := authStore.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	// Reset Appearance to the default so we can assert the invalid POST does
	// not change it, regardless of state left by earlier tests.
	if err := authStore.SetAppearance(context.Background(), user.ID, auth.AppearanceDay); err != nil {
		t.Fatalf("reset Appearance: %v", err)
	}

	form := url.Values{"appearance": {"bogus"}}
	req := httptest.NewRequest(http.MethodPost, "/settings/appearance", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", w.Code)
	}

	got, err := authStore.GetUserByID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	// Default is 'day' for new users; invalid POST should leave it unchanged.
	if got.Appearance != auth.AppearanceDay {
		t.Errorf("Appearance = %q, want %q (unchanged after invalid POST)", got.Appearance, auth.AppearanceDay)
	}
}

func TestSettingsCRTModePost_Invalid(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)

	user, err := authStore.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	// Reset CRTMode to the default so we can assert the invalid POST does
	// not change it, regardless of state left by earlier tests.
	if err := authStore.SetCRTMode(context.Background(), user.ID, auth.CRTModeConnecting); err != nil {
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

	got, err := authStore.GetUserByID(context.Background(), user.ID)
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

// seedPairedHandsetForTest inserts a line row and registers a signaling.Conn
// with the hub so the /phones handler sees the device as online with the given
// firmware version.
func seedPairedHandsetForTest(t *testing.T, h *Handler, database *db.Database, householdID, number, fwVersion string) {
	t.Helper()
	_, err := database.DB.Exec(
		`INSERT INTO lines (number, name, household_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		number, "Test Handset", householdID,
	)
	if err != nil {
		t.Fatalf("seed line %s: %v", number, err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number = $1", number)
	})

	conn := &signaling.Conn{Send: make(chan []byte, 10)}
	h.hub.Register(number, conn)
	h.hub.UpdateDeviceInfo(number, "", "", fwVersion, "")
}

// fakeReleasesForTest builds a fake GitHubReleases populated with the given
// version->notes map for the "firmware" component. The sentinel is stripped
// from notes before insertion to mirror what the real fetcher does.
func fakeReleasesForTest(t *testing.T, versionNotes map[string]string) *updates.GitHubReleases {
	t.Helper()
	releases := make(map[string]*updates.Release, len(versionNotes))
	var latest string
	for v, notes := range versionNotes {
		releases[v] = &updates.Release{Version: v, Notes: updates.StripGroomedSentinel(notes)}
		if latest == "" || updates.CompareSemver(v, latest) > 0 {
			latest = v
		}
	}
	idx := &updates.ReleaseIndex{
		Firmware: updates.ComponentIndex{Latest: latest, Releases: releases},
		Pi:       updates.ComponentIndex{Latest: "", Releases: make(map[string]*updates.Release)},
	}
	return updates.NewGitHubReleasesWithIndex(idx)
}

// seedLineWithoutDeviceInfoForTest inserts a line row but does NOT register a
// hub connection or call UpdateDeviceInfo, so the handler sees the device as
// offline with no version information.
func seedLineWithoutDeviceInfoForTest(t *testing.T, database *db.Database, householdID, number string) {
	t.Helper()
	_, err := database.DB.Exec(
		`INSERT INTO lines (number, name, household_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		number, "Offline Handset", householdID,
	)
	if err != nil {
		t.Fatalf("seed line %s: %v", number, err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number = $1", number)
	})
}

func TestLineRowNotesSkippedForOfflineDevice(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)

	// Seed a line without any hub registration (never-connected device).
	seedLineWithoutDeviceInfoForTest(t, database, hh.ID, "+15551230001")

	h.Releases = fakeReleasesForTest(t, map[string]string{
		"1.4.0": "<!-- groomed:v1 -->\nshould not appear for offline device",
	})

	req := httptest.NewRequest(http.MethodGet, "/phones", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "should not appear for offline device") {
		t.Errorf("offline device should not show release notes, but body contained them")
	}
}

func TestLineRowPopulatesFirmwareUpdateNotes(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)

	// Seed a paired handset at fw 1.2.0.
	seedPairedHandsetForTest(t, h, database, hh.ID, "+15551230000", "1.2.0")

	// Build a fake release index with fw 1.2.0, 1.3.0, 1.4.0.
	h.Releases = fakeReleasesForTest(t, map[string]string{
		"1.2.0": "older release",
		"1.3.0": "<!-- groomed:v1 -->\nmid release",
		"1.4.0": "<!-- groomed:v1 -->\nlatest release",
	})

	req := httptest.NewRequest(http.MethodGet, "/phones", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "latest release") {
		t.Errorf("body missing latest note text:\n%s", body)
	}
	if !strings.Contains(body, "mid release") {
		t.Errorf("body missing mid note text:\n%s", body)
	}
	if strings.Contains(body, "older release") {
		t.Errorf("body should not show the 1.2.0 note text (device is on 1.2.0):\n%s", body)
	}
	if strings.Contains(body, "<!-- groomed:v1 -->") {
		t.Errorf("sentinel should be stripped before render")
	}
}

func TestPairRedirectIncludesQueryParams(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)

	// Generate a real pairing code so ClaimDevice accepts it.
	hwID := fmt.Sprintf("test-redirect-hw-%d", time.Now().UnixNano())
	code, err := h.pairingStore.GenerateCode(context.Background(), hwID)
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	number := fmt.Sprintf("55%05d", time.Now().UnixNano()%100000)
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM devices WHERE hardware_id = $1", hwID)
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number = $1", number)
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE household_id = $1", hh.ID)
	})

	form := url.Values{}
	form.Set("number", number)
	form.Set("name", "Front Porch")
	form.Set("code", code)

	req := httptest.NewRequest(http.MethodPost, "/phones/pair", strings.NewReader(form.Encode()))
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	parsed, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("bad Location header %q: %v", loc, err)
	}
	if parsed.Path != "/phones" {
		t.Errorf("redirect path = %q, want /phones", parsed.Path)
	}
	if got := parsed.Query().Get("paired"); got != "Front Porch" {
		t.Errorf("paired param = %q, want %q", got, "Front Porch")
	}
}

func TestPairBannerRendersOnQueryParam(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)

	req := httptest.NewRequest(http.MethodGet, "/phones?paired=Kitchen&fw=1.4.0", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `class="pair-banner"`) {
		t.Errorf("body missing pair-banner element")
	}
	if !strings.Contains(body, "Kitchen") {
		t.Errorf("body missing paired name")
	}
	if !strings.Contains(body, "fw 1.4.0") {
		t.Errorf("body missing fw version")
	}
}

func readLineName(t *testing.T, database *db.Database) string {
	t.Helper()
	var name string
	if err := database.DB.QueryRow(`SELECT name FROM lines WHERE number = '3140001'`).Scan(&name); err != nil {
		t.Fatalf("read line name: %v", err)
	}
	return name
}

func postLineName(t *testing.T, h *Handler, cookie *http.Cookie, value string, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"name": {value}}
	req := httptest.NewRequest(http.MethodPost, "/phones/3140001/name", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	return w
}

func TestPhoneNamePostUpdatesAndRedirects(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	w := postLineName(t, h, cookie, "Garage", false)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/phones/3140001" {
		t.Fatalf("expected redirect to /phones/3140001, got %q", loc)
	}
	if got := readLineName(t, database); got != "Garage" {
		t.Fatalf("expected name=Garage in db, got %q", got)
	}
}

func TestPhoneNamePostHTMXReturnsDisplayPartial(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	w := postLineName(t, h, cookie, "  Garage  ", true)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="name-section"`) {
		t.Fatalf("htmx response missing name-section wrapper:\n%s", body)
	}
	if !strings.Contains(body, ">Garage<") {
		t.Fatalf("htmx response missing trimmed name display:\n%s", body)
	}
	if strings.Contains(body, `name="name"`) {
		t.Fatalf("htmx response should be display partial, but contains form input:\n%s", body)
	}
	if got := readLineName(t, database); got != "Garage" {
		t.Fatalf("expected trimmed name=Garage in db, got %q", got)
	}
}

func TestPhoneNamePostEmptyReturnsEditWithError(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	w := postLineName(t, h, cookie, "   ", true)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty name, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `name="name"`) {
		t.Fatalf("expected edit form re-rendered on error:\n%s", body)
	}
	if !strings.Contains(body, "name is required") {
		t.Fatalf("expected validation message in body:\n%s", body)
	}
	if got := readLineName(t, database); got != "Test Phone" {
		t.Fatalf("name should be unchanged after rejected POST, got %q", got)
	}
}

func TestPhoneNamePostTooLongReturnsEditWithError(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	overlong := strings.Repeat("a", 51)
	w := postLineName(t, h, cookie, overlong, true)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for too-long name, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "name too long") {
		t.Fatalf("expected length error in body:\n%s", w.Body.String())
	}
	if got := readLineName(t, database); got != "Test Phone" {
		t.Fatalf("name should be unchanged, got %q", got)
	}
}

func TestPhoneNameEditGetReturnsForm(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	req := httptest.NewRequest(http.MethodGet, "/phones/3140001/name/edit", nil)
	req.AddCookie(cookie)
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `name="name"`) {
		t.Fatalf("expected name input in edit form:\n%s", body)
	}
	if !strings.Contains(body, `value="Test Phone"`) {
		t.Fatalf("expected current name prefilled:\n%s", body)
	}
	if !strings.Contains(body, `maxlength="50"`) {
		t.Fatalf("expected maxlength attribute:\n%s", body)
	}
}

func TestPhoneEditPostEmptyNameReturns400(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	form := url.Values{"name": {"   "}}
	req := httptest.NewRequest(http.MethodPost, "/phones/3140001/edit", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty name on list-view edit, got %d: %s", w.Code, w.Body.String())
	}
	if got := readLineName(t, database); got != "Test Phone" {
		t.Fatalf("name should be unchanged after rejected POST, got %q", got)
	}
}

// TestHandleCalls_Intercom_PaginationControls covers the intercom theme's
// behavior around pagination controls and auto-refresh attributes:
//   - Page 1 with no older entries: no pagination nav, hx-trigger present.
//   - Page 1 with an older page available: nav rendered, hx-trigger present.
//   - Page 2 (paged via ?before=cursor): nav rendered, hx-trigger absent.
func TestHandleCalls_Intercom_PaginationControls(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	if err := h.householdStore.SetCallHistoryEnabled(context.Background(), hh.ID, true); err != nil {
		t.Fatalf("enable call history: %v", err)
	}

	// Sub-case 1: empty dataset, page 1. No pagination, polling on.
	{
		req := httptest.NewRequest(http.MethodGet, "/calls", nil)
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.Router().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("page 1 (empty): got %d, want 200", w.Code)
		}
		body := w.Body.String()
		if strings.Contains(body, `class="panel__pagination"`) {
			t.Errorf("page 1 (empty): pagination nav should be absent")
		}
		if !strings.Contains(body, `hx-trigger="every 10s"`) {
			t.Errorf("page 1 (empty): expected hx-trigger='every 10s'")
		}
	}

	// Seed enough ended calls to force OlderCursor on page 1.
	// The page size is 50, so seed 51 to guarantee a next page.
	seedEndedCallsForCursorTest(t, h, database, hh, 51)

	// Sub-case 2: page 1 with older page available. Nav rendered, polling on.
	{
		req := httptest.NewRequest(http.MethodGet, "/calls", nil)
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.Router().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("page 1 (with older): got %d, want 200", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, `class="panel__pagination"`) {
			t.Errorf("page 1 (with older): expected pagination nav")
		}
		if !strings.Contains(body, `href="/calls?before=`) {
			t.Errorf("page 1 (with older): expected Older link with cursor")
		}
		if !strings.Contains(body, `hx-trigger="every 10s"`) {
			t.Errorf("page 1 (with older): expected hx-trigger='every 10s'")
		}
	}

	// Build a cursor from the most recent entry to exercise the IsPaged path.
	lines, err := h.lineStore.ListByHousehold(context.Background(), hh.ID)
	if err != nil {
		t.Fatalf("list lines: %v", err)
	}
	numbers := make([]string, 0, len(lines))
	for _, l := range lines {
		numbers = append(numbers, l.Number)
	}
	entries, err := h.tracker.RecentHistoryForPhones(context.Background(), numbers, nil, 10)
	if err != nil {
		t.Fatalf("recent history: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one seeded entry")
	}
	cursor := calls.CursorForEntry(entries[0]).Encode()

	// Sub-case 3: page 2 via ?before=cursor. Nav rendered, polling off.
	{
		req := httptest.NewRequest(http.MethodGet, "/calls?before="+cursor, nil)
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.Router().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("page 2: got %d, want 200", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, `class="panel__pagination"`) {
			t.Errorf("page 2: expected pagination nav")
		}
		if !strings.Contains(body, `href="/calls"`) {
			t.Errorf("page 2: expected Newer link to /calls")
		}
		if strings.Contains(body, `hx-trigger="every 10s"`) {
			t.Errorf("page 2: hx-trigger must be absent on paged view")
		}
	}
}

// TestHandleCalls_Dialup_PaginationControls covers the dialup theme's
// behavior around pagination controls and auto-refresh attributes:
//   - Page 1 with no older entries: no pagination nav, hx-trigger present.
//   - Page 1 with an older page available: nav rendered, hx-trigger present.
//   - Page 2 (paged via ?before=cursor): nav rendered, hx-trigger absent.
func TestHandleCalls_Dialup_PaginationControls(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	if err := h.householdStore.SetCallHistoryEnabled(context.Background(), hh.ID, true); err != nil {
		t.Fatalf("enable call history: %v", err)
	}

	// Switch the test user to the dialup theme so the dialup template
	// renders. Reset on cleanup so subsequent tests sharing the user
	// (via setupAuthedHousehold) see the default value.
	user, err := authStore.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		t.Fatalf("get test user: %v", err)
	}
	if err := authStore.SetTheme(context.Background(), user.ID, auth.ThemeDialup); err != nil {
		t.Fatalf("set dialup theme: %v", err)
	}
	t.Cleanup(func() { _ = authStore.SetTheme(context.Background(), user.ID, auth.ThemeIntercom) })

	// Sub-case 1: empty dataset, page 1. No pagination, polling on.
	{
		req := httptest.NewRequest(http.MethodGet, "/calls", nil)
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.Router().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("page 1 (empty): got %d, want 200", w.Code)
		}
		body := w.Body.String()
		if strings.Contains(body, `dialup-pagination`) {
			t.Errorf("page 1 (empty): pagination nav should be absent")
		}
		if !strings.Contains(body, `hx-trigger="every 10s"`) {
			t.Errorf("page 1 (empty): expected hx-trigger='every 10s'")
		}
	}

	// Seed enough ended calls to force OlderCursor on page 1.
	// The page size is 50, so seed 51 to guarantee a next page.
	seedEndedCallsForCursorTest(t, h, database, hh, 51)

	// Sub-case 2: page 1 with older page available. Nav rendered, polling on.
	{
		req := httptest.NewRequest(http.MethodGet, "/calls", nil)
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.Router().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("page 1 (with older): got %d, want 200", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, `dialup-pagination`) {
			t.Errorf("page 1 (with older): expected dialup pagination nav")
		}
		if !strings.Contains(body, `href="/calls?before=`) {
			t.Errorf("page 1 (with older): expected Older link with cursor")
		}
		if !strings.Contains(body, `hx-trigger="every 10s"`) {
			t.Errorf("page 1 (with older): expected hx-trigger='every 10s'")
		}
	}

	// Build a cursor from the most recent entry to exercise the IsPaged path.
	lines, err := h.lineStore.ListByHousehold(context.Background(), hh.ID)
	if err != nil {
		t.Fatalf("list lines: %v", err)
	}
	numbers := make([]string, 0, len(lines))
	for _, l := range lines {
		numbers = append(numbers, l.Number)
	}
	entries, err := h.tracker.RecentHistoryForPhones(context.Background(), numbers, nil, 10)
	if err != nil {
		t.Fatalf("recent history: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one seeded entry")
	}
	cursor := calls.CursorForEntry(entries[0]).Encode()

	// Sub-case 3: page 2 via ?before=cursor. Nav rendered, polling off.
	{
		req := httptest.NewRequest(http.MethodGet, "/calls?before="+cursor, nil)
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.Router().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("page 2: got %d, want 200", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, `dialup-pagination`) {
			t.Errorf("page 2: expected dialup pagination nav")
		}
		if !strings.Contains(body, `href="/calls"`) {
			t.Errorf("page 2: expected Newer link to /calls")
		}
		if strings.Contains(body, `hx-trigger="every 10s"`) {
			t.Errorf("page 2: hx-trigger must be absent on paged view")
		}
	}
}

// TestHandleCalls_AM_PaginationControls covers the answering-machine theme's
// behavior around pagination controls and auto-refresh attributes:
//   - Page 1 with no older entries: no transport bar, hx-trigger present.
//   - Page 1 with an older page available: transport bar rendered, Older link
//     with cursor, hx-trigger present.
//   - Page 2 (paged via ?before=cursor): transport bar rendered, Newer link
//     to /calls, hx-trigger absent (snapshot mode, REW LED in header).
func TestHandleCalls_AM_PaginationControls(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	if err := h.householdStore.SetCallHistoryEnabled(context.Background(), hh.ID, true); err != nil {
		t.Fatalf("enable call history: %v", err)
	}

	// Switch the test user to the AM theme so the calls-am template renders.
	// Reset on cleanup so subsequent tests sharing the user (via
	// setupAuthedHousehold) see the default value.
	user, err := authStore.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		t.Fatalf("get test user: %v", err)
	}
	if err := authStore.SetTheme(context.Background(), user.ID, auth.ThemeAnsweringMachine); err != nil {
		t.Fatalf("set AM theme: %v", err)
	}
	t.Cleanup(func() { _ = authStore.SetTheme(context.Background(), user.ID, auth.ThemeIntercom) })

	// Sub-case 1: empty dataset, page 1. No transport bar, polling on.
	{
		req := httptest.NewRequest(http.MethodGet, "/calls", nil)
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.Router().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("page 1 (empty): got %d, want 200", w.Code)
		}
		body := w.Body.String()
		if strings.Contains(body, `am-calls__transport`) {
			t.Errorf("page 1 (empty): transport bar should be absent")
		}
		if !strings.Contains(body, `hx-trigger="every 10s"`) {
			t.Errorf("page 1 (empty): expected hx-trigger='every 10s'")
		}
	}

	// Seed enough ended calls to force OlderCursor on page 1.
	// The page size is 50, so seed 51 to guarantee a next page.
	seedEndedCallsForCursorTest(t, h, database, hh, 51)

	// Sub-case 2: page 1 with older page available. Bar rendered, polling on.
	{
		req := httptest.NewRequest(http.MethodGet, "/calls", nil)
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.Router().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("page 1 (with older): got %d, want 200", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, `am-calls__transport`) {
			t.Errorf("page 1 (with older): expected AM transport bar")
		}
		if !strings.Contains(body, `am-transport--rew`) {
			t.Errorf("page 1 (with older): expected enabled REW (older) button")
		}
		if !strings.Contains(body, `href="/calls?before=`) {
			t.Errorf("page 1 (with older): expected Older link with cursor")
		}
		if !strings.Contains(body, `hx-trigger="every 10s"`) {
			t.Errorf("page 1 (with older): expected hx-trigger='every 10s'")
		}
	}

	// Build a cursor from the most recent entry to exercise the IsPaged path.
	lines, err := h.lineStore.ListByHousehold(context.Background(), hh.ID)
	if err != nil {
		t.Fatalf("list lines: %v", err)
	}
	numbers := make([]string, 0, len(lines))
	for _, l := range lines {
		numbers = append(numbers, l.Number)
	}
	entries, err := h.tracker.RecentHistoryForPhones(context.Background(), numbers, nil, 10)
	if err != nil {
		t.Fatalf("recent history: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one seeded entry")
	}
	cursor := calls.CursorForEntry(entries[0]).Encode()

	// Sub-case 3: page 2 via ?before=cursor. Bar rendered, polling off.
	{
		req := httptest.NewRequest(http.MethodGet, "/calls?before="+cursor, nil)
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.Router().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("page 2: got %d, want 200", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, `am-calls__transport`) {
			t.Errorf("page 2: expected AM transport bar")
		}
		if !strings.Contains(body, `am-transport--ff`) {
			t.Errorf("page 2: expected enabled FF (newer) button")
		}
		if !strings.Contains(body, `href="/calls"`) {
			t.Errorf("page 2: expected Newer link to /calls")
		}
		if strings.Contains(body, `hx-trigger="every 10s"`) {
			t.Errorf("page 2: hx-trigger must be absent on paged view")
		}
	}
}
