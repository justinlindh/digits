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

	"github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/signaling"
)

func validVoicemailForm() url.Values {
	return url.Values{
		"enabled":              {"on"},
		"ring_timeout_seconds": {"25"},
	}
}

// postVoicemail posts the given form to the per-line voicemail endpoint as
// the session in cookie. When htmx is true the HX-Request header is set so
// the handler renders the section partial instead of 303-redirecting.
func postVoicemail(t *testing.T, h *Handler, cookie *http.Cookie, form url.Values, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/phones/3140001/voicemail", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	return w
}

func readVoicemailJSON(t *testing.T, database *db.Database) string {
	t.Helper()
	var raw string
	if err := database.DB.QueryRow(
		`SELECT COALESCE(settings->'voicemail', '{}'::jsonb)::text FROM lines WHERE number = '3140001'`,
	).Scan(&raw); err != nil {
		t.Fatalf("read voicemail: %v", err)
	}
	return raw
}

func postVoicemailToggle(t *testing.T, h *Handler, cookie *http.Cookie, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/phones/3140001/voicemail-toggle", nil)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	return w
}

func readVoicemailEnabled(t *testing.T, database *db.Database) bool {
	t.Helper()
	var raw bool
	if err := database.DB.QueryRow(
		`SELECT COALESCE((settings->'voicemail'->>'enabled')::bool, false) FROM lines WHERE number = '3140001'`,
	).Scan(&raw); err != nil {
		t.Fatalf("read voicemail enabled: %v", err)
	}
	return raw
}

func TestPhoneVoicemailValidFormPersistsAndRedirects(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	w := postVoicemail(t, h, cookie, validVoicemailForm(), false)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/phones/3140001" {
		t.Fatalf("expected redirect to /phones/3140001, got %q", loc)
	}
	got := readVoicemailJSON(t, database)
	for _, want := range []string{
		`"enabled": true`,
		`"ring_timeout_seconds": 25`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("voicemail JSONB missing %q in %s", want, got)
		}
	}
	// Removed fields must not reappear in new writes.
	for _, banned := range []string{"max_message_seconds", "max_stored_messages", "retrieval_code"} {
		if strings.Contains(got, banned) {
			t.Errorf("voicemail JSONB should not carry %q, got %s", banned, got)
		}
	}
}

func TestPhoneVoicemailRingTimeoutOutOfRangeRejected(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	cases := []string{"0", "1", "4", "61", "999", "abc", ""}
	for _, val := range cases {
		t.Run(val, func(t *testing.T) {
			form := validVoicemailForm()
			form.Set("ring_timeout_seconds", val)
			w := postVoicemail(t, h, cookie, form, false)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("ring_timeout_seconds=%q: expected 400, got %d: %s", val, w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "ring_timeout_seconds") {
				t.Errorf("400 body should name the field, got %q", w.Body.String())
			}
		})
	}
}

func TestPhoneVoicemailMissingLineReturns404(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	user, err := authStore.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		t.Fatalf("get test user: %v", err)
	}
	hh, err := h.householdStore.Create(context.Background(), "Voicemail Missing", user.ID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE household_id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM households WHERE id = $1", hh.ID)
	})

	w := postVoicemail(t, h, cookie, validVoicemailForm(), false)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing line, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPhoneVoicemailPushesToConnectedDevice(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	conn := &signaling.Conn{Send: make(chan []byte, 10)}
	_ = h.hub.Register("3140001", conn)

	if w := postVoicemail(t, h, cookie, validVoicemailForm(), false); w.Code != http.StatusSeeOther {
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
		if msg.LineSettings == nil || msg.LineSettings.Voicemail == nil {
			t.Fatalf("expected non-nil Voicemail in push, got %+v", msg.LineSettings)
		}
		got := *msg.LineSettings.Voicemail
		want := signaling.Voicemail{
			Enabled:            true,
			RingTimeoutSeconds: 25,
		}
		if got != want {
			t.Errorf("Voicemail push: got %+v, want %+v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("device did not receive line_settings push")
	}
}

func TestPhoneVoicemailNoOpSkipsPush(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	// First save populates the row with the validVoicemailForm values.
	if w := postVoicemail(t, h, cookie, validVoicemailForm(), false); w.Code != http.StatusSeeOther {
		t.Fatalf("setup save: %d %s", w.Code, w.Body.String())
	}

	// Now connect a device and re-submit the identical form. No change
	// should produce no push.
	conn := &signaling.Conn{Send: make(chan []byte, 10)}
	_ = h.hub.Register("3140001", conn)

	if w := postVoicemail(t, h, cookie, validVoicemailForm(), false); w.Code != http.StatusSeeOther {
		t.Fatalf("second save: %d %s", w.Code, w.Body.String())
	}
	select {
	case data := <-conn.Send:
		t.Fatalf("expected no push on no-op save, got: %s", string(data))
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPhoneVoicemailHTMXReturnsSectionPartial(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	w := postVoicemail(t, h, cookie, validVoicemailForm(), true)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="voicemail-section"`) {
		t.Fatalf("htmx response missing voicemail-section wrapper:\n%s", body)
	}
	if !strings.Contains(body, `name="enabled"`) {
		t.Fatalf("htmx response missing enabled checkbox:\n%s", body)
	}
	if !strings.Contains(body, `name="ring_timeout_seconds"`) {
		t.Fatalf("htmx response missing ring_timeout_seconds:\n%s", body)
	}
}

func TestPhoneVoicemailToggleFlipsEnabledAndRedirects(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	if !readVoicemailEnabled(t, database) {
		t.Fatal("setup invariant: voicemail should default to on")
	}

	w := postVoicemailToggle(t, h, cookie, false)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}
	if readVoicemailEnabled(t, database) {
		t.Fatal("expected voicemail enabled=false after toggle")
	}

	if w := postVoicemailToggle(t, h, cookie, false); w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 on second toggle, got %d", w.Code)
	}
	if !readVoicemailEnabled(t, database) {
		t.Fatal("expected voicemail enabled=true after second toggle")
	}
}

func TestPhoneVoicemailToggleHTMXReturnsSectionPartial(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	// First toggle: on -> off (default is enabled).
	w := postVoicemailToggle(t, h, cookie, true)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="voicemail-section"`) {
		t.Fatalf("htmx response missing voicemail-section wrapper:\n%s", body)
	}
	if !strings.Contains(body, "/voicemail-toggle") {
		t.Fatalf("htmx response missing toggle endpoint:\n%s", body)
	}
	// After toggling off, fields should be disabled.
	if !strings.Contains(body, `disabled`) {
		t.Errorf("expected disabled attr on inner fields when off:\n%s", body)
	}

	// Second toggle: off -> on. Fields should be enabled again.
	w = postVoicemailToggle(t, h, cookie, true)
	body = w.Body.String()
	if strings.Contains(body, `disabled`) {
		t.Errorf("expected no disabled attr on inner fields when on:\n%s", body)
	}
}

func TestPhoneVoicemailTogglePushesToConnectedDevice(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie := addSessionCookie(t, authStore)
	_ = setupVoiceStyleLine(t, h, database, authStore)

	conn := &signaling.Conn{Send: make(chan []byte, 10)}
	_ = h.hub.Register("3140001", conn)

	// Default is enabled. First toggle: on -> off. Drain the push.
	if w := postVoicemailToggle(t, h, cookie, false); w.Code != http.StatusSeeOther {
		t.Fatalf("first toggle failed: %d %s", w.Code, w.Body.String())
	}
	select {
	case <-conn.Send:
	case <-time.After(time.Second):
		t.Fatal("device did not receive push after first toggle")
	}

	// Second toggle: off -> on. Verify the push carries enabled=true.
	if w := postVoicemailToggle(t, h, cookie, false); w.Code != http.StatusSeeOther {
		t.Fatalf("second toggle failed: %d %s", w.Code, w.Body.String())
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
		if msg.LineSettings == nil || msg.LineSettings.Voicemail == nil || !msg.LineSettings.Voicemail.Enabled {
			t.Fatalf("expected enabled=true voicemail push, got %+v", msg.LineSettings)
		}
	case <-time.After(time.Second):
		t.Fatal("device did not receive line_settings push after toggle")
	}
}

func TestPhonesListShowsVoicemailUnheardBadge(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	setupLineWithConn(t, h, database, hh, "3140042", "Kitchen")

	// Hub reports unheard messages for the line. Voicemail is enabled by
	// default on a new line, so the badge should render.
	h.hub.SetVoicemailUnheard("3140042", "hw-fake", 3)

	req := httptest.NewRequest(http.MethodGet, "/phones", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "chip--msg") {
		t.Errorf("expected chip--msg unheard badge on phones list:\n%s", body)
	}
	if !strings.Contains(body, "3 unheard") {
		t.Errorf("expected '3 unheard' label on phones list:\n%s", body)
	}
}

func TestPhonesListOmitsVoicemailBadgeWhenNoUnheard(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	setupLineWithConn(t, h, database, hh, "3140043", "Hallway")

	// No SetVoicemailUnheard call: the hub reports a zero count.
	req := httptest.NewRequest(http.MethodGet, "/phones", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "chip--msg") {
		t.Errorf("expected no unheard badge when count is zero:\n%s", w.Body.String())
	}
}
