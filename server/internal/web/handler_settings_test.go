//go:build integration

package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/signaling"
)

// postDoNotDisturb POSTs the enabled value to /settings/do-not-disturb and
// returns the recorded response.
func postDoNotDisturb(t *testing.T, h *Handler, cookie *http.Cookie, enabled string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"enabled": {enabled}}
	req := httptest.NewRequest(http.MethodPost, "/settings/do-not-disturb", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	return w
}

// expectLineSettingsPush drains conn.Send within 1s and asserts that the
// frame is a LineSettings push with the expected SilentMode value.
func expectLineSettingsPush(t *testing.T, conn *signaling.Conn, wantSilent bool) {
	t.Helper()
	select {
	case data := <-conn.Send:
		msg, err := signaling.ParseMessage(data)
		if err != nil {
			t.Fatalf("parse pushed message: %v", err)
		}
		if msg.Type != signaling.TypeLineSettings {
			t.Fatalf("expected %s push, got %s", signaling.TypeLineSettings, msg.Type)
		}
		if msg.LineSettings == nil {
			t.Fatalf("expected line_settings payload, got nil")
		}
		if msg.LineSettings.SilentMode != wantSilent {
			t.Fatalf("expected SilentMode=%v, got %v", wantSilent, msg.LineSettings.SilentMode)
		}
	case <-time.After(time.Second):
		t.Fatal("device did not receive line_settings push")
	}
}

func TestHandleSettingsDoNotDisturbPost_PersistsAndFansOut(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)

	user, err := authStore.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		t.Fatalf("get test user: %v", err)
	}
	hh, err := h.householdStore.Create(context.Background(), "DND Fan-out", user.ID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	lineStore := line.NewStore(database)
	if _, err := lineStore.Add(context.Background(), "3140101", "Phone A", hh.ID); err != nil {
		t.Fatalf("add line A: %v", err)
	}
	if _, err := lineStore.Add(context.Background(), "3140102", "Phone B", hh.ID); err != nil {
		t.Fatalf("add line B: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM lines WHERE household_id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE household_id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM households WHERE id = $1", hh.ID)
	})

	connA := &signaling.Conn{Send: make(chan []byte, 10)}
	connB := &signaling.Conn{Send: make(chan []byte, 10)}
	h.Hub().Register("3140101", connA)
	h.Hub().Register("3140102", connB)

	w := postDoNotDisturb(t, h, cookie, "true")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/settings?saved=1" {
		t.Fatalf("expected redirect to /settings?saved=1, got %q", loc)
	}

	got, err := h.householdStore.GetByID(context.Background(), hh.ID)
	if err != nil {
		t.Fatalf("get household: %v", err)
	}
	if !got.DoNotDisturb {
		t.Fatalf("expected DoNotDisturb=true after POST, got false")
	}

	expectLineSettingsPush(t, connA, true)
	expectLineSettingsPush(t, connB, true)
}

func TestHandleSettingsDoNotDisturbPost_OffSendsFalse(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)

	user, err := authStore.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		t.Fatalf("get test user: %v", err)
	}
	hh, err := h.householdStore.Create(context.Background(), "DND Off", user.ID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	if err := h.householdStore.SetDoNotDisturb(context.Background(), hh.ID, true); err != nil {
		t.Fatalf("seed DND=true: %v", err)
	}
	lineStore := line.NewStore(database)
	if _, err := lineStore.Add(context.Background(), "3140201", "Phone X", hh.ID); err != nil {
		t.Fatalf("add line: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM lines WHERE household_id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE household_id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM households WHERE id = $1", hh.ID)
	})

	conn := &signaling.Conn{Send: make(chan []byte, 10)}
	h.Hub().Register("3140201", conn)

	w := postDoNotDisturb(t, h, cookie, "false")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}

	got, err := h.householdStore.GetByID(context.Background(), hh.ID)
	if err != nil {
		t.Fatalf("get household: %v", err)
	}
	if got.DoNotDisturb {
		t.Fatalf("expected DoNotDisturb=false after POST, got true")
	}

	// Line silent_mode is the default (false) and household DND is now false,
	// so the push should carry SilentMode=false.
	expectLineSettingsPush(t, conn, false)
}

func TestHandleSettingsDoNotDisturbPost_OffPreservesPerLineSilent(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)

	user, err := authStore.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		t.Fatalf("get test user: %v", err)
	}
	hh, err := h.householdStore.Create(context.Background(), "DND Off Line Silent", user.ID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	if err := h.householdStore.SetDoNotDisturb(context.Background(), hh.ID, true); err != nil {
		t.Fatalf("seed DND=true: %v", err)
	}
	lineStore := line.NewStore(database)
	ln, err := lineStore.Add(context.Background(), "3140301", "Phone Y", hh.ID)
	if err != nil {
		t.Fatalf("add line: %v", err)
	}
	if err := lineStore.UpdateSettings(context.Background(), ln.ID, line.Settings{SilentMode: true}); err != nil {
		t.Fatalf("set line silent_mode=true: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM lines WHERE household_id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE household_id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM households WHERE id = $1", hh.ID)
	})

	conn := &signaling.Conn{Send: make(chan []byte, 10)}
	h.Hub().Register("3140301", conn)

	w := postDoNotDisturb(t, h, cookie, "false")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}

	got, err := h.householdStore.GetByID(context.Background(), hh.ID)
	if err != nil {
		t.Fatalf("get household: %v", err)
	}
	if got.DoNotDisturb {
		t.Fatalf("expected DoNotDisturb=false after POST, got true")
	}

	// Household DND is now false but the line itself has silent_mode=true,
	// so the OR'd push should still carry SilentMode=true.
	expectLineSettingsPush(t, conn, true)
}

func TestHandleSettingsDoNotDisturbPost_NoHouseholdRedirects(t *testing.T) {
	h, _, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)

	w := postDoNotDisturb(t, h, cookie, "true")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect for user without household, got %d: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if loc != "/settings" && loc != "/onboard" {
		t.Fatalf("expected redirect to /settings or /onboard, got %q", loc)
	}
}
