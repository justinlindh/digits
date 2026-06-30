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

func TestHandleSettingsDoNotDisturbPost(t *testing.T) {
	cases := []struct {
		name           string
		seedLineSilent bool
		numLines       int
		postEnabled    string
		wantSilent     bool
	}{
		{
			name:           "silence all sets every line to silent",
			seedLineSilent: false,
			numLines:       2,
			postEnabled:    "true",
			wantSilent:     true,
		},
		{
			name:           "unsilence all clears every line",
			seedLineSilent: true,
			numLines:       2,
			postEnabled:    "false",
			wantSilent:     false,
		},
		{
			name:           "single line silence",
			seedLineSilent: false,
			numLines:       1,
			postEnabled:    "true",
			wantSilent:     true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, database, authStore := setupHandler(t)
			cookie, hh := setupAuthedHousehold(t, h, database, authStore)

			conns := make([]*signaling.Conn, 0, tc.numLines)
			lines := make([]*line.Line, 0, tc.numLines)
			for i := 0; i < tc.numLines; i++ {
				number := nextPhone()
				ln, conn := setupLineWithConn(t, h, database, hh, number, "Phone")
				if tc.seedLineSilent {
					if err := h.lineStore.UpdateSettings(context.Background(), ln.ID, line.Settings{SilentMode: true}); err != nil {
						t.Fatalf("set line silent_mode=true: %v", err)
					}
				}
				conns = append(conns, conn)
				lines = append(lines, ln)
			}

			w := postDoNotDisturb(t, h, cookie, tc.postEnabled)
			if w.Code != http.StatusSeeOther {
				t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
			}

			// Verify per-line silent_mode was set correctly.
			for _, ln := range lines {
				got, err := h.lineStore.GetByID(context.Background(), ln.ID)
				if err != nil {
					t.Fatalf("get line: %v", err)
				}
				if got.Settings.SilentMode != tc.wantSilent {
					t.Errorf("line %s: SilentMode=%v, want %v", ln.Number, got.Settings.SilentMode, tc.wantSilent)
				}
			}

			// Verify push to each connected device.
			for _, conn := range conns {
				expectLineSettingsPush(t, conn, tc.wantSilent)
			}
		})
	}
}

func TestHandleAccountDeletePost(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)

	// Add a line so we can verify it's cleaned up.
	lineStore := line.NewStore(database.DB)
	ln, err := lineStore.Add(context.Background(), "555-0099", "Delete Test", hh.ID)
	if err != nil {
		t.Fatalf("add line: %v", err)
	}

	// Register a connection on the hub.
	conn := &signaling.Conn{Send: make(chan []byte, 10)}
	_ = h.hub.Register(ln.Number, conn)

	// POST to delete account.
	req := httptest.NewRequest(http.MethodPost, "/settings/account/delete", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/auth/login" {
		t.Errorf("expected redirect to /auth/login, got %s", loc)
	}

	// User gone.
	_, err = authStore.GetUserByEmail(context.Background(), "test@example.com")
	if err == nil {
		t.Error("user still exists after account deletion")
	}

	// Household gone (sole member).
	var count int
	database.DB.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM households WHERE id = $1", hh.ID).Scan(&count)
	if count != 0 {
		t.Error("household still exists after sole-member account deletion")
	}

	// Line gone.
	database.DB.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM lines WHERE id = $1", ln.ID).Scan(&count)
	if count != 0 {
		t.Error("line still exists after household deletion")
	}

	// Hub connection unregistered.
	if h.hub.ConnectionCount(ln.Number) != 0 {
		t.Error("hub connection still registered after deletion")
	}
}

func TestHandleAccountDeletePost_MultiMemberHousehold(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)

	// Add a second member.
	var otherUserID string
	err := database.DB.QueryRowContext(context.Background(),
		`INSERT INTO users (email, name) VALUES ('other-del@test.com', 'Other') RETURNING id`,
	).Scan(&otherUserID)
	if err != nil {
		t.Fatalf("seed second user: %v", err)
	}
	t.Cleanup(func() { _, _ = database.DB.Exec("DELETE FROM users WHERE id = $1", otherUserID) })

	if err := h.householdStore.AddMember(context.Background(), otherUserID, hh.ID, "admin"); err != nil {
		t.Fatalf("add second member: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/settings/account/delete", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}

	// User gone.
	_, err = authStore.GetUserByEmail(context.Background(), "test@example.com")
	if err == nil {
		t.Error("user still exists")
	}

	// Household survives.
	var count int
	database.DB.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM households WHERE id = $1", hh.ID).Scan(&count)
	if count != 1 {
		t.Errorf("expected household to survive, got count=%d", count)
	}

	// Other member still present.
	database.DB.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM household_members WHERE household_id = $1", hh.ID).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 remaining member, got %d", count)
	}
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

// TestApplyLineSettings_PushesEffectiveSettingsDuringQuietHoursWindow verifies
// that saving an unrelated line setting while a quiet-hours window is currently
// active pushes SilentMode=true to the device, not the raw stored value.
//
// Without the fix, applyLineSettings would push the raw `next` settings
// (SilentMode=false) and un-silence the device mid-window.
func TestApplyLineSettings_PushesEffectiveSettingsDuringQuietHoursWindow(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)

	number := nextPhone()
	ln, conn := setupLineWithConn(t, h, database, hh, number, "QH Test Phone")

	// Set up a quiet-hours window that is active right now: 00:00-23:59 every
	// day guarantees the window is open at any time of day for the UTC timezone
	// that test households default to. SilentMode is explicitly off so the raw
	// settings have SilentMode=false; only the effective path should return true.
	now := time.Now().UTC()
	// Build a 23-hour window that definitely contains now. We cannot use
	// 00:00-23:59 because equal start/end (or same-minute boundary) is rejected
	// by Normalize; instead anchor start 30 minutes before now and end 30
	// minutes after, clamped to the valid range. Using a full all-days window
	// guarantees the day filter passes regardless of when the test runs.
	startH := now.Hour()
	startM := now.Minute() - 30
	if startM < 0 {
		startM += 60
		startH--
	}
	if startH < 0 {
		startH = 0
		startM = 0
	}
	endH := now.Hour()
	endM := now.Minute() + 30
	if endM >= 60 {
		endM -= 60
		endH++
	}
	if endH >= 24 {
		endH = 23
		endM = 59
	}
	// If the clamping collapsed start==end, fall back to a fixed overnight
	// window guaranteed to contain any time: 00:00 to 23:59 expressed as two
	// distinct minute values by using 00:00-23:58, then verifying separately.
	startStr := fmt.Sprintf("%02d:%02d", startH, startM)
	endStr := fmt.Sprintf("%02d:%02d", endH, endM)
	if startStr == endStr {
		startStr = "00:00"
		endStr = "23:59"
	}

	qhSettings := line.Settings{
		VoiceStyle: line.VoiceStyleCopper,
		SilentMode: false, // explicit off: only quiet hours should silence
		Voicemail:  line.DefaultVoicemail(),
		QuietHours: line.QuietHours{
			Enabled: true,
			Start:   startStr,
			End:     endStr,
			Days:    line.AllDays(),
		},
	}
	if err := h.lineStore.UpdateSettings(context.Background(), ln.ID, qhSettings); err != nil {
		t.Fatalf("seed quiet-hours settings: %v", err)
	}

	// Confirm the effective settings see SilentMode=true (window is open).
	effective, err := h.lineStore.EffectiveSettingsByNumber(context.Background(), number)
	if err != nil {
		t.Fatalf("EffectiveSettingsByNumber: %v", err)
	}
	if !effective.SilentMode {
		t.Fatalf("precondition: quiet-hours window should be active right now, but SilentMode=false (start=%s end=%s)", startStr, endStr)
	}

	// POST an unrelated setting change (auto-update toggle). This triggers
	// applyLineSettings with a new `next` that has SilentMode=false.
	form := url.Values{"auto_update": {"on"}}
	req := httptest.NewRequest(http.MethodPost, "/phones/"+number+"/auto-update", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}

	// The device must receive a push with SilentMode=true (effective), not false (raw).
	expectLineSettingsPush(t, conn, true)
}
