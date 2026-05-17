//go:build integration

package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/household"
)

func TestCallsPageReturns200(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)
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
	cookie, _ := setupAuthedHousehold(t, h, database, authStore)
	req := httptest.NewRequest(http.MethodGet, "/calls?before=notbase64!!", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad cursor, got %d", w.Code)
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
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Call log") {
		t.Errorf("calls page missing 'Call log' title: %s", body)
	}
	if strings.Contains(body, "Call history") {
		t.Errorf("calls page still shows old 'Call history' title")
	}
}

// TestHandleCalls_Intercom_PaginationControls covers the intercom (default)
// theme's behavior around pagination controls and auto-refresh attributes:
//   - Page 1 with no older entries: no pagination nav, hx-trigger present.
//   - Page 1 with an older page available: nav rendered, Older link with cursor,
//     hx-trigger present.
//   - Page 2 (paged via ?before=cursor): nav rendered, Newer link to /calls,
//     hx-trigger absent (snapshot mode).
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
