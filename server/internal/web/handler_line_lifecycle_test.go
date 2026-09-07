//go:build integration

package web

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/signaling"
)

// postNumberChange submits the number-change form for oldNumber as the
// authenticated household admin and returns the recorded response.
func postNumberChange(t *testing.T, h *Handler, cookie *http.Cookie, oldNumber, newNumber string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"number": {newNumber}}
	req := httptest.NewRequest(http.MethodPost, "/phones/"+oldNumber+"/number", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	return w
}

// registerPaired registers a paired device on a fresh WebSocket and waits
// until the hub sees it under number.
func registerPaired(t *testing.T, srv *httptest.Server, hub *signaling.Hub, number, hwID, token string) *websocket.Conn {
	t.Helper()
	ws := dialWS(t, srv)
	sendMsg(t, ws, signaling.Message{
		Type:        signaling.TypeRegister,
		Number:      number,
		HardwareID:  hwID,
		DeviceToken: token,
	})
	waitForRegister(t, hub, number)
	return ws
}

// Changing a line's number while its phone is connected closes the phone's
// socket and lets it come back under the new number.
func TestChangePhoneNumber_ConnectedDeviceReregisters(t *testing.T) {
	h, database, authStore := setupHandler(t)
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	cookie := addSessionCookie(t, authStore)
	hwID, oldNumber, token := setupPairedDevice(t, database, h.pairingStore, h.householdStore, authStore)
	newNumber := nextPhone()
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number = $1", newNumber)
	})

	ws := registerPaired(t, srv, h.hub, oldNumber, hwID, token)

	w := postNumberChange(t, h, cookie, oldNumber, newNumber)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/phones/"+newNumber {
		t.Fatalf("expected redirect to /phones/%s, got %s", newNumber, loc)
	}

	// The device is told its new number so it persists it before reconnecting,
	// then the server closes the old-identity socket.
	msg := recvMsg(t, ws)
	if msg.Type != signaling.TypeLineRenumber || msg.Number != newNumber {
		t.Fatalf("expected %s{%s}, got %s{%s}", signaling.TypeLineRenumber, newNumber, msg.Type, msg.Number)
	}
	drainUntilClosed(t, ws)

	// No ghost under either number, and the hardware is offline until it
	// registers again.
	waitForCondition(t, "old socket unregistered", func() bool {
		return h.hub.ConnectionCount(oldNumber) == 0 && h.hub.ConnectionCount(newNumber) == 0
	})
	if h.hub.IsHardwareOnline(hwID) {
		t.Fatal("hardware must be offline after its old-identity socket closed")
	}

	// The device comes back with the new number and the same token, is
	// accepted without a reconcile, and is reachable under the new number.
	ws2 := registerPaired(t, srv, h.hub, newNumber, hwID, token)
	sendMarker(t, h.hub, newNumber)
	readUntil(t, ws2, "re-registered device", signaling.TypeRingTest, signaling.TypeLineRenumber)
	if !h.hub.IsOnline(newNumber) {
		t.Fatal("device should be online under the new number")
	}
	if h.hub.IsOnline(oldNumber) {
		t.Fatal("nothing may remain registered under the old number")
	}
	if h.hub.ConnectionCount(newNumber) != 1 {
		t.Fatalf("expected exactly one connection under the new number, got %d", h.hub.ConnectionCount(newNumber))
	}
}

// A number change is refused while any phone on the line is in a call: the
// change closes the line's sockets, which would drop the call.
func TestChangePhoneNumber_BusyLineRejected(t *testing.T) {
	h, database, authStore := setupHandler(t)
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	cookie := addSessionCookie(t, authStore)
	hwID, oldNumber, token := setupPairedDevice(t, database, h.pairingStore, h.householdStore, authStore)
	newNumber := nextPhone()
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number = $1", newNumber)
		_, _ = database.DB.Exec("DELETE FROM calls WHERE caller = $1 OR callee = $1", oldNumber)
	})

	ws := registerPaired(t, srv, h.hub, oldNumber, hwID, token)

	if _, err := h.tracker.OnCallInitiated(context.Background(), oldNumber, "9999999"); err != nil {
		t.Fatalf("seed active call: %v", err)
	}
	t.Cleanup(func() { h.tracker.ClearByNumber(context.Background(), oldNumber) })

	w := postNumberChange(t, h, cookie, oldNumber, newNumber)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "number_error=") {
		t.Fatalf("expected rejection redirect, got %s", loc)
	}

	// Nothing changed: the socket stays open (a marker sent now arrives with
	// no farewell ahead of it) and the line keeps its number.
	sendMarker(t, h.hub, oldNumber)
	readUntil(t, ws, "busy line", signaling.TypeRingTest, signaling.TypeLineRenumber)
	if !h.hub.IsOnline(oldNumber) {
		t.Fatal("busy line must remain online under its number")
	}
	if _, err := h.lineStore.GetByNumber(context.Background(), oldNumber); err != nil {
		t.Fatalf("line must keep its old number: %v", err)
	}
}

// pairDeviceInNewHousehold creates a fresh user and household and pairs a
// new device on it under number. Used to model another household claiming a
// number that an earlier line gave up.
func pairDeviceInNewHousehold(t *testing.T, h *Handler, database *db.Database, authStore *auth.Store, label, number string) (hardwareID, token string) {
	t.Helper()
	ctx := context.Background()
	hardwareID = fmt.Sprintf("%s-hw-%d", label, time.Now().UnixNano())
	email := fmt.Sprintf("%s-%d@example.com", label, time.Now().UnixNano())
	user, err := authStore.CreateUser(ctx, email, label, nil)
	if err != nil {
		t.Fatalf("%s: create user: %v", label, err)
	}
	markUserOnboarded(t, authStore, user.ID)
	hh, err := h.householdStore.Create(ctx, label+" household", user.ID)
	if err != nil {
		t.Fatalf("%s: create household: %v", label, err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE household_id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM households WHERE id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM users WHERE id = $1", user.ID)
	})
	token = pairDevice(t, database, h.pairingStore, hh.ID, hardwareID, number, label+" phone")
	return hardwareID, token
}

// sendMarker sends a frame to number that the tests use purely as an
// ordering marker: anything the hub queued for that socket earlier must
// arrive before it.
func sendMarker(t *testing.T, hub *signaling.Hub, number string) {
	t.Helper()
	if err := hub.SendTo(number, &signaling.Message{Type: signaling.TypeRingTest}); err != nil {
		t.Fatalf("marker %s: %v", number, err)
	}
}

// readUntil reads frames from ws until one of type want arrives, failing if
// a frame of type forbidden shows up first. Frames reach a socket in the
// order the hub queued them, so a message sent before the marker that was
// meant for someone else would have to appear before the marker: this is a
// deterministic "nothing was delivered" check without relying on a read
// timeout (which gorilla makes sticky on the connection).
func readUntil(t *testing.T, ws *websocket.Conn, who, want, forbidden string) {
	t.Helper()
	for {
		msg := recvMsg(t, ws) // recvMsg bounds each read
		if msg.Type == forbidden {
			t.Fatalf("%s received a %s that was not addressed to it", who, forbidden)
		}
		if msg.Type == want {
			return
		}
	}
}

// drainUntilClosed reads ws until the server closes it, as digitsd does, so
// the client answers the server's close frame and the read loop unwinds. It
// returns how many frames arrived before the close.
func drainUntilClosed(t *testing.T, ws *websocket.Conn) int {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for n := 0; ; n++ {
		_, _, err := ws.ReadMessage()
		if err == nil {
			continue
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			t.Fatal("socket still open, expected the server to close it")
		}
		return n
	}
}

// Once a line gives up its number and another household claims it, nothing
// addressed to that number may reach the previous owner, and nothing addressed
// to the previous owner's new number may reach the new claimant. This also
// covers the previous owner's phone coming back with the stale number in its
// config: registration binds it to its real line, never to the reused one.
func TestChangePhoneNumber_ReusedNumberNeverReachesOldOwner(t *testing.T) {
	h, database, authStore := setupHandler(t)
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	cookie := addSessionCookie(t, authStore)
	hwA, oldNumber, tokenA := setupPairedDevice(t, database, h.pairingStore, h.householdStore, authStore)
	newNumber := nextPhone()
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number = $1", newNumber)
	})

	wsA := registerPaired(t, srv, h.hub, oldNumber, hwA, tokenA)
	w := postNumberChange(t, h, cookie, oldNumber, newNumber)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/phones/"+newNumber {
		t.Fatalf("renumber failed: %d %s", w.Code, w.Header().Get("Location"))
	}
	if msg := recvMsg(t, wsA); msg.Type != signaling.TypeLineRenumber {
		t.Fatalf("expected line_renumber, got %s", msg.Type)
	}
	drainUntilClosed(t, wsA)
	waitForCondition(t, "old-identity socket unregistered", func() bool {
		return h.hub.ConnectionCount(oldNumber) == 0
	})
	wsA2 := registerPaired(t, srv, h.hub, newNumber, hwA, tokenA)

	// Household B claims the number A just gave up and connects a phone on it.
	hwB, tokenB := pairDeviceInNewHousehold(t, h, database, authStore, "hhb", oldNumber)
	wsB := registerPaired(t, srv, h.hub, oldNumber, hwB, tokenB)

	if got := h.hub.Get(oldNumber); got == nil || got.HardwareID != hwB {
		t.Fatalf("old number must resolve to B's hardware, got %+v", got)
	}
	if n := h.hub.ConnectionCount(oldNumber); n != 1 {
		t.Fatalf("old number must have exactly B's connection, got %d", n)
	}
	if got := h.hub.Get(newNumber); got == nil || got.HardwareID != hwA {
		t.Fatalf("new number must resolve to A's hardware, got %+v", got)
	}

	// A ring to the reused number reaches B only, and a ring to A's new
	// number reaches A only. Each socket then gets a marker addressed to it;
	// the other side's ring must not precede the marker.
	ringTo := func(number string) {
		if err := h.hub.SendTo(number, &signaling.Message{Type: signaling.TypeRing, From: "0000000"}); err != nil {
			t.Fatalf("ring %s: %v", number, err)
		}
	}
	ringTo(oldNumber)
	sendMarker(t, h.hub, newNumber)
	readUntil(t, wsB, "B (claimant of the reused number)", signaling.TypeRing, signaling.TypeRingTest)
	readUntil(t, wsA2, "A (previous owner of the reused number)", signaling.TypeRingTest, signaling.TypeRing)

	ringTo(newNumber)
	sendMarker(t, h.hub, oldNumber)
	readUntil(t, wsA2, "A", signaling.TypeRing, signaling.TypeRingTest)
	readUntil(t, wsB, "B", signaling.TypeRingTest, signaling.TypeRing)

	// A's phone comes back with the stale number still in its config. It is
	// bound to its real line, told the right number, and never lands in B's
	// bucket.
	wsStale := dialWS(t, srv)
	sendMsg(t, wsStale, signaling.Message{
		Type:        signaling.TypeRegister,
		Number:      oldNumber,
		HardwareID:  hwA,
		DeviceToken: tokenA,
	})
	if msg := recvMsg(t, wsStale); msg.Type != signaling.TypeLineRenumber || msg.Number != newNumber {
		t.Fatalf("stale register must be reconciled to %s, got %s{%s}", newNumber, msg.Type, msg.Number)
	}
	// The same hardware re-registering replaces its earlier slot under the
	// new number (the replaced socket is closed), so the stale socket is now
	// A's live one and the line still has one connection.
	drainUntilClosed(t, wsA2)
	if n := h.hub.ConnectionCount(newNumber); n != 1 {
		t.Fatalf("new number must hold exactly A's replacement connection, got %d", n)
	}
	sendMarker(t, h.hub, newNumber)
	readUntil(t, wsStale, "A (stale-number register)", signaling.TypeRingTest, signaling.TypeRing)
	if n := h.hub.ConnectionCount(oldNumber); n != 1 {
		t.Fatalf("B's bucket must be untouched by A's stale register, got %d connections", n)
	}
	if got := h.hub.Get(oldNumber); got == nil || got.HardwareID != hwB {
		t.Fatalf("old number must still resolve to B's hardware, got %+v", got)
	}
}

// postForm submits a phone-management form as the authenticated household
// admin and returns the recorded response.
func postForm(t *testing.T, h *Handler, cookie *http.Cookie, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	return w
}

// seedActiveCall marks number as in a call for the rest of the test.
func seedActiveCall(t *testing.T, h *Handler, number string) {
	t.Helper()
	if _, err := h.tracker.OnCallInitiated(context.Background(), number, "9999999"); err != nil {
		t.Fatalf("seed active call: %v", err)
	}
	t.Cleanup(func() { h.tracker.ClearByNumber(context.Background(), number) })
}

// Deleting a line closes its phones' sockets everywhere. Each phone comes
// back unpaired and is offered a pairing code, and once another household
// claims the number, nothing addressed to it reaches the previous phone.
func TestDeletePhone_ConnectedDeviceUnpairs(t *testing.T) {
	h, database, authStore := setupHandler(t)
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	cookie := addSessionCookie(t, authStore)
	hwA, number, tokenA := setupPairedDevice(t, database, h.pairingStore, h.householdStore, authStore)
	wsA := registerPaired(t, srv, h.hub, number, hwA, tokenA)

	w := postForm(t, h, cookie, "/phones/"+number+"/delete", nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("delete failed: %d %s", w.Code, w.Header().Get("Location"))
	}

	// Closed with nothing said first: the phone learns what it is now when
	// it registers again.
	if n := drainUntilClosed(t, wsA); n != 0 {
		t.Fatalf("expected a bare close, got %d frames first", n)
	}
	waitForCondition(t, "deleted line's socket unregistered", func() bool {
		return h.hub.ConnectionCount(number) == 0
	})

	// The phone reconnects with its old config and is offered a pairing code
	// instead of landing on the number.
	wsA2 := dialWS(t, srv)
	sendMsg(t, wsA2, signaling.Message{
		Type:        signaling.TypeRegister,
		Number:      number,
		HardwareID:  hwA,
		DeviceToken: tokenA,
	})
	if msg := recvMsg(t, wsA2); msg.Type != signaling.TypePairingCode || msg.PairingCode == "" {
		t.Fatalf("expected pairing_code after line deletion, got %s", msg.Type)
	}
	waitForCondition(t, "unpaired register", func() bool { return h.hub.IsHardwareOnline(hwA) })
	if h.hub.ConnectionCount(number) != 0 {
		t.Fatal("unpaired phone must not be registered under the deleted number")
	}

	// Household B claims the freed number. A ring to it reaches B only; A's
	// unpaired socket sees only the marker addressed to its hardware.
	hwB, tokenB := pairDeviceInNewHousehold(t, h, database, authStore, "hhb", number)
	wsB := registerPaired(t, srv, h.hub, number, hwB, tokenB)
	if got := h.hub.Get(number); got == nil || got.HardwareID != hwB || h.hub.ConnectionCount(number) != 1 {
		t.Fatalf("number must resolve to B's hardware only, got %+v", got)
	}
	if err := h.hub.SendTo(number, &signaling.Message{Type: signaling.TypeRing, From: "0000000"}); err != nil {
		t.Fatalf("ring: %v", err)
	}
	if err := h.hub.SendToHardware(hwA, &signaling.Message{Type: signaling.TypeRingTest}); err != nil {
		t.Fatalf("marker to A: %v", err)
	}
	readUntil(t, wsB, "B (claimant of the freed number)", signaling.TypeRing, signaling.TypeRingTest)
	readUntil(t, wsA2, "A (previous owner, now unpaired)", signaling.TypeRingTest, signaling.TypeRing)
}

// Deleting a line is refused while any of its phones is in a call.
func TestDeletePhone_BusyLineRejected(t *testing.T) {
	h, database, authStore := setupHandler(t)
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	cookie := addSessionCookie(t, authStore)
	hwID, number, token := setupPairedDevice(t, database, h.pairingStore, h.householdStore, authStore)
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM calls WHERE caller = $1 OR callee = $1", number)
	})
	ws := registerPaired(t, srv, h.hub, number, hwID, token)
	seedActiveCall(t, h, number)

	if w := postForm(t, h, cookie, "/phones/"+number+"/delete", nil); w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
	if _, err := h.lineStore.GetByNumber(context.Background(), number); err != nil {
		t.Fatalf("busy line must survive: %v", err)
	}
	sendMarker(t, h.hub, number)
	readUntil(t, ws, "busy line", signaling.TypeRingTest, signaling.TypeLineRenumber)
}

// convertFixture is a line with two connected phones and an empty target
// line in the same household, ready for one phone to be moved.
type convertFixture struct {
	cookie           *http.Cookie
	number, target   string
	hwMoved, tokenMv string
	movedID          int64
	wsMoved, wsStay  *websocket.Conn
	targetID         int64
}

func setupConvertFixture(t *testing.T, h *Handler, srv *httptest.Server, database *db.Database, authStore *auth.Store) convertFixture {
	t.Helper()
	ctx := context.Background()
	f := convertFixture{cookie: addSessionCookie(t, authStore)}
	var tokenStay string
	f.hwMoved, f.number, f.tokenMv = setupPairedDevice(t, database, h.pairingStore, h.householdStore, authStore)
	src, err := h.lineStore.GetByNumber(ctx, f.number)
	if err != nil {
		t.Fatalf("source line: %v", err)
	}

	hwStay := fmt.Sprintf("stay-hw-%d", time.Now().UnixNano())
	code, err := h.pairingStore.GenerateCode(ctx, hwStay)
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	if tokenStay, _, err = h.pairingStore.ClaimDeviceToLine(ctx, code, src.ID, "Sibling", src.HouseholdID); err != nil {
		t.Fatalf("pair sibling: %v", err)
	}
	t.Cleanup(func() { _, _ = database.DB.Exec("DELETE FROM devices WHERE hardware_id = $1", hwStay) })

	f.target = nextPhone()
	tgt, err := h.lineStore.Add(ctx, f.target, "Target", src.HouseholdID)
	if err != nil {
		t.Fatalf("target line: %v", err)
	}
	f.targetID = tgt.ID
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM devices WHERE hardware_id = $1", f.hwMoved)
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number = $1", f.target)
	})

	devices, err := h.deviceStore.ListByLine(ctx, src.ID)
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	for _, d := range devices {
		if d.HardwareID == f.hwMoved {
			f.movedID = d.ID
		}
	}
	if f.movedID == 0 {
		t.Fatal("moved device not found on source line")
	}

	f.wsMoved = registerPaired(t, srv, h.hub, f.number, f.hwMoved, f.tokenMv)
	f.wsStay = registerPaired(t, srv, h.hub, f.number, hwStay, tokenStay)
	waitForCondition(t, "both phones registered", func() bool { return h.hub.ConnectionCount(f.number) == 2 })
	return f
}

func (f convertFixture) form() url.Values {
	return url.Values{
		"target_line_id": {strconv.FormatInt(f.targetID, 10)},
		"device_id":      {strconv.FormatInt(f.movedID, 10)},
	}
}

// Moving one connected handset to another line closes only that handset's
// socket, tells it the target number, and leaves its sibling on the source
// line untouched. The handset comes back under the target line.
func TestConvertLineToExtension_ConnectedDeviceMoves(t *testing.T) {
	h, database, authStore := setupHandler(t)
	srv := httptest.NewServer(h.Router())
	defer srv.Close()
	f := setupConvertFixture(t, h, srv, database, authStore)

	w := postForm(t, h, f.cookie, "/phones/"+f.number+"/convert", f.form())
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/phones/"+f.number {
		t.Fatalf("convert failed: %d %s", w.Code, w.Header().Get("Location"))
	}

	if msg := recvMsg(t, f.wsMoved); msg.Type != signaling.TypeLineRenumber || msg.Number != f.target {
		t.Fatalf("moved handset expected %s{%s}, got %s{%s}", signaling.TypeLineRenumber, f.target, msg.Type, msg.Number)
	}
	drainUntilClosed(t, f.wsMoved)
	waitForCondition(t, "moved handset unregistered", func() bool { return h.hub.ConnectionCount(f.number) == 1 })

	// The sibling saw nothing: a marker sent now is the first thing it reads.
	sendMarker(t, h.hub, f.number)
	readUntil(t, f.wsStay, "sibling on the source line", signaling.TypeRingTest, signaling.TypeLineRenumber)

	// The moved handset registers under the target line with its existing
	// token and is reachable there, and only there.
	wsMoved2 := registerPaired(t, srv, h.hub, f.target, f.hwMoved, f.tokenMv)
	sendMarker(t, h.hub, f.target)
	readUntil(t, wsMoved2, "moved handset", signaling.TypeRingTest, signaling.TypeLineRenumber)
	if h.hub.ConnectionCount(f.target) != 1 || h.hub.ConnectionCount(f.number) != 1 {
		t.Fatalf("expected one socket on each line, got target=%d source=%d",
			h.hub.ConnectionCount(f.target), h.hub.ConnectionCount(f.number))
	}
}

// Moving a handset is refused while its line is in a call; the handset stays
// on the source line and its socket stays open.
func TestConvertLineToExtension_BusyLineRejected(t *testing.T) {
	h, database, authStore := setupHandler(t)
	srv := httptest.NewServer(h.Router())
	defer srv.Close()
	f := setupConvertFixture(t, h, srv, database, authStore)
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM calls WHERE caller = $1 OR callee = $1", f.number)
	})
	seedActiveCall(t, h, f.number)

	w := postForm(t, h, f.cookie, "/phones/"+f.number+"/convert", f.form())
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "number_error=") {
		t.Fatalf("expected rejection redirect, got %d %s", w.Code, w.Header().Get("Location"))
	}

	src, err := h.lineStore.GetByNumber(context.Background(), f.number)
	if err != nil {
		t.Fatalf("source line: %v", err)
	}
	devices, err := h.deviceStore.ListByLine(context.Background(), src.ID)
	if err != nil || len(devices) != 2 {
		t.Fatalf("both handsets must stay on the source line, got %d (%v)", len(devices), err)
	}
	sendMarker(t, h.hub, f.number)
	readUntil(t, f.wsMoved, "handset on busy line", signaling.TypeRingTest, signaling.TypeLineRenumber)
}
