//go:build integration

package web

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/justinlindh/digits/server/internal/dbutil"
	"github.com/justinlindh/digits/server/internal/signaling"
	"github.com/justinlindh/digits/server/internal/updates"
)

func TestWithCurrentConnPropagatesNestedRenumberFenceContext(t *testing.T) {
	h, database, _ := setupHandler(t)
	h.hub.SetLineResolver(nil, func(int64) (string, error) { return "3140042", nil })
	conn := &signaling.Conn{LineID: 42, HardwareID: "nested-context", Send: make(chan []byte, 1)}
	if err := h.hub.Register("3140042", conn); err != nil {
		t.Fatal(err)
	}

	outerEntered := make(chan struct{})
	allowNested := make(chan struct{})
	outerDone := make(chan error, 1)
	go func() {
		outerDone <- h.withCurrentConn(context.Background(), conn, func(fencedCtx context.Context) error {
			close(outerEntered)
			<-allowNested
			return h.lineStore.WithRenumberReadFence(fencedCtx, func(context.Context) error { return nil })
		})
	}()
	<-outerEntered
	writerStarted := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		close(writerStarted)
		writerDone <- dbutil.WithRenumberWriteFence(context.Background(), database.DB, func(*sql.Tx) error { return nil })
	}()
	<-writerStarted
	time.Sleep(50 * time.Millisecond)
	close(allowNested)
	select {
	case err := <-outerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("production withCurrentConn lost nested fence context behind queued writer")
	}
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
}

func TestOwnerBuildersExcludeDelayedPresenceFromReusedNumber(t *testing.T) {
	h, database, authStore := setupHandler(t)
	_, hh := setupAuthedHousehold(t, h, database, authStore)
	const number = "3140068"
	current, err := h.lineStore.Add(context.Background(), number, "Replacement", hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM lines WHERE id = $1", current.ID)
	})

	stale := &signaling.Conn{
		LineID:       current.ID + 1000,
		HardwareID:   "delayed-owner-builder",
		ConnectionID: "old-builder",
		LastSeen:     time.Unix(1234, 0),
		PiVersion:    "old-owner-version",
		Send:         make(chan []byte, 1),
	}
	if err := h.hub.Register(number, stale); err != nil {
		t.Fatal(err)
	}
	h.hub.SetVoicemailUnheardForLine(number, stale.LineID, stale.HardwareID, 11)
	// Model the ownership lookup racing behind the household line snapshot.
	// Number-only helpers would accept the stale identity here.
	h.hub.SetLineResolver(func(string) (int64, error) { return stale.LineID, nil }, nil)

	rows, _ := h.buildLineRows(httptest.NewRequest("GET", "/phones", nil), hh)
	var row *lineRow
	for i := range rows {
		if rows[i].Line.ID == current.ID {
			row = &rows[i]
			break
		}
	}
	if row == nil {
		t.Fatal("replacement line missing from owner rows")
	}
	if row.Online || row.OnlineDeviceCount != 0 || row.DeviceInfo != nil || row.VoicemailUnheard != 0 {
		t.Fatalf("owner row exposed delayed old-owner presence: %+v", *row)
	}

	detail := h.buildOperatorData(current, hh, true, "", nil, h.hub.AllDeviceInfoForLine(number, current.ID))
	if detail.Online || detail.DeviceInfo != nil || detail.LastSeenAt != nil {
		t.Fatalf("owner detail exposed delayed old-owner presence: %+v", detail)
	}
}

func TestOwnerSettingsAndFallbackCommandRejectReusedNumber(t *testing.T) {
	h, database, authStore := setupHandler(t)
	_, firstHousehold := setupAuthedHousehold(t, h, database, authStore)
	if _, err := database.DB.Exec(`UPDATE renumber_control SET enabled = TRUE, updated_at = NOW() WHERE singleton`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec(`UPDATE renumber_control SET enabled = FALSE, updated_at = NOW() WHERE singleton`)
	})
	var secondHouseholdID string
	if err := database.DB.QueryRow(`INSERT INTO households (name) VALUES ($1) RETURNING id`, "Replacement Household").Scan(&secondHouseholdID); err != nil {
		t.Fatal(err)
	}
	oldNumber := nextPhone()
	oldLine, err := h.lineStore.Add(context.Background(), oldNumber, "Original", firstHousehold.ID)
	if err != nil {
		t.Fatal(err)
	}
	staleSnapshot := *oldLine
	if _, err := database.DB.Exec(`UPDATE lines SET number = $1 WHERE id = $2`, nextPhone(), oldLine.ID); err != nil {
		t.Fatal(err)
	}
	replacement, err := h.lineStore.Add(context.Background(), oldNumber, "Replacement", secondHouseholdID)
	if err != nil {
		t.Fatal(err)
	}
	replacementConn := &signaling.Conn{LineID: replacement.ID, HardwareID: "replacement-device", Send: make(chan []byte, 2)}
	if err := h.hub.Register(oldNumber, replacementConn); err != nil {
		t.Fatal(err)
	}

	next := staleSnapshot.Settings
	next.SilentMode = !next.SilentMode
	settingsResponse := httptest.NewRecorder()
	settingsRequest := httptest.NewRequest(http.MethodPost, "/phones/"+oldNumber+"/silent", nil)
	if !h.applyLineSettings(settingsResponse, settingsRequest, &staleSnapshot, next) {
		t.Fatalf("applyLineSettings failed: %d %s", settingsResponse.Code, settingsResponse.Body.String())
	}
	expectNoSignalingMessage(t, replacementConn)

	commandResponse := httptest.NewRecorder()
	commandRequest := httptest.NewRequest(http.MethodPost, "/phones/"+oldNumber+"/restart", nil)
	h.sendDeviceCommandAndRespond(commandResponse, commandRequest, &staleSnapshot, "", &signaling.Message{Type: signaling.TypeRestart}, "restart command")
	expectNoSignalingMessage(t, replacementConn)
}

func TestOwnerMetadataReadersRetainKnownLineIdentity(t *testing.T) {
	h, database, authStore := setupHandler(t)
	cookie, hh := setupAuthedHousehold(t, h, database, authStore)
	number := nextPhone()
	current, err := h.lineStore.Add(context.Background(), number, "Current", hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale := &signaling.Conn{
		LineID:     current.ID + 1000,
		HardwareID: "replacement-metadata",
		PiVersion:  "1.0.0",
		DevMode:    true,
		Send:       make(chan []byte, 1),
	}
	if err := h.hub.Register(number, stale); err != nil {
		t.Fatal(err)
	}
	h.hub.SetLineResolver(func(string) (int64, error) { return stale.LineID, nil }, nil)
	idx := &updates.ReleaseIndex{Pi: updates.ComponentIndex{
		Latest: "2.0.0",
		Releases: map[string]*updates.Release{
			"2.0.0": {Version: "2.0.0"},
		},
	}}
	h.releases = updates.NewGitHubReleasesWithIndex(idx)

	if h.hasPhoneUpdates(context.Background(), hh.ID) {
		t.Fatal("replacement owner's version set update availability")
	}

	req := httptest.NewRequest(http.MethodGet, "/phones/"+number+"/dev-mode-status", nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("dev-mode status = %d: %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "{\"enabled\":false,\"ssh_user\":\"dev\"}\n" {
		t.Fatalf("dev-mode status exposed replacement metadata: %s", got)
	}
}

func expectNoSignalingMessage(t *testing.T, conn *signaling.Conn) {
	t.Helper()
	select {
	case data := <-conn.Send:
		t.Fatalf("replacement owner received message: %q", data)
	default:
	}
}
