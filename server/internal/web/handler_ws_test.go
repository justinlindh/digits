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

	code, err := pairingStore.GenerateCode(context.Background(), hardwareID)
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}

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
	if !strings.Contains(err.Error(), "i/o timeout") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
}

// A paired device whose stored number no longer matches its bound line (after
// a move, join, or renumber) must not be rejected: rejecting strands it
// offline in a reconnect loop. The bound number is authoritative, so the
// server reconciles and registers the device under its real line. It then
// self-heals the drift by pushing the corrected number back so the device
// persists it and stops re-claiming the stale one on the next register.
func TestWSRegister_PairedDevice_NumberMismatch_UsesBoundNumber(t *testing.T) {
	h, database, authStore := setupHandler(t)
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	hwID, number, token := setupPairedDevice(t, database, h.pairingStore, h.householdStore, authStore)

	ws := dialWS(t, srv)
	sendMsg(t, ws, signaling.Message{
		Type:        signaling.TypeRegister,
		Number:      "1001", // stale / wrong number the device still thinks it has
		HardwareID:  hwID,
		DeviceToken: token,
	})

	// The connection is accepted (not rejected with an error), and the server
	// immediately pushes the corrected line number so the device self-heals.
	msg := recvMsg(t, ws)
	if msg.Type != signaling.TypeLineRenumber {
		t.Fatalf("expected %q message, got %q (error=%q)", signaling.TypeLineRenumber, msg.Type, msg.Error)
	}
	if msg.Number != number {
		t.Errorf("renumber carried %q, want bound number %q", msg.Number, number)
	}

	// Nothing else follows: the renumber is sent exactly once, not on a loop.
	_ = ws.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if _, _, err := ws.ReadMessage(); err == nil {
		t.Fatal("expected no further message after the single renumber, but got one")
	} else if !strings.Contains(err.Error(), "i/o timeout") {
		t.Fatalf("expected timeout after renumber, got: %v", err)
	}

	// The device is online under its BOUND number, never under the stale one.
	if !h.hub.IsOnline(number) {
		t.Errorf("device should be online under its bound number %q", number)
	}
	if h.hub.IsOnline("1001") {
		t.Error("device must not be registered under the stale claimed number 1001")
	}
}

// A paired device that registers with the correct number must NOT receive a
// renumber message: the self-heal push fires only on an actual reconcile, so
// the common case stays silent.
func TestWSRegister_PairedDevice_CorrectNumber_NoRenumber(t *testing.T) {
	h, database, authStore := setupHandler(t)
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	hwID, number, token := setupPairedDevice(t, database, h.pairingStore, h.householdStore, authStore)

	ws := dialWS(t, srv)
	sendMsg(t, ws, signaling.Message{
		Type:        signaling.TypeRegister,
		Number:      number, // correct number: no drift to heal
		HardwareID:  hwID,
		DeviceToken: token,
	})

	_ = ws.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if _, _, err := ws.ReadMessage(); err == nil {
		t.Fatal("expected no message for a correctly-numbered register, but got one")
	} else if !strings.Contains(err.Error(), "i/o timeout") {
		t.Fatalf("expected timeout (no renumber), got: %v", err)
	}
}

// An unknown device (no paired row) that registers must be issued a pairing
// code and parked under its unpaired hardware-ID key, never under the line
// number it claimed. This is the first-contact handshake every new device goes
// through; parking it under the unpaired prefix lets it receive the eventual
// TypePaired via SendToHardware without displacing a real line's connection.
func TestWSRegister_UnpairedDevice_GetsPairingCode(t *testing.T) {
	h, database, _ := setupHandler(t)
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	hwID := fmt.Sprintf("test-hw-unpaired-%d", time.Now().UnixNano())
	number := fmt.Sprintf("88%05d", time.Now().UnixNano()%100000)
	t.Cleanup(func() {
		_, _ = database.DB.Exec("DELETE FROM devices WHERE hardware_id = $1", hwID)
	})

	ws := dialWS(t, srv)
	sendMsg(t, ws, signaling.Message{
		Type:       signaling.TypeRegister,
		Number:     number,
		HardwareID: hwID,
	})

	msg := recvMsg(t, ws)
	if msg.Type != signaling.TypePairingCode {
		t.Fatalf("expected %q, got %q (error=%q)", signaling.TypePairingCode, msg.Type, msg.Error)
	}
	if msg.PairingCode == "" {
		t.Error("pairing code message carried an empty code")
	}
	if msg.PairingCodeTTL != int(pairing.CodeTTL.Seconds()) {
		t.Errorf("PairingCodeTTL = %d, want %d", msg.PairingCodeTTL, int(pairing.CodeTTL.Seconds()))
	}

	// The device is parked under the unpaired prefix, not the claimed number.
	waitForRegister(t, h.hub, signaling.UnpairedPrefix+hwID)
	if h.hub.IsOnline(number) {
		t.Errorf("unpaired device must not be online under claimed number %q", number)
	}
}

// Repair always targets the authenticated connection's device. The payload's
// hardware ID is untrusted and optional because older clients may omit it.
func TestWSRepairUsesAuthenticatedHardwareID(t *testing.T) {
	tests := []struct {
		name             string
		repairHardwareID func(authenticatedID, otherID string) string
	}{
		{
			name: "foreign payload hardware ID",
			repairHardwareID: func(_, otherID string) string {
				return otherID
			},
		},
		{
			name: "omitted payload hardware ID",
			repairHardwareID: func(_, _ string) string {
				return ""
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, database, authStore := setupHandler(t)
			srv := httptest.NewServer(h.Router())
			defer srv.Close()

			authenticatedID, number, authenticatedToken := setupPairedDevice(t, database, h.pairingStore, h.householdStore, authStore)
			otherID, _, otherToken := setupPairedDevice(t, database, h.pairingStore, h.householdStore, authStore)

			ws := dialWS(t, srv)
			sendMsg(t, ws, signaling.Message{
				Type:        signaling.TypeRegister,
				Number:      number,
				HardwareID:  authenticatedID,
				DeviceToken: authenticatedToken,
			})
			waitForRegister(t, h.hub, number)

			sendMsg(t, ws, signaling.Message{
				Type:       signaling.TypeRepair,
				HardwareID: tt.repairHardwareID(authenticatedID, otherID),
			})

			deadline := time.Now().Add(2 * time.Second)
			for {
				authenticatedPaired, _, err := h.deviceStore.AuthStatus(context.Background(), authenticatedID, authenticatedToken)
				if err != nil {
					t.Fatalf("authenticated device status after repair: %v", err)
				}
				otherPaired, _, err := h.deviceStore.AuthStatus(context.Background(), otherID, otherToken)
				if err != nil {
					t.Fatalf("other device status after repair: %v", err)
				}
				if !authenticatedPaired || !otherPaired || time.Now().After(deadline) {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}

			authenticatedPaired, authenticatedValid, err := h.deviceStore.AuthStatus(context.Background(), authenticatedID, authenticatedToken)
			if err != nil {
				t.Fatalf("authenticated device final status: %v", err)
			}
			if authenticatedPaired || authenticatedValid {
				t.Errorf("authenticated device remained paired after repair (paired=%v valid=%v)", authenticatedPaired, authenticatedValid)
			}

			otherPaired, otherValid, err := h.deviceStore.AuthStatus(context.Background(), otherID, otherToken)
			if err != nil {
				t.Fatalf("other device final status: %v", err)
			}
			if !otherPaired || !otherValid {
				t.Errorf("repair changed other device (paired=%v valid=%v)", otherPaired, otherValid)
			}
		})
	}
}
