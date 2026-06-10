//go:build integration

package web

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/justinlindh/digits/server/internal/signaling"
)

func TestE2EFullCallFlow(t *testing.T) {
	srv, database, lineStore, tracker, authStore, hub := setupTestServer(t)
	// Create authenticated client for protected route checks
	cookie := addSessionCookie(t, authStore)
	jar := &testCookieJar{cookie: cookie, url: srv.URL}
	authedClient := &http.Client{Jar: jar}

	// Register lines under a real household; the lines.household_id FK
	// rejects bogus UUIDs and the /api/status call goes through the
	// onboarding middleware, which needs a household_members row.
	hhID := seedE2EHousehold(t, database, authStore)
	if _, err := lineStore.Add(context.Background(), "3140001", "Phone A", hhID); err != nil {
		t.Fatalf("add line A: %v", err)
	}
	if _, err := lineStore.Add(context.Background(), "3140002", "Phone B", hhID); err != nil {
		t.Fatalf("add line B: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number IN ('3140001', '3140002')")
	})

	// Connect two phones
	ws1 := dialWS(t, srv)
	ws2 := dialWS(t, srv)

	// Both register. The WS handler requires a non-empty hardware_id; pairing
	// enforcement is skipped here because setupTestServer wires a nil
	// pairingStore, so any stable string works.
	sendMsg(t, ws1, signaling.Message{Type: signaling.TypeRegister, Number: "3140001", HardwareID: "e2e-hw-a"})
	sendMsg(t, ws2, signaling.Message{Type: signaling.TypeRegister, Number: "3140002", HardwareID: "e2e-hw-b"})

	// Both ws goroutines handle their own register; the call below needs ws2's
	// entry to be present in the hub, which the two sendMsg calls don't order.
	waitForRegister(t, hub, "3140001")
	waitForRegister(t, hub, "3140002")

	// Phone 1 calls Phone 2
	sendMsg(t, ws1, signaling.Message{Type: signaling.TypeCall, To: "3140002"})

	// Phone 2 should receive ring
	ring := recvMsg(t, ws2)
	if ring.Type != signaling.TypeRing {
		t.Fatalf("expected ring, got %s", ring.Type)
	}
	if ring.From != "3140001" {
		t.Fatalf("expected ring from 3140001, got %s", ring.From)
	}

	// Phone 2 answers
	sendMsg(t, ws2, signaling.Message{Type: signaling.TypeAnswer, To: "3140001"})

	// Phone 1 receives answer
	answer := recvMsg(t, ws1)
	if answer.Type != signaling.TypeAnswer {
		t.Fatalf("expected answer, got %s", answer.Type)
	}

	// Exchange SDP
	sendMsg(t, ws1, signaling.Message{Type: signaling.TypeSDP, To: "3140002", SDP: "offer-sdp-data"})
	sdp := recvMsg(t, ws2)
	if sdp.Type != signaling.TypeSDP || sdp.SDP != "offer-sdp-data" {
		t.Fatalf("expected sdp offer, got: %+v", sdp)
	}

	// Exchange ICE
	sendMsg(t, ws2, signaling.Message{Type: signaling.TypeICE, To: "3140001", Candidate: "candidate-data"})
	ice := recvMsg(t, ws1)
	if ice.Type != signaling.TypeICE || ice.Candidate != "candidate-data" {
		t.Fatalf("expected ice, got: %+v", ice)
	}

	// Phone 1 hangs up
	sendMsg(t, ws1, signaling.Message{Type: signaling.TypeHangup, To: "3140002"})
	hangup := recvMsg(t, ws2)
	if hangup.Type != signaling.TypeHangup {
		t.Fatalf("expected hangup, got %s", hangup.Type)
	}
	// The relay calls Tracker.OnCallEnded synchronously before forwarding
	// Hangup to the peer, so by the time ws2 has read it the DB UPDATE is
	// already done.

	// Verify call appears in history via HTTP (authenticated)
	resp, err := authedClient.Get(srv.URL + "/api/status")
	if err != nil {
		t.Fatalf("api/status: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Verify call in tracker history
	recentCalls, err := tracker.RecentForPhones(context.Background(), []string{"3140001", "3140002"}, 10)
	if err != nil {
		t.Fatalf("RecentForPhones: %v", err)
	}
	if len(recentCalls) == 0 {
		t.Fatal("expected at least 1 call in history")
	}
	found := false
	for _, c := range recentCalls {
		if c.Caller == "3140001" && c.Callee == "3140002" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("call 3140001→3140002 not found in history: %+v", recentCalls)
	}
}

func TestE2ECallToOfflinePhone(t *testing.T) {
	srv, _, _, _, _, hub := setupTestServer(t)

	ws1 := dialWS(t, srv)
	sendMsg(t, ws1, signaling.Message{Type: signaling.TypeRegister, Number: "3140001", HardwareID: "e2e-offline-a"})
	waitForRegister(t, hub, "3140001")

	// Call a phone that's not connected
	sendMsg(t, ws1, signaling.Message{Type: signaling.TypeCall, To: "3140099"})

	errMsg := recvMsg(t, ws1)
	if errMsg.Type != signaling.TypeError {
		t.Fatalf("expected error, got %s", errMsg.Type)
	}
}

func TestE2ENoRegisterRejected(t *testing.T) {
	srv, _, _, _, _, _ := setupTestServer(t)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send a non-register message first
	sendMsg(t, conn, signaling.Message{Type: signaling.TypeCall, To: "3140002"})

	// Should receive error and connection close
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		// Connection closed is also acceptable
		return
	}
	msg, _ := signaling.ParseMessage(data)
	if msg != nil && msg.Type != signaling.TypeError {
		t.Fatalf("expected error message, got: %+v", msg)
	}
}

func TestE2EWebUIWithData(t *testing.T) {
	srv, database, lineStore, _, authStore, _ := setupTestServer(t)

	hhID := seedE2EHousehold(t, database, authStore)
	if _, err := lineStore.Add(context.Background(), "3140001", "Kitchen", hhID); err != nil {
		t.Fatalf("add kitchen line: %v", err)
	}
	if _, err := lineStore.Add(context.Background(), "3140002", "Bedroom", hhID); err != nil {
		t.Fatalf("add bedroom line: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number IN ('3140001', '3140002')")
	})

	// Create a session cookie for authenticated requests
	cookie := addSessionCookie(t, authStore)
	jar := &testCookieJar{cookie: cookie, url: srv.URL}
	client := &http.Client{Jar: jar}

	// Dashboard should show phone count
	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Overview should list the registered lines
	resp2, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	var bodyBuf strings.Builder
	_, _ = io.Copy(&bodyBuf, resp2.Body)
	body := bodyBuf.String()
	if !strings.Contains(body, "Kitchen") || !strings.Contains(body, "Bedroom") {
		t.Fatalf("overview missing registered lines")
	}
}
