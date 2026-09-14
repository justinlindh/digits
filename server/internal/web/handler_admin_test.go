package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/signaling"
)

func TestAdminDeviceRows(t *testing.T) {
	lines := []line.Line{
		{Number: "1000001", Name: "Kitchen", HouseholdID: "hh-a"},
		{Number: "1000002", Name: "Den", HouseholdID: "hh-a"},
		{Number: "2000001", Name: "Hall", HouseholdID: "hh-b"},
	}
	names := map[string]string{"hh-a": "Alpha", "hh-b": "Beta"}
	infos := map[string][]signaling.DeviceInfoSnapshot{
		// Two devices on one line: the oldest version wins, as on /phones.
		"1000001": {
			{PiVersion: "1.2.0", FirmwareVersion: "0.9.0"},
			{PiVersion: "1.1.0", FirmwareVersion: "1.0.0"},
		},
		"1000002": {{PiVersion: "1.2.0", FirmwareVersion: "1.0.0"}},
	}
	infoFor := func(number string) []signaling.DeviceInfoSnapshot { return infos[number] }

	rows := adminDeviceRows(lines, names, infoFor, "1.2.0", "1.0.0")
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	kitchen, den, hall := rows[0], rows[1], rows[2]
	if kitchen.Household != "Alpha" || kitchen.Name != "Kitchen" || !kitchen.Online {
		t.Errorf("kitchen = %+v", kitchen)
	}
	if kitchen.PiVersion != "1.1.0" || !kitchen.PiBehind || kitchen.FirmwareVersion != "0.9.0" || !kitchen.FirmwareBehind {
		t.Errorf("kitchen versions = %+v, want oldest of each and both behind", kitchen)
	}
	if den.PiBehind || den.FirmwareBehind || den.PiVersion != "1.2.0" {
		t.Errorf("den = %+v, want current on both", den)
	}
	if hall.Online || hall.PiVersion != "" || hall.FirmwareVersion != "" || hall.PiBehind || hall.FirmwareBehind {
		t.Errorf("hall = %+v, want offline with no versions", hall)
	}
	if hall.Household != "Beta" {
		t.Errorf("hall.Household = %q, want Beta", hall.Household)
	}
}

func TestAdminDeviceRows_NoLatestMeansNothingBehind(t *testing.T) {
	lines := []line.Line{{Number: "1000001", Name: "Kitchen", HouseholdID: "hh-a"}}
	infoFor := func(string) []signaling.DeviceInfoSnapshot {
		return []signaling.DeviceInfoSnapshot{{PiVersion: "0.1.0", FirmwareVersion: "0.1.0"}}
	}
	rows := adminDeviceRows(lines, map[string]string{}, infoFor, "", "")
	if rows[0].PiBehind || rows[0].FirmwareBehind {
		t.Errorf("rows[0] = %+v, want nothing flagged behind without a release index", rows[0])
	}
	if rows[0].Household != "" {
		t.Errorf("unknown household should render empty, got %q", rows[0].Household)
	}
}

func TestRequireAdmin(t *testing.T) {
	cases := []struct {
		name     string
		allow    []string
		user     *auth.User
		wantCode int
	}{
		{"no allowlist configured", nil, &auth.User{Email: "admin@example.com"}, http.StatusNotFound},
		{"no user in context", []string{"admin@example.com"}, nil, http.StatusNotFound},
		{"user not on allowlist", []string{"admin@example.com"}, &auth.User{Email: "someone@example.com"}, http.StatusNotFound},
		{"user on allowlist", []string{"admin@example.com"}, &auth.User{Email: "admin@example.com"}, http.StatusOK},
		{"email match is case-insensitive", []string{"admin@example.com"}, &auth.User{Email: "Admin@Example.COM"}, http.StatusOK},
		{"second entry matches", []string{"first@example.com", "admin@example.com"}, &auth.User{Email: "admin@example.com"}, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, err := NewHandler(Deps{}, HandlerConfig{AdminEmails: tc.allow})
			if err != nil {
				t.Fatalf("NewHandler: %v", err)
			}
			called := false
			gated := h.requireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, "/admin", nil)
			if tc.user != nil {
				req = req.WithContext(auth.ContextWithUser(req.Context(), tc.user))
			}
			w := httptest.NewRecorder()
			gated.ServeHTTP(w, req)
			if w.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", w.Code, tc.wantCode)
			}
			if called != (tc.wantCode == http.StatusOK) {
				t.Errorf("next called = %v, want %v", called, tc.wantCode == http.StatusOK)
			}
		})
	}
}
