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

func TestHandleSettingsDoNotDisturbPost(t *testing.T) {
	cases := []struct {
		name             string
		seedHouseholdDND bool
		seedLineSilent   bool
		numLines         int
		postEnabled      string
		wantStored       bool
		wantWireSilent   bool
	}{
		{
			name:             "persists and fans out to multiple lines",
			seedHouseholdDND: false,
			seedLineSilent:   false,
			numLines:         2,
			postEnabled:      "true",
			wantStored:       true,
			wantWireSilent:   true,
		},
		{
			name:             "off sends false when no per-line silent",
			seedHouseholdDND: true,
			seedLineSilent:   false,
			numLines:         1,
			postEnabled:      "false",
			wantStored:       false,
			wantWireSilent:   false,
		},
		{
			name:             "off preserves per-line silent",
			seedHouseholdDND: true,
			seedLineSilent:   true,
			numLines:         1,
			postEnabled:      "false",
			wantStored:       false,
			wantWireSilent:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, database, authStore := setupHandler(t)
			cookie, hh := setupAuthedHousehold(t, h, database, authStore)

			if tc.seedHouseholdDND {
				if err := h.householdStore.SetDoNotDisturb(context.Background(), hh.ID, true); err != nil {
					t.Fatalf("seed DND=true: %v", err)
				}
			}

			conns := make([]*signaling.Conn, 0, tc.numLines)
			for i := 0; i < tc.numLines; i++ {
				number := nextPhone()
				ln, conn := setupLineWithConn(t, h, database, hh, number, "Phone")
				if tc.seedLineSilent {
					if err := h.lineStore.UpdateSettings(context.Background(), ln.ID, line.Settings{SilentMode: true}); err != nil {
						t.Fatalf("set line silent_mode=true: %v", err)
					}
				}
				conns = append(conns, conn)
			}

			w := postDoNotDisturb(t, h, cookie, tc.postEnabled)
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
			if got.DoNotDisturb != tc.wantStored {
				t.Fatalf("expected DoNotDisturb=%v after POST, got %v", tc.wantStored, got.DoNotDisturb)
			}

			for _, conn := range conns {
				expectLineSettingsPush(t, conn, tc.wantWireSilent)
			}
		})
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
