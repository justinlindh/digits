//go:build integration

package web

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/justinlindh/digits/server/internal/auth"
	"github.com/justinlindh/digits/server/internal/db"
	"github.com/justinlindh/digits/server/internal/household"
	"github.com/justinlindh/digits/server/internal/pairing"
	"github.com/justinlindh/digits/server/internal/signaling"
)

// setupPairedDevice creates a paired device row in the DB and returns the
// hardware ID, line number, and device token. Registers cleanup of all rows.
func setupPairedDevice(t *testing.T, database *db.Database, pairingStore *pairing.Store, householdStore *household.Store, authStore *auth.Store) (hardwareID, number, token string) {
	t.Helper()

	hardwareID = fmt.Sprintf("test-hw-%d", time.Now().UnixNano())
	number = fmt.Sprintf("99%05d", time.Now().UnixNano()%100000)

	// Create a household for the line
	user, err := authStore.GetUserByEmail(context.Background(), "test@example.com")
	if err != nil {
		user, err = authStore.CreateUser(context.Background(), "test@example.com", "Test User", nil)
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
	}
	markUserOnboarded(t, authStore, user.ID)
	hh, err := householdStore.Create(context.Background(), "Test Household", user.ID)
	if err != nil {
		t.Fatalf("create household: %v", err)
	}

	// Generate pairing code (creates the device row)
	code, err := pairingStore.GenerateCode(context.Background(), hardwareID)
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}

	// Claim the device (pairs it, sets hashed token)
	token, _, err = pairingStore.ClaimDevice(context.Background(), code, number, "Test Phone", "Test Phone", hh.ID)
	if err != nil {
		t.Fatalf("claim device: %v", err)
	}

	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM devices WHERE hardware_id = $1", hardwareID)
		_, _ = database.DB.Exec("DELETE FROM lines WHERE number = $1", number)
		_, _ = database.DB.Exec("DELETE FROM household_members WHERE household_id = $1", hh.ID)
		_, _ = database.DB.Exec("DELETE FROM households WHERE id = $1", hh.ID)
	})

	return hardwareID, number, token
}

func TestWSRegister_MissingHardwareID(t *testing.T) {
	h, _, _ := setupHandler(t)
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	ws := dialWS(t, srv)
	sendMsg(t, ws, signaling.Message{
		Type:   signaling.TypeRegister,
		Number: "1001",
	})

	msg := recvMsg(t, ws)
	if msg.Type != signaling.TypeError {
		t.Fatalf("expected error message, got %q", msg.Type)
	}
	if msg.Error != "hardware_id required" {
		t.Errorf("expected 'hardware_id required', got %q", msg.Error)
	}
}

func TestWSRegister_PairedDevice_MissingToken(t *testing.T) {
	h, database, authStore := setupHandler(t)
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	hwID, _, _ := setupPairedDevice(t, database, h.pairingStore, h.householdStore, authStore)

	ws := dialWS(t, srv)
	sendMsg(t, ws, signaling.Message{
		Type:       signaling.TypeRegister,
		Number:     "1001",
		HardwareID: hwID,
	})

	msg := recvMsg(t, ws)
	if msg.Type != signaling.TypeError {
		t.Fatalf("expected error message, got %q", msg.Type)
	}
	if msg.Error != "device_token required" {
		t.Errorf("expected 'device_token required', got %q", msg.Error)
	}
}

func TestWSRegister_PairedDevice_WrongToken(t *testing.T) {
	h, database, authStore := setupHandler(t)
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	hwID, _, _ := setupPairedDevice(t, database, h.pairingStore, h.householdStore, authStore)

	ws := dialWS(t, srv)
	sendMsg(t, ws, signaling.Message{
		Type:        signaling.TypeRegister,
		Number:      "1001",
		HardwareID:  hwID,
		DeviceToken: "wrong-token-value",
	})

	msg := recvMsg(t, ws)
	if msg.Type != signaling.TypeError {
		t.Fatalf("expected error message, got %q", msg.Type)
	}
	if msg.Error != "invalid device_token" {
		t.Errorf("expected 'invalid device_token', got %q", msg.Error)
	}
}

func TestWSRegister_PairedDevice_CorrectToken(t *testing.T) {
	h, database, authStore := setupHandler(t)
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	hwID, number, token := setupPairedDevice(t, database, h.pairingStore, h.householdStore, authStore)

	ws := dialWS(t, srv)
	sendMsg(t, ws, signaling.Message{
		Type:        signaling.TypeRegister,
		Number:      number,
		HardwareID:  hwID,
		DeviceToken: token,
	})

	// The connection should stay open. If there was an auth error, we'd get
	// an error message. Try reading with a short deadline; no error message
	// means auth succeeded.
	_ = ws.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, err := ws.ReadMessage()
	if err == nil {
		t.Fatal("expected no message (timeout), but got one")
	}
	// A timeout (deadline exceeded) means no error was sent, which is success.
	if !strings.Contains(err.Error(), "i/o timeout") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
}
