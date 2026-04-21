// devpreview: scratch server for the webapp redesign spike. Renders every
// page with fixture data on :18080, no database or auth. Not shipped.
package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/household"
	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/signaling"
	"github.com/justinlindh/digits/server/internal/updates"
	"github.com/justinlindh/digits/server/internal/web"
)

type lineRow struct {
	Line   line.Line
	Online bool
}

type dashStats struct {
	TotalLines  int
	OnlineLines int
	ActiveCalls int
	CallsToday  int
}

type linkedFamily struct {
	ID         string
	Name       string
	Lines      []line.Line
	Status     string
	AcceptedAt *time.Time
}
type linkRow struct {
	ID         string
	InviteCode string
	Status     string
	CreatedAt  time.Time
}

func ptrTime(t time.Time) *time.Time { return &t }

func main() {
	tmplFS := web.TemplateFS()
	funcs := template.FuncMap{"fmtPhone": line.FormatNumber}

	parse := func(pages ...string) *template.Template {
		t, err := template.New("").Funcs(funcs).ParseFS(tmplFS, pages...)
		if err != nil {
			log.Fatalf("parse %v: %v", pages, err)
		}
		return t
	}

	tDash := parse("templates/layout-v2.html", "templates/layout-aol.html", "templates/dashboard.html")
	tPhones := parse("templates/layout-v2.html", "templates/layout-aol.html", "templates/phones.html")
	tPhoneDetail := parse("templates/layout-v2.html", "templates/layout-aol.html", "templates/phone-detail.html")
	tLinks := parse("templates/layout-v2.html", "templates/layout-aol.html", "templates/links.html")
	tCalls := parse("templates/layout-v2.html", "templates/layout-aol.html", "templates/calls.html")
	tSettings := parse("templates/layout-v2.html", "templates/layout-aol.html", "templates/settings.html")
	tOnboard := parse("templates/layout-v2.html", "templates/layout-aol.html", "templates/onboard.html")
	tLogin := parse("templates/layout-v2.html", "templates/layout-aol.html", "templates/login.html")

	// ---- Fixture data ----

	household_ := &household.Household{
		ID:                 "hh-1",
		Name:               "Justin Lindh's Family",
		CallHistoryEnabled: true,
		Timezone:           "America/Los_Angeles",
		CreatedAt:          time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC),
	}
	googleID := "g-12345"
	user := &auth.User{
		ID:       "u-1",
		Email:    "justinlindh@gmail.com",
		Name:     "Justin Lindh",
		GoogleID: &googleID,
	}
	lines := []lineRow{
		{Line: line.Line{ID: 1, Number: "2456390", Name: "Digits 1", CreatedAt: time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC), Settings: line.Settings{VoiceStyle: "copper"}}, Online: true},
		{Line: line.Line{ID: 2, Number: "2486881", Name: "Digits 2", CreatedAt: time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC), Settings: line.Settings{VoiceStyle: "modern"}}, Online: true},
		{Line: line.Line{ID: 3, Number: "5793721", Name: "Digits 3", CreatedAt: time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC), Settings: line.Settings{VoiceStyle: "copper"}}, Online: false},
	}
	version := "1.15.0-spike"

	baseCtx := func(page string) map[string]any {
		return map[string]any{
			"Page":               page,
			"Version":            version,
			"CallHistoryEnabled": true,
			"HouseholdName":      household_.Name,
		}
	}

	// ---- Handlers ----

	layoutFor := func(r *http.Request) string {
		if r.URL.Query().Get("theme") == "aol" {
			return "layout-aol.html"
		}
		return "layout-v2.html"
	}

	render := func(w http.ResponseWriter, r *http.Request, t *template.Template, data any) {
		if err := t.ExecuteTemplate(w, layoutFor(r), data); err != nil {
			log.Printf("render %s: %v", layoutFor(r), err)
			http.Error(w, err.Error(), 500)
		}
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data := baseCtx("dashboard")
		data["Stats"] = dashStats{TotalLines: 3, OnlineLines: 2, ActiveCalls: 0, CallsToday: 1}
		data["Lines"] = lines
		render(w, r, tDash, data)
	})

	mux.HandleFunc("/phones", func(w http.ResponseWriter, r *http.Request) {
		data := baseCtx("phones")
		data["Lines"] = lines
		render(w, r, tPhones, data)
	})

	mux.HandleFunc("/phones/2456390", func(w http.ResponseWriter, r *http.Request) {
		data := baseCtx("phones")
		data["Line"] = lines[0].Line
		data["Online"] = true
		data["DeviceInfo"] = &signaling.DeviceInfoSnapshot{
			PiVersion:       "1.14.1-4",
			PiCommit:        "ge35f5bf",
			FirmwareVersion: "v0.8.2",
			FirmwareCommit:  "a1b2c3d",
			FlashCapable:    true,
		}
		data["LastSeenAt"] = ptrTime(time.Now().Add(-12 * time.Minute))
		data["LatestPiVersion"] = "1.15.0"
		data["LatestFirmwareVersion"] = "v0.8.2"
		data["PiReleases"] = []updates.Release{
			{Version: "1.15.0", Date: "2026-04-18"},
			{Version: "1.14.1-4", Date: "2026-04-05"},
			{Version: "1.14.0", Date: "2026-03-22"},
		}
		data["FWReleases"] = []updates.Release{
			{Version: "v0.8.2", Date: "2026-04-14"},
			{Version: "v0.8.1", Date: "2026-03-30"},
		}
		render(w, r, tPhoneDetail, data)
	})

	mux.HandleFunc("/links", func(w http.ResponseWriter, r *http.Request) {
		data := baseCtx("links")
		data["LinkedFamilies"] = []linkedFamily{
			{
				ID:         "link-1",
				Name:       "The Patel Family",
				Status:     "connected",
				AcceptedAt: ptrTime(time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC)),
				Lines: []line.Line{
					{Number: "6128844", Name: "Ravi's Phone"},
					{Number: "6128801", Name: "Kitchen"},
				},
			},
			{
				ID:         "link-2",
				Name:       "The O'Connell Family",
				Status:     "connected",
				AcceptedAt: ptrTime(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)),
				Lines: []line.Line{
					{Number: "5031234", Name: "Grandma's Kitchen"},
					{Number: "5031235", Name: "Den"},
				},
			},
			{
				ID:         "link-3",
				Name:       "Uncle Mike",
				Status:     "connected",
				AcceptedAt: ptrTime(time.Date(2026, 4, 8, 0, 0, 0, 0, time.UTC)),
				Lines: []line.Line{
					{Number: "2015550", Name: "Workshop"},
				},
			},
		}
		data["PendingInvites"] = []linkRow{
			{ID: "inv-1", InviteCode: "K7F-Q29", Status: "pending", CreatedAt: time.Now().Add(-2 * 24 * time.Hour)},
		}
		render(w, r, tLinks, data)
	})

	mux.HandleFunc("/links/solo", func(w http.ResponseWriter, r *http.Request) {
		data := baseCtx("links")
		data["LinkedFamilies"] = []linkedFamily{
			{
				ID:         "link-1",
				Name:       "The Patel Family",
				Status:     "connected",
				AcceptedAt: ptrTime(time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC)),
				Lines: []line.Line{
					{Number: "6128844", Name: "Ravi's Phone"},
					{Number: "6128801", Name: "Kitchen"},
				},
			},
		}
		data["PendingInvites"] = []linkRow{}
		render(w, r, tLinks, data)
	})

	mux.HandleFunc("/links/created", func(w http.ResponseWriter, r *http.Request) {
		// alt variant: just created an invite
		data := baseCtx("links")
		data["CreatedCode"] = "K7F-Q29"
		data["LinkedFamilies"] = []linkedFamily{}
		data["PendingInvites"] = []linkRow{}
		render(w, r, tLinks, data)
	})

	mux.HandleFunc("/calls", func(w http.ResponseWriter, r *http.Request) {
		data := baseCtx("calls")
		now := time.Now()
		data["Calls"] = []calls.Call{
			{ID: 1, Caller: "2456390", Callee: "6128844", Status: "ended", StartedAt: now.Add(-2 * time.Hour), DurationS: 124},
			{ID: 2, Caller: "6128801", Callee: "2486881", Status: "missed", StartedAt: now.Add(-26 * time.Hour), DurationS: 0},
			{ID: 3, Caller: "2486881", Callee: "6128844", Status: "connected", StartedAt: now.Add(-4 * time.Minute), DurationS: 240},
			{ID: 4, Caller: "2456390", Callee: "6128801", Status: "ended", StartedAt: now.Add(-3 * 24 * time.Hour), DurationS: 38},
		}
		render(w, r, tCalls, data)
	})

	mux.HandleFunc("/settings", func(w http.ResponseWriter, r *http.Request) {
		data := baseCtx("settings")
		data["User"] = user
		data["Household"] = household_
		data["Saved"] = false
		render(w, r, tSettings, data)
	})

	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		data := map[string]any{
			"Page":          "login",
			"Version":       version,
			"GoogleEnabled": true,
		}
		render(w, r, tLogin, data)
	})

	mux.HandleFunc("/onboard", func(w http.ResponseWriter, r *http.Request) {
		data := map[string]any{
			"Page":          "onboard",
			"Version":       version,
			"SuggestedName": "Justin Lindh's Family",
		}
		render(w, r, tOnboard, data)
	})

	mux.HandleFunc("/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})

	// Static files
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("internal/web/static"))))

	addr := ":18080"
	fmt.Printf("devpreview: http://localhost%s/\n", addr)
	fmt.Printf("  /  /phones  /phones/2456390  /links  /links/created  /calls  /settings  /login  /onboard\n")
	log.Fatal(http.ListenAndServe(addr, mux))
}
