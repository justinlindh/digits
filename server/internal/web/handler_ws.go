package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/justinlindh/digits/server/internal/pairing"
	"github.com/justinlindh/digits/server/internal/signaling"
)

const (
	wsRegisterTimeout = 10 * time.Second
	wsPingInterval    = 30 * time.Second
	wsPongTimeout     = 45 * time.Second
	wsWriteTimeout    = 10 * time.Second
	wsSendBuf         = 32
)

// wsReject sends an error message to the WebSocket client and closes the connection.
var errStaleConnectionBinding = errors.New("stale connection binding")

func (h *Handler) withCurrentConn(ctx context.Context, conn *signaling.Conn, fn func(context.Context) error) error {
	if conn.LineID == 0 {
		return fn(ctx)
	}
	return h.lineStore.WithRenumberReadFence(ctx, func(fencedCtx context.Context) error {
		if !h.hub.EnsureConnBindingCurrent(conn) {
			return errStaleConnectionBinding
		}
		return fn(fencedCtx)
	})
}

func wsReject(ws *websocket.Conn, errMsg string) {
	_ = ws.WriteMessage(websocket.TextMessage, mustMarshal(&signaling.Message{
		Type:  signaling.TypeError,
		Error: errMsg,
	}))
	_ = ws.Close()
}

func (h *Handler) handleWS(w http.ResponseWriter, r *http.Request) {
	if h.hub.IsDraining() {
		http.Error(w, "server shutting down", http.StatusServiceUnavailable)
		return
	}

	ws, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.ErrorContext(r.Context(), "websocket upgrade failed", "err", err)
		return
	}

	// Wait for register message
	if err := ws.SetReadDeadline(time.Now().Add(wsRegisterTimeout)); err != nil {
		slog.ErrorContext(r.Context(), "ws set register deadline failed", "err", err)
		_ = ws.Close()
		return
	}
	_, data, err := ws.ReadMessage()
	if err != nil {
		slog.ErrorContext(r.Context(), "websocket no register message", "err", err)
		_ = ws.Close()
		return
	}
	if err := ws.SetReadDeadline(time.Time{}); err != nil {
		slog.ErrorContext(r.Context(), "ws clear register deadline failed", "err", err)
		_ = ws.Close()
		return
	}

	msg, err := signaling.ParseMessage(data)
	if err != nil || msg.Type != signaling.TypeRegister || msg.Number == "" {
		slog.WarnContext(r.Context(), "invalid register message")
		wsReject(ws, "must send register message first")
		return
	}

	// Require hardware ID for all connections
	if msg.HardwareID == "" {
		slog.WarnContext(r.Context(), "ws register without hardware_id", "number", msg.Number)
		wsReject(ws, "hardware_id required")
		return
	}

	// Check pairing and token status. For paired devices, the server-side
	// bound line number is authoritative: a device cannot register as a
	// line it is not paired to.
	isPaired := false
	var boundLineID int64
	// reconciledNumber is set to the bound line number when a paired device
	// registered with a stale number. After the connection is wired up we push
	// it back so the device persists the correction and stops re-claiming the
	// stale number on every reconnect.
	reconciledNumber := ""
	if h.pairingStore != nil {
		paired, tokenValid, err := h.deviceStore.AuthStatus(r.Context(), msg.HardwareID, msg.DeviceToken)
		if err != nil {
			slog.ErrorContext(r.Context(), "device auth check failed", "hardware_id", msg.HardwareID, "err", err)
			wsReject(ws, "internal error")
			return
		}
		if !paired {
			code, err := h.pairingStore.GenerateCode(r.Context(), msg.HardwareID)
			if err != nil {
				slog.ErrorContext(r.Context(), "generate pairing code failed", "hardware_id", msg.HardwareID, "err", err)
			} else {
				_ = ws.WriteMessage(websocket.TextMessage, mustMarshal(&signaling.Message{
					Type:           signaling.TypePairingCode,
					PairingCode:    code,
					PairingCodeTTL: int(pairing.CodeTTL.Seconds()),
				}))
			}
			// Unpaired devices register under their hardware ID (not a line
			// number) so they can receive the TypePaired message via
			// SendToHardware without displacing a real line's connection.
			msg.Number = signaling.UnpairedPrefix + msg.HardwareID
		} else if msg.DeviceToken == "" {
			slog.WarnContext(r.Context(), "ws register without device_token", "hardware_id", msg.HardwareID)
			wsReject(ws, "device_token required")
			return
		} else if !tokenValid {
			slog.WarnContext(r.Context(), "ws invalid device_token", "hardware_id", msg.HardwareID)
			wsReject(ws, "invalid device_token")
			return
		} else {
			isPaired = true
			var boundNumber string
			boundLineID, boundNumber, err = h.deviceStore.BoundLineIdentity(r.Context(), msg.HardwareID)
			if err != nil {
				slog.ErrorContext(r.Context(), "bound line lookup failed", "hardware_id", msg.HardwareID, "err", err)
				wsReject(ws, "internal error")
				return
			}
			if boundNumber == "" {
				slog.WarnContext(r.Context(), "paired device has no bound line", "hardware_id", msg.HardwareID)
				wsReject(ws, "device has no assigned line")
				return
			}
			if boundNumber != msg.Number {
				// The device registered with a stale number: its line was
				// moved, joined to another line, or renumbered, and the device
				// still has the old number in its local config. The bound
				// number is authoritative (the device can only ever land on
				// the line it is actually paired to), so register it on its
				// real line instead of rejecting. Rejecting would strand the
				// device offline in a reconnect loop, unable to receive calls.
				slog.InfoContext(r.Context(), "ws register reconciled to bound line",
					"hardware_id", msg.HardwareID,
					"claimed", msg.Number,
					"bound", boundNumber)
				msg.Number = boundNumber
				reconciledNumber = boundNumber
			}
		}
	}

	registeredNumber := msg.Number
	conn := &signaling.Conn{
		WS:         ws,
		LineID:     boundLineID,
		HardwareID: msg.HardwareID,
		Send:       make(chan []byte, wsSendBuf),
		LastSeen:   time.Now(),
	}
	if isPaired {
		conn.ValidateBinding = func() bool {
			lineID, currentNumber, bindErr := h.deviceStore.BoundLineIdentity(r.Context(), conn.HardwareID)
			return bindErr == nil && lineID == conn.LineID && currentNumber == registeredNumber
		}
	}
	// Self-heal a stale-number register: tell the device its real line number
	// so digitsd persists it and registers correctly next time. Enqueued before
	// Register so the conn is not yet visible to hub fan-out: nothing else can
	// contend for the freshly created buffer, so this send never blocks. The
	// write pump below drains it once it starts.
	if reconciledNumber != "" {
		conn.Send <- mustMarshal(&signaling.Message{
			Type:   signaling.TypeLineRenumber,
			Number: reconciledNumber,
		})
	}
	register := func() error { return h.hub.Register(registeredNumber, conn) }
	if isPaired {
		err = h.lineStore.WithRenumberReadFence(r.Context(), func(fencedCtx context.Context) error {
			lineID, currentNumber, bindErr := h.deviceStore.BoundLineIdentity(fencedCtx, conn.HardwareID)
			if bindErr != nil || lineID != conn.LineID || currentNumber != registeredNumber {
				return errStaleConnectionBinding
			}
			if err := register(); err != nil {
				return err
			}
			h.relay.OnRegistered(fencedCtx, registeredNumber)
			h.relay.OnReconnect(fencedCtx, registeredNumber, conn.HardwareID)
			return nil
		})
	} else {
		err = register()
	}
	if err != nil {
		if errors.Is(err, signaling.ErrDraining) {
			wsReject(ws, "server shutting down")
		} else {
			_ = ws.Close()
		}
		return
	}
	number := registeredNumber
	ctx := r.Context()

	// Configure pong handler to extend read deadline on each pong
	if err := ws.SetReadDeadline(time.Now().Add(wsPongTimeout)); err != nil {
		slog.WarnContext(ctx, "ws set pong deadline failed", "err", err)
	}
	ws.SetPongHandler(func(string) error {
		if !h.hub.EnsureConnBindingCurrent(conn) {
			return errStaleConnectionBinding
		}
		if err := ws.SetReadDeadline(time.Now().Add(wsPongTimeout)); err != nil {
			return err
		}
		h.hub.TouchLastSeen(number, conn.HardwareID, conn.ConnectionID)
		return nil
	})

	// Write pump with periodic pings
	go func() {
		ticker := time.NewTicker(wsPingInterval)
		defer ticker.Stop()
		defer func() { _ = ws.Close() }()
		for {
			select {
			case data, ok := <-conn.Send:
				if !ok {
					return
				}
				if err := ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout)); err != nil {
					return
				}
				if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
					slog.ErrorContext(ctx, "websocket write failed", "number", number, "err", err)
					return
				}
			case <-ticker.C:
				if !h.hub.EnsureConnBindingCurrent(conn) {
					return
				}
				if err := ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout)); err != nil {
					return
				}
				if err := ws.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()

	// Read pump (blocks until disconnect)
	defer h.hub.Unregister(number, conn)
	defer h.relay.OnConnClosed(ctx, conn)
	defer func() {
		if msg.HardwareID != "" && h.deviceStore != nil {
			if err := h.deviceStore.TouchLastSeen(ctx, msg.HardwareID); err != nil {
				slog.WarnContext(ctx, "touch last seen on disconnect failed", "hardware_id", msg.HardwareID, "err", err)
			}
		}
	}()
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				slog.ErrorContext(ctx, "websocket read failed", "number", number, "err", err)
			}
			break
		}
		msg, err := signaling.ParseMessage(data)
		if err != nil {
			slog.WarnContext(ctx, "bad websocket message", "number", number, "err", err)
			continue
		}
		err = h.withCurrentConn(ctx, conn, func(fencedCtx context.Context) error {
			// TypeRepair is intercepted here because the authenticated hardware
			// identity is available at the WebSocket boundary.
			if msg.Type == signaling.TypeRepair {
				if h.deviceStore != nil {
					if unpairErr := h.deviceStore.Unpair(fencedCtx, conn.HardwareID); unpairErr != nil {
						slog.WarnContext(fencedCtx, "repair: unpair failed", "hardware_id", conn.HardwareID, "err", unpairErr)
					} else {
						slog.InfoContext(fencedCtx, "repair: device unpaired by client request", "hardware_id", conn.HardwareID, "number", number)
					}
				}
				return nil
			}
			msg.HardwareID = conn.HardwareID
			h.relay.HandleMessageFromBinding(fencedCtx, number, conn.LineID, conn.ConnectionID, msg)
			return nil
		})
		if err != nil {
			return
		}
	}
}

func mustMarshal(msg *signaling.Message) []byte {
	data, err := msg.Marshal()
	if err != nil {
		panic(fmt.Sprintf("mustMarshal: %v", err))
	}
	return data
}

// handleTestStartCall is a DEV_MODE test-harness endpoint used by the
// Playwright suite to seed an active call without driving the full
// signaling flow. Never registered in production builds.
func (h *Handler) handleTestStartCall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Caller string `json:"caller"`
		Callee string `json:"callee"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if body.Caller == "" || body.Callee == "" {
		http.Error(w, "caller and callee required", http.StatusBadRequest)
		return
	}
	id, err := h.tracker.OnCallInitiated(r.Context(), body.Caller, body.Callee)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = writeJSON(w, map[string]any{"id": id})
}

// handleTestStartConference is a DEV_MODE test-harness endpoint that seeds
// two 2-party calls and then merges them into a conference. Used by the
// Playwright suite to drive the observation deck without going through
// signaling.
func (h *Handler) handleTestStartConference(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Host  string   `json:"host"`
		Added []string `json:"added"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if body.Host == "" || len(body.Added) != 2 {
		http.Error(w, "host and exactly 2 added members required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	for _, callee := range body.Added {
		if _, err := h.tracker.OnCallInitiated(ctx, body.Host, callee); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := h.tracker.OnCallAnswered(ctx, body.Host, callee); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	originatingCallID := h.tracker.CallIDForPair(ctx, body.Host, body.Added[0])
	if originatingCallID == 0 {
		http.Error(w, "originating call not found", http.StatusInternalServerError)
		return
	}
	conf, err := h.tracker.CreateConferencePersistent(ctx, body.Host, originatingCallID, body.Added)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = writeJSON(w, map[string]any{"conf_id": conf.ID.String()})
}

// handleDevSeedFirmware registers a fake hub entry for a line number with the
// given firmware (and optional pi) version. It is only reachable when DevMode
// is true and lets the Playwright e2e suite (and interactive dev sessions)
// exercise the update chip without a real device connection.
//
// POST /dev/seed-firmware?number=<line-number>&fw=<semver>[&pi=<semver>]
func (h *Handler) handleDevSeedFirmware(w http.ResponseWriter, r *http.Request) {
	number := r.URL.Query().Get("number")
	fw := r.URL.Query().Get("fw")
	pi := r.URL.Query().Get("pi")
	ip := r.URL.Query().Get("ip")
	dm := r.URL.Query().Get("dev_mode") == "1"
	if number == "" || fw == "" {
		http.Error(w, "number and fw query params are required", http.StatusBadRequest)
		return
	}
	conn := &signaling.Conn{Send: make(chan []byte, 8)}
	// Drain Send so any hub fan-out to this fake device is silently discarded
	// instead of blocking at the channel cap during interactive dev testing.
	go func() {
		for range conn.Send {
		}
	}()
	if err := h.hub.Register(number, conn); err != nil {
		http.Error(w, "server shutting down", http.StatusServiceUnavailable)
		return
	}
	h.hub.UpdateDeviceInfo(number, signaling.DeviceInfoParams{
		PiVersion:       pi,
		FirmwareVersion: fw,
		RemoteAddr:      ip,
		DevMode:         dm,
	})
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"ok":true,"number":%q,"fw":%q,"pi":%q}`, number, fw, pi)
}
