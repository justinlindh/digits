package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/justinlindh/digits/server/internal/signaling"
)

// wsReject sends an error message to the WebSocket client and closes the connection.
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
		slog.Error("websocket upgrade failed", "err", err)
		return
	}

	// Wait for register message
	_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, data, err := ws.ReadMessage()
	if err != nil {
		slog.Error("websocket no register message", "err", err)
		_ = ws.Close()
		return
	}
	_ = ws.SetReadDeadline(time.Time{})

	msg, err := signaling.ParseMessage(data)
	if err != nil || msg.Type != signaling.TypeRegister || msg.Number == "" {
		slog.Warn("invalid register message")
		wsReject(ws, "must send register message first")
		return
	}

	// Require hardware ID for all connections
	if msg.HardwareID == "" {
		slog.Warn("ws register without hardware_id", "number", msg.Number)
		wsReject(ws, "hardware_id required")
		return
	}

	// Check pairing and token status. For paired devices, the server-side
	// bound line number is authoritative: a device cannot register as a
	// line it is not paired to.
	isPaired := false
	if h.pairingStore != nil {
		paired, tokenValid, err := h.deviceStore.AuthStatus(r.Context(), msg.HardwareID, msg.DeviceToken)
		if err != nil {
			slog.Error("device auth check failed", "hardware_id", msg.HardwareID, "err", err)
			wsReject(ws, "internal error")
			return
		}
		if !paired {
			code, err := h.pairingStore.GenerateCode(r.Context(), msg.HardwareID)
			if err != nil {
				slog.Error("generate pairing code failed", "hardware_id", msg.HardwareID, "err", err)
			} else {
				_ = ws.WriteMessage(websocket.TextMessage, mustMarshal(&signaling.Message{
					Type:        signaling.TypePairingCode,
					PairingCode: code,
				}))
			}
			// Unpaired devices register under their hardware ID (not a line
			// number) so they can receive the TypePaired message via
			// SendToHardware without displacing a real line's connection.
			msg.Number = "unpaired:" + msg.HardwareID
		} else if msg.DeviceToken == "" {
			slog.Warn("ws register without device_token", "hardware_id", msg.HardwareID)
			wsReject(ws, "device_token required")
			return
		} else if !tokenValid {
			slog.Warn("ws invalid device_token", "hardware_id", msg.HardwareID)
			wsReject(ws, "invalid device_token")
			return
		} else {
			isPaired = true
			boundNumber, err := h.deviceStore.BoundLineNumber(r.Context(), msg.HardwareID)
			if err != nil {
				slog.Error("bound line lookup failed", "hardware_id", msg.HardwareID, "err", err)
				wsReject(ws, "internal error")
				return
			}
			if boundNumber == "" {
				slog.Warn("paired device has no bound line", "hardware_id", msg.HardwareID)
				wsReject(ws, "device has no assigned line")
				return
			}
			if boundNumber != msg.Number {
				slog.Warn("ws register number mismatch",
					"hardware_id", msg.HardwareID,
					"claimed", msg.Number,
					"bound", boundNumber)
				wsReject(ws, "number does not match paired line")
				return
			}
		}
	}

	const (
		wsPingInterval = 30 * time.Second
		wsPongTimeout  = 45 * time.Second
		wsWriteTimeout = 10 * time.Second
	)

	conn := &signaling.Conn{
		WS:         ws,
		HardwareID: msg.HardwareID,
		Send:       make(chan []byte, 32),
		LastSeen:   time.Now(),
	}
	if err := h.hub.Register(msg.Number, conn); err != nil {
		wsReject(ws, "server shutting down")
		return
	}
	if isPaired {
		h.relay.OnRegistered(r.Context(), msg.Number)
	}
	number := msg.Number

	// Configure pong handler to extend read deadline on each pong
	_ = ws.SetReadDeadline(time.Now().Add(wsPongTimeout))
	ws.SetPongHandler(func(string) error {
		_ = ws.SetReadDeadline(time.Now().Add(wsPongTimeout))
		h.hub.TouchLastSeen(number)
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
					slog.Error("websocket write failed", "number", number, "err", err)
					return
				}
			case <-ticker.C:
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
	defer h.relay.OnDisconnect(r.Context(), number)
	defer func() {
		if msg.HardwareID != "" && h.deviceStore != nil {
			if err := h.deviceStore.TouchLastSeen(r.Context(), msg.HardwareID); err != nil {
				slog.Warn("touch last seen on disconnect failed", "hardware_id", msg.HardwareID, "err", err)
			}
		}
	}()
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				slog.Error("websocket read failed", "number", number, "err", err)
			}
			break
		}
		msg, err := signaling.ParseMessage(data)
		if err != nil {
			slog.Warn("bad websocket message", "number", number, "err", err)
			continue
		}
		// TypeRepair is intercepted here (not in relay) because we have the
		// authenticated connection's HardwareID in scope and don't need to
		// thread the device store into the Relay. The phone calls this from
		// its *#0* callback before reboot to invalidate server-side pairing,
		// so the next register-without-token is treated as a fresh pair-up
		// rather than rejected as "device_token required".
		if msg.Type == signaling.TypeRepair {
			if h.deviceStore != nil && msg.HardwareID != "" {
				if err := h.deviceStore.Unpair(r.Context(), msg.HardwareID); err != nil {
					slog.Warn("repair: unpair failed", "hardware_id", msg.HardwareID, "err", err)
				} else {
					slog.Info("repair: device unpaired by client request", "hardware_id", msg.HardwareID, "number", number)
				}
			}
			continue
		}
		msg.HardwareID = conn.HardwareID
		h.relay.HandleMessage(r.Context(), number, msg)
	}
}

func mustMarshal(msg *signaling.Message) []byte {
	data, _ := msg.Marshal()
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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
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
	originatingCallID := h.tracker.CallIDForPair(body.Host, body.Added[0])
	if originatingCallID == 0 {
		http.Error(w, "originating call not found", http.StatusInternalServerError)
		return
	}
	conf, err := h.tracker.CreateConferencePersistent(ctx, body.Host, originatingCallID, body.Added)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"conf_id": conf.ID.String()})
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
	h.hub.UpdateDeviceInfo(number, pi, "", fw, "", "")
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"ok":true,"number":%q,"fw":%q,"pi":%q}`, number, fw, pi)
}
