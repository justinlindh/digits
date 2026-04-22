//go:build integration

// Screen-level compliance tests for the intercom theme redesign. Each test
// asserts that the rendered HTML for a given page contains the exact
// strings, classes, and structural hooks the redesign committed to. The
// intent is to catch regressions that visual review alone would miss:
// dropped greeting lines, missing sub-labels, renamed classes.
//
// Conventions:
//   - One test function per screen, named TestSpec<Screen>.
//   - Each assertion is followed by a short comment stating the rule it
//     enforces, so a failure points straight at the broken requirement.
//   - Setup reuses existing helpers (setupHandler, setupAuthedHousehold,
//     seedLinkedFamily, h.lineStore.Add) so these tests add no new
//     fixtures.
//
// Runs under the `integration` build tag alongside handler_test.go:
//
//   go test -tags=integration -run 'TestSpec' ./internal/web/...
package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/justinlindh/digits/server/internal/auth"
)

// TestSpecDashboard covers the Dashboard section of the spec.
func TestSpecDashboard(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	user, err := authStore.GetUserByEmail("test@example.com")
	if err != nil {
		t.Fatalf("get test user: %v", err)
	}
	// Seed minimum data for the spec checks: own lines, a linked family,
	// history enabled so the Today panel renders.
	if _, err := h.lineStore.Add("2456390", "Kitchen", hh.ID); err != nil {
		t.Fatalf("seed own line: %v", err)
	}
	if _, err := h.lineStore.Add("2486881", "Living room", hh.ID); err != nil {
		t.Fatalf("seed own line: %v", err)
	}
	seedLinkedFamily(t, h, database, authStore, hh.ID, user.ID, "Grandma Lindh", "2180042", "Grandma")
	if err := h.householdStore.SetCallHistoryEnabled(hh.ID, true); err != nil {
		t.Fatalf("enable call history: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	body := w.Body.String()

	// Title row: household name, no subtitle.
	// (Spec: "Title row: household name (or 'Overview') at left, no subtitle.")
	if !strings.Contains(body, hh.Name) {
		t.Errorf("dashboard missing household name %q", hh.Name)
	}

	// Room cards grid: one card per line.
	// (Spec: "Room cards grid: one card per line.")
	for _, want := range []string{
		`class="rooms"`,
		`class="rooms__card`,
		"Kitchen",
		"Living room",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q (room cards + names)", want)
		}
	}

	// KPI strip removed.
	// (Spec: "Remove: the four-cell KPI strip.")
	if strings.Contains(body, `class="strip"`) {
		t.Errorf("dashboard still renders old KPI .strip markup")
	}

	// Today panel header: "Today" with "N calls · M min total" sub-label.
	// (Spec: "The panel header reads 'Today' with a 'N calls · M min total' sub-label.")
	if !strings.Contains(body, `id="today-panel"`) {
		t.Errorf("Today panel missing when call history is enabled")
	}
	if !strings.Contains(body, "min total") {
		t.Errorf("Today sub-label missing 'min total' (spec requires 'N calls · M min total')")
	}

	// Connected families row present.
	// (Spec: "Connected families row: existing chip list.")
	for _, want := range []string{
		`class="connected-row"`,
		`class="connected-row__chip"`,
		"Grandma Lindh",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q (connected families chip row)", want)
		}
	}
}

// TestSpecLines covers the Lines section of the spec.
func TestSpecLines(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	if _, err := h.lineStore.Add("2456390", "Kitchen", hh.ID); err != nil {
		t.Fatalf("seed line: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/phones", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	body := w.Body.String()

	// Panel title "Pair a new handset" (Spec: copy change).
	if !strings.Contains(body, "Pair a new handset") {
		t.Errorf("Lines missing renamed panel title 'Pair a new handset'")
	}
	if strings.Contains(body, ">Pair a device<") {
		t.Errorf("Lines still shows old panel title 'Pair a device'")
	}

	// Field label, placeholder, helper (Spec: the core pair-flow nudge).
	for _, want := range []string{
		"Handset name",
		"Kitchen · Grandma&#39;s bedroom · Garage", // html/template escapes the apostrophe
		"Most families name handsets by where they live",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Lines pair field missing %q", want)
		}
	}
	if strings.Contains(body, ">Line name<") {
		t.Errorf("Lines still shows old field label 'Line name'")
	}

	// Firmware column on Your lines table (Spec: placeholder-only in this round).
	if !strings.Contains(body, ">Firmware<") {
		t.Errorf("Lines table missing Firmware column header")
	}
}

// TestSpecFamilies covers the Families section of the spec.
func TestSpecFamilies(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	user, err := authStore.GetUserByEmail("test@example.com")
	if err != nil {
		t.Fatalf("get test user: %v", err)
	}
	seedLinkedFamily(t, h, database, authStore, hh.ID, user.ID, "Grandma Lindh", "2180042", "Grandma")

	// Default view — no postcard yet.
	req := httptest.NewRequest(http.MethodGet, "/links", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	body := w.Body.String()

	// Page heading stays.
	// (Spec: "Page heading stays Connected families.")
	if !strings.Contains(body, `Connected families`) {
		t.Errorf("Families page missing 'Connected families' heading")
	}

	// Hub-and-spoke hero kept.
	// (Spec: "The hub-and-spoke hero stays.")
	if !strings.Contains(body, `class="hub"`) {
		t.Errorf("Families page missing hub-and-spoke hero")
	}

	// Two-pane Neighborhood.
	// (Spec: "Replace the 'compact list' section with two-pane address-book rows.")
	for _, want := range []string{
		`class="neighborhood`,
		`class="neighborhood__row"`,
		`class="neighborhood__identity"`,
		`class="neighborhood__lines"`,
		"Grandma Lindh",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Families page missing %q (neighborhood two-pane)", want)
		}
	}

	// Invite CTA copy.
	// (Spec: "Generate invite code -> Invite a friend.")
	if !strings.Contains(body, "Invite a friend") {
		t.Errorf("Families page missing 'Invite a friend' CTA")
	}
	if strings.Contains(body, "Generate invite code") {
		t.Errorf("Families page still shows old 'Generate invite code' CTA")
	}

	// Postcard view — pass ?created= so the postcard is gated in.
	req2 := httptest.NewRequest(http.MethodGet, "/links?created=DEMO-123", nil)
	req2.AddCookie(cookie)
	w2 := httptest.NewRecorder()
	h.Router().ServeHTTP(w2, req2)
	body2 := w2.Body.String()

	// Postcard visual + the spec-locked copy.
	// (Spec: postcard body reads "To the <recipient or blank>," then
	// "Here's our code. Paste it into app.digits.family/links ...")
	for _, want := range []string{
		`class="postcard"`,
		"DEMO-123",
		"To our friends",                       // greeting ("recipient or blank")
		"Paste it into",                        // body line 1
		"your handsets and ours can dial each", // body line 2 fragment
		"line number",                          // body line 2 tail
		"FAMILY MAIL",                          // stamp
	} {
		if !strings.Contains(body2, want) {
			t.Errorf("Families postcard missing %q", want)
		}
	}
}

// TestSpecSettings covers the Settings section of the spec.
func TestSpecSettings(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	body := w.Body.String()

	// Two-column layout + sticky nav markers.
	// (Spec: "Layout: two columns, grid 180px 1fr. Left column is a sticky nav.")
	for _, want := range []string{
		`class="settings-layout"`,
		`class="settings-nav"`,
		`href="#account"`,
		`href="#household"`,
		`href="#timezone"`,
		`href="#theme"`,
		`href="#privacy"`,
		`href="#session"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Settings page missing %q (sticky nav + anchors)", want)
		}
	}

	// Theme cards + every swatch hex value (Spec: explicit palettes).
	// (Spec: "Intercom palette: #f5f1ea, #2e231b, #c48b3a. Dialup palette:
	//  #0a3a8a, #7ab4ff, #f0c020.")
	for _, want := range []string{
		`class="theme-card`,
		"theme-card__swatch",
		"#f5f1ea", "#2e231b", "#c48b3a",
		"#0a3a8a", "#7ab4ff", "#f0c020",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Settings theme section missing %q", want)
		}
	}

	// Privacy copy verbatim (Spec-locked string).
	// (Spec: "Only numbers, timestamps, and duration. Never audio. Digits was
	//  built on the idea kids deserve the same phone privacy you grew up
	//  with. Most families don't need this.")
	if !strings.Contains(body, "kids deserve the same phone privacy you grew up with") {
		t.Errorf("Settings privacy copy not updated to the spec-locked wording")
	}
}

// TestSpecCallLog covers the Call history -> Call log rename.
func TestSpecCallLog(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	// /calls redirects to /settings when history is disabled; enable so the
	// page renders and we can assert its heading.
	if err := h.householdStore.SetCallHistoryEnabled(hh.ID, true); err != nil {
		t.Fatalf("enable call history: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/calls", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	body := w.Body.String()

	// Page heading + <title>.
	// (Spec: "Call history -> Call log.")
	if !strings.Contains(body, ">Call log<") {
		t.Errorf("Calls page missing 'Call log' heading")
	}
	if strings.Contains(body, ">Call history<") {
		t.Errorf("Calls page still renders old 'Call history' heading")
	}

	// Settings toggle label also renamed.
	req2 := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req2.AddCookie(cookie)
	w2 := httptest.NewRecorder()
	h.Router().ServeHTTP(w2, req2)
	if !strings.Contains(w2.Body.String(), "Call log") {
		t.Errorf("Settings page missing 'Call log' toggle label")
	}
}

// TestSpecDialupHubGlyph asserts the dialup theme renders the new
// pixel-art house SVG on /links, not the intercom line-drawing.
// Spec: Option D of the 2026-04-21 dialup-theme-port design.
// Each family spoke (and the center household) shows a chunky Win98
// pixel-art house with a roof-mounted antenna and signal arcs.
func TestSpecDialupHubGlyph(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	user, err := authStore.GetUserByEmail("test@example.com")
	if err != nil {
		t.Fatalf("get test user: %v", err)
	}
	if err := authStore.SetTheme(user.ID, auth.ThemeDialup); err != nil {
		t.Fatalf("set dialup theme: %v", err)
	}
	seedLinkedFamily(t, h, database, authStore, hh.ID, user.ID, "Grandma Lindh", "2180042", "Grandma")

	req := httptest.NewRequest(http.MethodGet, "/links", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /links: got %d, want 200", w.Code)
	}
	body := w.Body.String()

	// The dialup-specific SVG uses <g class="dialup-house__antenna">
	// which the intercom template never emits. Presence proves the
	// hub spoke + center switched to the pixel-art glyph for dialup.
	if !strings.Contains(body, `dialup-house__antenna`) {
		t.Error("dialup /links should render the dialup house SVG (class=dialup-house__antenna)")
	}
}
