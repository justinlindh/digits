//go:build integration

package web

// Integration tests for GET /api/dashboard/stream, the SSE endpoint that
// drives the AM-theme top-row counters and clock.
//
// Verified behaviours:
//   - 200 with Content-Type: text/event-stream for an authenticated user
//   - Initial "status" frame contains the household-scoped counters
//   - Hub.Register fires another "status" frame
//   - Tracker.OnCallInitiated fires another "status" frame
//   - Unauthenticated requests are redirected
//
// Requires TEST_DATABASE_URL (skipped otherwise via setupHandler).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/justinlindh/digits/server/internal/signaling"
)

func dashStreamURL(srv *httptest.Server) string {
	return srv.URL + "/api/dashboard/stream"
}

func TestDashboardStream_InitialSnapshot(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	srv := httptest.NewServer(h.Router())
	t.Cleanup(srv.Close)

	// Seed a single line so OnlineLines is at least 1 in the initial frame.
	_, _ = setupLineWithConn(t, h, database, hh, "+15551110001", "Kitchen")

	req, err := http.NewRequest("GET", dashStreamURL(srv), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.AddCookie(cookie)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type: got %q want text/event-stream", ct)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sr := newSSEReader(resp)

	ev, data, err := readSSEFrame(t, ctx, sr)
	if err != nil {
		t.Fatalf("read initial frame: %v", err)
	}
	if ev != "status" {
		t.Fatalf("initial event: got %q want status", ev)
	}
	for _, want := range []string{"am-overview__top", "ON&middot;CALL", "LINES&middot;ONLINE", "FAMILIES", "LOCAL&middot;TIME"} {
		if !strings.Contains(data, want) {
			t.Fatalf("initial frame missing %q; data=%q", want, data)
		}
	}
}

func TestDashboardStream_HubRegisterTriggersFrame(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	srv := httptest.NewServer(h.Router())
	t.Cleanup(srv.Close)

	number := "+15551110010"
	_, _ = setupLineWithConn(t, h, database, hh, number, "Kitchen")
	// setupLineWithConn already registers a conn. Unregister so the test
	// drives a fresh Register below and observes the resulting Notify.
	h.hub.Unregister(number, h.hub.Get(number))

	req, _ := http.NewRequest("GET", dashStreamURL(srv), nil)
	req.AddCookie(cookie)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sr := newSSEReader(resp)

	if _, _, err := readSSEFrame(t, ctx, sr); err != nil {
		t.Fatalf("drain initial: %v", err)
	}

	// Give the handler goroutine time to reach Subscribe() after the
	// initial flush. Same pattern as sse_integration_test.go.
	time.Sleep(50 * time.Millisecond)

	conn := &signaling.Conn{Send: make(chan []byte, 1)}
	_ = h.hub.Register(number, conn)
	t.Cleanup(func() { h.hub.Unregister(number, conn) })

	ev, _, err := readSSEFrame(t, ctx, sr)
	if err != nil {
		t.Fatalf("read post-register frame: %v", err)
	}
	if ev != "status" {
		t.Fatalf("post-register event: got %q want status", ev)
	}
}

func TestDashboardStream_CallInitiatedTriggersFrame(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	srv := httptest.NewServer(h.Router())
	t.Cleanup(srv.Close)

	caller := "+15551110020"
	callee := "+15551110021"
	_, _ = setupLineWithConn(t, h, database, hh, caller, "Caller")

	req, _ := http.NewRequest("GET", dashStreamURL(srv), nil)
	req.AddCookie(cookie)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sr := newSSEReader(resp)

	if _, _, err := readSSEFrame(t, ctx, sr); err != nil {
		t.Fatalf("drain initial: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if _, err := h.tracker.OnCallInitiated(context.Background(), caller, callee); err != nil {
		t.Fatalf("OnCallInitiated: %v", err)
	}
	t.Cleanup(func() {
		_ = h.tracker.OnCallEnded(context.Background(), caller, callee)
		_, _ = database.DB.Exec("DELETE FROM calls WHERE caller = $1 OR callee = $1", caller)
	})

	ev, data, err := readSSEFrame(t, ctx, sr)
	if err != nil {
		t.Fatalf("read post-call frame: %v", err)
	}
	if ev != "status" {
		t.Fatalf("post-call event: got %q want status", ev)
	}
	// One owned endpoint with an active call: ON CALL renders as "01".
	if !strings.Contains(data, "01") {
		t.Errorf("post-call frame missing ON CALL count of 01; data=%q", data)
	}
}

func TestDashboardStream_AuthRequired(t *testing.T) {
	h, _, _ := setupHandler(t)
	srv := httptest.NewServer(h.Router())
	t.Cleanup(srv.Close)

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(dashStreamURL(srv))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("unauthenticated stream returned 200; want redirect or 4xx")
	}
}
