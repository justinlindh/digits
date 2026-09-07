// Package signaling implements the WebSocket-based signaling layer for
// peer-to-peer phone calls. Hub manages connected device sessions and routes
// messages; Relay enforces authorization and drives the call-state machine;
// RedisBridge extends both across multiple server replicas via pub/sub.
package signaling

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/justinlindh/digits/server/internal/httputil"
)

// UnpairedPrefix is the hub key prefix for devices that have connected but
// not yet completed pairing. Keys of this form intentionally never count as
// online line numbers and are excluded from IsOnline, LocalConnectionCount,
// and OnlineNumbers.
const UnpairedPrefix = "unpaired:"

// ErrNotConnected is returned by SendTo and SendToHardware when the target
// device has no active hub connection. Callers that treat "device offline" as
// expected (e.g., best-effort pushes) can check for it with errors.Is and skip
// logging.
var ErrNotConnected = errors.New("not connected")

// ErrDraining is returned by Register when the hub is draining (shutdown in
// progress). The WebSocket handler should reject new upgrade requests.
var ErrDraining = errors.New("hub is draining")

// Conn represents an active WebSocket connection from a device to the hub.
// The Send channel is the only safe write path; all outbound messages are
// queued here and delivered by the per-connection write pump goroutine.
type Conn struct {
	WS                 *websocket.Conn
	Number             string
	LineID             int64
	ConnectionID       string
	PresenceGeneration int64
	HardwareID         string
	Send               chan []byte
	LastSeen           time.Time
	ValidateBinding    func() bool

	// RemoteAddr is the device's primary LAN address as it sees itself,
	// reported by the device in the device_info message after register.
	// Hub.UpdateDeviceInfo assigns it; non-private values are filtered to
	// "" before storage. Empty until device_info arrives, or when the
	// device reports a non-private address.
	RemoteAddr string

	// Device info (reported on connect via device_info message)
	PiVersion       string
	PiCommit        string
	FirmwareVersion string
	FirmwareCommit  string
	DevMode         bool
}

// dashNotifier is the subset of *events.Broadcaster the Hub uses
// to wake dashboard SSE subscribers when the set of online lines changes.
// Optional; nil disables notifications.
type dashNotifier interface {
	Notify()
}

type voicemailPresence struct {
	LineID int64
	Count  int
}

// Hub manages all active device WebSocket connections and routes signaling
// messages between them. In single-instance mode it holds connections in
// memory; in cluster mode a RedisBridge fans out to sibling pods and a
// DeviceState tracks presence across the fleet.
type Hub struct {
	mu           sync.RWMutex
	presenceMu   sync.Mutex
	conns        map[string][]*Conn               // phone number -> connections (multiple devices per line)
	hwConns      map[string]*Conn                 // hardware ID -> connection
	updateStatus map[string]*UpdateStatusSnapshot // hardware id -> last update status
	// voicemailUnheard tracks the per-handset unheard-voicemail count last
	// reported by each device. Outer key is the phone number; inner key is
	// the originating handset's hardware ID. Per-handset because voicemail
	// storage is local to each handset on a multi-handset line, so the
	// line-level "you have N new messages" indicator is the SUM across
	// handsets. In-memory only; digitsd republishes on every reconnect, so
	// volatility is intentional.
	voicemailUnheard map[string]map[string]voicemailPresence
	dashEvents       dashNotifier
	redis            redisPubSub  // nil = single-instance mode (no Redis)
	state            *DeviceState // nil = single-instance mode (no cluster state)
	draining         bool         // set by StartDraining; blocks new Register calls
	reconnectHook    func(number, hardwareID string)
	// dropHook is called each time a best-effort SendTo skips a device whose
	// send buffer is full. Optional; nil disables. Wired in cmd/signald/main.go
	// to the metrics registry to count dropped signaling sends.
	dropHook                      func()
	lineResolver                  func(number string) (int64, error)
	lineNumberResolver            func(lineID int64) (string, error)
	legacyIdentityAllowedResolver func() (bool, error)
}

// NewHub creates a Hub ready for use. Call SetRedis and SetDeviceState before
// Run to enable cluster mode; omitting them leaves the hub in single-instance
// mode.
func NewHub() *Hub {
	return &Hub{
		conns:            make(map[string][]*Conn),
		hwConns:          make(map[string]*Conn),
		updateStatus:     make(map[string]*UpdateStatusSnapshot),
		voicemailUnheard: make(map[string]map[string]voicemailPresence),
	}
}

// SetRedis attaches a RedisBridge to the hub, enabling cross-pod message
// delivery. Must be called before Run. Passing nil disables Redis (the
// default single-instance mode).
func (h *Hub) SetRedis(bridge redisPubSub) {
	h.mu.Lock()
	h.redis = bridge
	h.mu.Unlock()
}

// SetReconnectHook registers a callback invoked when a "reconnect" envelope
// arrives from another pod. Used to cancel a grace timer held on this pod
// for a device that re-registered elsewhere.
func (h *Hub) SetReconnectHook(fn func(number, hardwareID string)) {
	h.mu.Lock()
	h.reconnectHook = fn
	h.mu.Unlock()
}

// SetDropHook registers a zero-argument callback that is called each time
// SendTo skips a device because its send buffer is full. Used by
// cmd/signald/main.go to wire in the metrics counter for dropped sends;
// nil disables instrumentation. Must be called before the hub starts
// handling messages.
func (h *Hub) SetDropHook(fn func()) {
	h.mu.Lock()
	h.dropHook = fn
	h.mu.Unlock()
}

// SetLineResolver configures the durable number-to-line identity fence used by
// line-targeted delivery. Connections whose immutable LineID no longer owns a
// reused number are excluded even if a reconnect control event was delayed.
func (h *Hub) SetLineResolver(fn func(number string) (int64, error), reverse func(lineID int64) (string, error)) {
	h.mu.Lock()
	h.lineResolver = fn
	h.lineNumberResolver = reverse
	h.mu.Unlock()
}

func (h *Hub) SetLegacyIdentityAllowedResolver(fn func() (bool, error)) {
	h.mu.Lock()
	h.legacyIdentityAllowedResolver = fn
	h.mu.Unlock()
}

// PublishReconnect broadcasts that a device re-registered, so any pod holding
// a grace timer for it cancels. No-op in single-instance mode.
func (h *Hub) PublishReconnect(number, hardwareID string) {
	h.mu.RLock()
	bridge := h.redis
	h.mu.RUnlock()
	if bridge == nil {
		return
	}
	h.publish(bridge, "reconnect", number, &Message{HardwareID: hardwareID})
}

// SetDeviceState attaches a DeviceState to the hub, enabling cluster-wide
// presence queries via Redis. Passing nil disables cluster state (the
// default single-instance mode).
func (h *Hub) SetDeviceState(ds *DeviceState) {
	h.mu.Lock()
	h.state = ds
	h.mu.Unlock()
}

// Run starts the Redis subscriber goroutine that delivers incoming
// cross-pod messages to local connections. Blocks until ctx is cancelled.
// If no RedisBridge is configured, Run returns immediately.
func (h *Hub) Run(ctx context.Context) {
	h.mu.RLock()
	bridge := h.redis
	h.mu.RUnlock()

	if bridge == nil {
		return
	}

	ch := bridge.Subscribe(ctx)
	for env := range ch {
		h.deliverFromRedis(env)
	}
}

// deliverFromRedis attempts local delivery of an envelope received from
// another pod via Redis.
func (h *Hub) deliverFromRedis(env *Envelope) {
	if env.Message == nil {
		return
	}
	data, err := env.Message.Marshal()
	if err != nil {
		slog.Debug("redis: marshal for local delivery failed", "err", err)
		return
	}

	switch env.TargetType {
	case "number":
		targetLineID := env.TargetLineID
		h.mu.RLock()
		resolver := h.lineResolver
		legacyResolver := h.legacyIdentityAllowedResolver
		h.mu.RUnlock()
		if targetLineID == 0 && legacyResolver != nil {
			allowed, policyErr := legacyResolver()
			if policyErr != nil || !allowed {
				return
			}
		}
		if resolver != nil {
			currentLineID, resolveErr := resolver(env.Target)
			if resolveErr != nil || (targetLineID != 0 && targetLineID != currentLineID) {
				return
			}
			targetLineID = currentLineID
		}
		h.mu.RLock()
		for _, conn := range h.conns[env.Target] {
			if targetLineID != 0 && conn.LineID != targetLineID {
				continue
			}
			select {
			case conn.Send <- data:
				slog.Debug("redis: delivered to local connection", "pod", env.PodID, "delivered", true)
			default:
				slog.Debug("redis: local send buffer full", "pod", env.PodID)
			}
		}
		h.mu.RUnlock()

	case "hardware":
		err := h.enqueueHardware(env.Target, data)
		switch {
		case err == nil:
			slog.Debug("redis: delivered to local hardware connection", "pod", env.PodID, "delivered", true)
		case errors.Is(err, ErrNotConnected):
			return
		default:
			slog.Debug("redis: local hw send buffer full", "pod", env.PodID)
		}

	case "broadcast":
		h.mu.RLock()
		for _, conns := range h.conns {
			for _, conn := range conns {
				select {
				case conn.Send <- data:
				default:
				}
			}
		}
		h.mu.RUnlock()
		slog.Debug("redis: delivered broadcast from remote pod", "pod", env.PodID)

	case "line_renumber":
		h.mu.RLock()
		resolver := h.lineNumberResolver
		h.mu.RUnlock()
		number := ""
		if resolver != nil {
			number, _ = resolver(env.TargetLineID)
		}
		h.deliverLineRenumber(env.TargetLineID, number)

	case "reconnect":
		// env.Message is guaranteed non-nil by the early return at the top of
		// deliverFromRedis.
		h.mu.RLock()
		hook := h.reconnectHook
		h.mu.RUnlock()
		if hook != nil {
			hook(env.Target, env.Message.HardwareID)
		}
	}
}

// StartDraining marks the hub as draining. New Register calls will return
// ErrDraining and the WebSocket handler should reject upgrade requests.
func (h *Hub) StartDraining() {
	h.mu.Lock()
	h.draining = true
	h.mu.Unlock()
	slog.Info("hub draining started")
}

// IsDraining reports whether the hub is in drain mode.
func (h *Hub) IsDraining() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.draining
}

// DrainAndClose sends a WebSocket close frame (1001 Going Away) to every
// connected device and waits for connections to disconnect. If the context
// deadline is reached, remaining connections are force-closed. The method
// logs aggregate counts only (no per-device data).
func (h *Hub) DrainAndClose(ctx context.Context) {
	h.mu.RLock()
	var snapshot []*Conn
	for _, conns := range h.conns {
		snapshot = append(snapshot, conns...)
	}
	n := len(snapshot)
	h.mu.RUnlock()

	if n == 0 {
		slog.InfoContext(ctx, "drain: no connections to close")
		return
	}

	// Send 1001 Going Away close frame to each connection.
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(5 * time.Second)
	}
	slog.InfoContext(ctx, "drain: sending close frames", "connections", n, "remaining", time.Until(deadline).Round(time.Millisecond))
	closeMsg := websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down")
	for _, c := range snapshot {
		if c.WS != nil {
			_ = c.WS.WriteControl(websocket.CloseMessage, closeMsg, deadline)
		}
	}

	// Poll until all connections are gone or the context expires.
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			h.forceCloseAll()
			return
		case <-ticker.C:
			h.mu.RLock()
			remaining := h.totalConns()
			h.mu.RUnlock()
			if remaining == 0 {
				slog.InfoContext(ctx, "drain: all connections closed gracefully")
				return
			}
		}
	}
}

// totalConns returns the total number of connections. Must be called with
// at least a read lock held.
func (h *Hub) totalConns() int {
	n := 0
	for _, conns := range h.conns {
		n += len(conns)
	}
	return n
}

// forceCloseAll hard-closes every remaining WebSocket connection.
func (h *Hub) forceCloseAll() {
	h.mu.RLock()
	var conns []*Conn
	for _, cs := range h.conns {
		conns = append(conns, cs...)
	}
	h.mu.RUnlock()

	for _, c := range conns {
		if c.WS != nil {
			_ = c.WS.Close()
		}
	}
	slog.Info("drain: force-closed remaining connections", "connections", len(conns))
}

// SetDashboardEvents registers an optional broadcaster that is signalled
// whenever the set of online lines changes. Wakes dashboard SSE subscribers.
// Safe to call once at startup; subsequent calls overwrite.
func (h *Hub) SetDashboardEvents(b dashNotifier) {
	h.mu.Lock()
	h.dashEvents = b
	h.mu.Unlock()
}

// Register adds a connection for the given number. Multiple devices on the
// same line each get their own connection (POTS extension model). If a
// connection with the same HardwareID is already registered on this number,
// the old one is closed and replaced.
func (h *Hub) Register(number string, conn *Conn) error {
	h.presenceMu.Lock()
	defer h.presenceMu.Unlock()
	h.mu.RLock()
	ds := h.state
	h.mu.RUnlock()
	if ds != nil && conn.HardwareID != "" && conn.PresenceGeneration == 0 {
		generation, err := ds.ClaimGeneration(context.Background(), conn.HardwareID)
		if err != nil {
			return fmt.Errorf("claim presence generation: %w", err)
		}
		conn.PresenceGeneration = generation
	}
	h.mu.Lock()
	if h.draining {
		h.mu.Unlock()
		return ErrDraining
	}

	conn.Number = number
	if conn.ConnectionID == "" {
		conn.ConnectionID = uuid.NewString()
	}

	// A hardware ID has exactly one live socket across all line numbers. This
	// removes a stale old-number registration before installing a reconnect on
	// the authoritative number.
	if old := h.hwConns[conn.HardwareID]; old != nil && old != conn && old.Number != number {
		oldConns := h.conns[old.Number]
		for i, candidate := range oldConns {
			if candidate == old {
				if old.WS != nil {
					_ = old.WS.Close()
				}
				close(old.Send)
				h.conns[old.Number] = append(oldConns[:i], oldConns[i+1:]...)
				if len(h.conns[old.Number]) == 0 {
					delete(h.conns, old.Number)
				}
				if perHW := h.voicemailUnheard[old.Number]; perHW != nil {
					delete(perHW, old.HardwareID)
					if len(perHW) == 0 {
						delete(h.voicemailUnheard, old.Number)
					}
				}
				break
			}
		}
	}

	// If the same hardware_id already has a connection on this number,
	// close the old one (device reconnect).
	existing := h.conns[number]
	replaced := false
	for i, old := range existing {
		if old.HardwareID != "" && old.HardwareID == conn.HardwareID {
			if old.WS != nil {
				_ = old.WS.Close()
			}
			// Close the old connection's send channel so its write pump exits
			// (the pump returns on the !ok branch). Double-close is impossible:
			// replacing the slot in place below makes the old conn invisible to
			// any later Unregister, and every send into old.Send happens under
			// h.mu, which we hold here. Draining first would silently drop a
			// queued outbound frame, so we close unconditionally instead.
			close(old.Send)
			existing[i] = conn
			replaced = true
			break
		}
	}
	if !replaced {
		h.conns[number] = append(existing, conn)
	}

	if conn.HardwareID != "" {
		h.hwConns[conn.HardwareID] = conn
	}

	devCount := len(h.conns[number])
	d := h.dashEvents
	h.mu.Unlock()

	slog.Debug("hub registered", "number", number, "hardware_id", conn.HardwareID,
		"devices_on_line", devCount)

	if d != nil {
		d.Notify()
	}
	if ds != nil {
		ds.SetOnline(context.Background(), number, DevicePresence{
			PodID:              ds.PodID(),
			LineID:             conn.LineID,
			ConnectionID:       conn.ConnectionID,
			PresenceGeneration: conn.PresenceGeneration,
			HardwareID:         conn.HardwareID,
			PiVersion:          conn.PiVersion,
			PiCommit:           conn.PiCommit,
			FirmwareVersion:    conn.FirmwareVersion,
			FirmwareCommit:     conn.FirmwareCommit,
			RemoteAddr:         conn.RemoteAddr,
			DevMode:            conn.DevMode,
		})
	}
	return nil
}

// Unregister removes the specific connection from the hub. Only the exact
// conn pointer is removed; other devices on the same line are not affected.
func (h *Hub) Unregister(number string, conn *Conn) {
	h.presenceMu.Lock()
	defer h.presenceMu.Unlock()
	h.mu.Lock()
	var changed bool
	conns := h.conns[number]
	for i, c := range conns {
		if c == conn {
			close(conn.Send)
			h.conns[number] = append(conns[:i], conns[i+1:]...)
			if len(h.conns[number]) == 0 {
				delete(h.conns, number)
			}
			if conn.HardwareID != "" {
				delete(h.hwConns, conn.HardwareID)
				// Drop the per-handset voicemail count too: the next
				// reconnect republishes it. Without this a vanished
				// handset would inflate the line-level sum forever.
				if perHW, ok := h.voicemailUnheard[number]; ok {
					delete(perHW, conn.HardwareID)
					if len(perHW) == 0 {
						delete(h.voicemailUnheard, number)
					}
				}
			}
			changed = true
			break
		}
	}
	remaining := len(h.conns[number])
	d := h.dashEvents
	ds := h.state
	h.mu.Unlock()

	if changed {
		slog.Debug("hub unregistered", "number", number, "remaining", remaining)
		if d != nil {
			d.Notify()
		}
		if ds != nil {
			ds.SetOffline(context.Background(), number, conn.HardwareID, conn.ConnectionID)
		}
	}
}

// RenumberLine retires every socket for lineID. When the current number can be
// resolved, the queued control frame lets digitsd persist it before reconnecting.
// Retirement does not depend on queue capacity or Redis delivery.
func (h *Hub) RenumberLine(lineID int64) {
	h.mu.RLock()
	resolver := h.lineNumberResolver
	bridge := h.redis
	h.mu.RUnlock()
	newNumber := ""
	if resolver != nil {
		newNumber, _ = resolver(lineID)
	}
	h.deliverLineRenumber(lineID, newNumber)
	if bridge != nil {
		bridge.Publish(context.Background(), &Envelope{TargetType: "line_renumber", TargetLineID: lineID, Message: &Message{Type: TypeLineRenumber}})
	}
}

func (h *Hub) deliverLineRenumber(lineID int64, newNumber string) {
	h.presenceMu.Lock()
	defer h.presenceMu.Unlock()
	var data []byte
	if newNumber != "" {
		data, _ = (&Message{Type: TypeLineRenumber, Number: newNumber}).Marshal()
	}

	// Validate outside h.mu because production validators query PostgreSQL.
	// Only sockets in this snapshot are candidates, so a concurrent reconnect
	// that registers after validation is never retired by an older event.
	h.mu.RLock()
	var candidates []*Conn
	for _, conns := range h.conns {
		for _, conn := range conns {
			if conn.LineID == lineID {
				candidates = append(candidates, conn)
			}
		}
	}
	h.mu.RUnlock()
	retire := make(map[*Conn]bool, len(candidates))
	for _, conn := range candidates {
		current := newNumber != "" && conn.Number == newNumber
		if conn.ValidateBinding != nil && conn.ValidateBinding() {
			current = true
		}
		retire[conn] = !current
	}

	type retiredConn struct {
		conn    *Conn
		deliver bool
	}
	var retired []retiredConn
	h.mu.Lock()
	for number, conns := range h.conns {
		kept := conns[:0]
		for _, conn := range conns {
			if !retire[conn] {
				kept = append(kept, conn)
				continue
			}
			delivered := false
			if len(data) != 0 {
				select {
				case conn.Send <- data:
					delivered = true
				default:
				}
			}
			close(conn.Send)
			if h.hwConns[conn.HardwareID] == conn {
				delete(h.hwConns, conn.HardwareID)
			}
			if perHW := h.voicemailUnheard[number]; perHW != nil {
				delete(perHW, conn.HardwareID)
				if len(perHW) == 0 {
					delete(h.voicemailUnheard, number)
				}
			}
			retired = append(retired, retiredConn{conn: conn, deliver: delivered})
		}
		if len(kept) == 0 {
			delete(h.conns, number)
		} else {
			h.conns[number] = kept
		}
	}
	d := h.dashEvents
	ds := h.state
	h.mu.Unlock()

	if len(retired) != 0 && d != nil {
		d.Notify()
	}
	for _, item := range retired {
		if ds != nil {
			ds.SetOffline(context.Background(), item.conn.Number, item.conn.HardwareID, item.conn.ConnectionID)
		}
		if !item.deliver && item.conn.WS != nil {
			_ = item.conn.WS.Close()
		}
	}
}

// lineIdentityPolicy resolves current ownership and whether legacy zero-ID
// connections are admissible during the default-off rolling period.
func (h *Hub) lineIdentityPolicy(number string) (lineID int64, allowLegacy, enforced, ok bool) {
	if strings.HasPrefix(number, UnpairedPrefix) {
		return 0, true, false, true
	}
	h.mu.RLock()
	resolver := h.lineResolver
	legacyResolver := h.legacyIdentityAllowedResolver
	h.mu.RUnlock()
	if resolver == nil {
		return 0, true, false, true
	}
	lineID, err := resolver(number)
	if err != nil {
		return 0, false, true, false
	}
	allowLegacy = true
	if legacyResolver != nil {
		var err error
		allowLegacy, err = legacyResolver()
		if err != nil {
			return 0, false, true, false
		}
	}
	return lineID, allowLegacy, true, true
}

// knownLineIdentityPolicy applies the monotonic legacy policy to a line ID
// already loaded by an owner-facing database query. It deliberately does not
// resolve the reusable number again.
func (h *Hub) knownLineIdentityPolicy(lineID int64) (allowLegacy, enforced, ok bool) {
	if lineID == 0 {
		return true, false, true
	}
	h.mu.RLock()
	legacyResolver := h.legacyIdentityAllowedResolver
	h.mu.RUnlock()
	allowLegacy = true
	if legacyResolver != nil {
		var err error
		allowLegacy, err = legacyResolver()
		if err != nil {
			return false, true, false
		}
	}
	return allowLegacy, true, true
}

func connectionMatchesLine(conn *Conn, lineID int64, allowLegacy, enforced bool) bool {
	return !enforced || conn.LineID == lineID || (allowLegacy && conn.LineID == 0)
}

// Get returns the first identity-eligible connection for a number.
func (h *Hub) Get(number string) *Conn {
	conns := h.GetAll(number)
	if len(conns) == 0 {
		return nil
	}
	return conns[0]
}

// GetAll returns identity-eligible active connections for a number.
func (h *Hub) GetAll(number string) []*Conn {
	h.mu.RLock()
	conns := append([]*Conn(nil), h.conns[number]...)
	h.mu.RUnlock()
	lineID, allowLegacy, enforced, ok := h.lineIdentityPolicy(number)
	if !ok {
		return nil
	}
	out := conns[:0]
	for _, conn := range conns {
		if connectionMatchesLine(conn, lineID, allowLegacy, enforced) {
			out = append(out, conn)
		}
	}
	return out
}

// GetAllForLine uses a caller-known immutable line identity instead of
// resolving the reusable number again.
func (h *Hub) GetAllForLine(number string, lineID int64) []*Conn {
	allowLegacy, enforced, ok := h.knownLineIdentityPolicy(lineID)
	if !ok {
		return nil
	}
	h.mu.RLock()
	conns := append([]*Conn(nil), h.conns[number]...)
	h.mu.RUnlock()
	out := conns[:0]
	for _, conn := range conns {
		if connectionMatchesLine(conn, lineID, allowLegacy, enforced) {
			out = append(out, conn)
		}
	}
	return out
}

// ConnectionCount returns the number of identity-eligible connections.
func (h *Hub) ConnectionCount(number string) int {
	return len(h.GetAll(number))
}

// ConnectionCountForLine counts connections for a caller-known line identity.
func (h *Hub) ConnectionCountForLine(number string, lineID int64) int {
	return len(h.GetAllForLine(number, lineID))
}

// ConnIsCurrent reports whether conn is still the hub's registered connection
// on its line, compared by identity. When a device reconnects with the same
// hardware_id, Register replaces the previous conn in place, so the displaced
// conn is no longer current even before its read loop runs Unregister.
// Disconnect handling uses this to avoid tearing down a line that a newer
// connection already owns.
func (h *Hub) ConnIsCurrent(conn *Conn) bool {
	if conn == nil {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return slices.Contains(h.conns[conn.Number], conn)
}

// ConnBindingCurrent reports whether the socket still represents the current
// number of its immutable line identity.
func (h *Hub) ConnBindingCurrent(conn *Conn) bool {
	if conn == nil {
		return false
	}
	if conn.ValidateBinding != nil {
		return conn.ValidateBinding()
	}
	if conn.LineID == 0 {
		return true
	}
	h.mu.RLock()
	resolver := h.lineNumberResolver
	h.mu.RUnlock()
	if resolver == nil {
		return true
	}
	number, err := resolver(conn.LineID)
	return err == nil && number == conn.Number
}

// EnsureConnBindingCurrent validates the socket against durable line ownership.
// A stale or unverifiable paired socket is retired before returning false.
func (h *Hub) EnsureConnBindingCurrent(conn *Conn) bool {
	if h.ConnBindingCurrent(conn) {
		return true
	}
	h.retireConn(conn)
	return false
}

func (h *Hub) retireConn(conn *Conn) {
	if conn == nil {
		return
	}
	h.presenceMu.Lock()
	defer h.presenceMu.Unlock()
	h.mu.Lock()
	conns := h.conns[conn.Number]
	found := false
	for i, candidate := range conns {
		if candidate != conn {
			continue
		}
		close(conn.Send)
		h.conns[conn.Number] = append(conns[:i], conns[i+1:]...)
		if len(h.conns[conn.Number]) == 0 {
			delete(h.conns, conn.Number)
		}
		if h.hwConns[conn.HardwareID] == conn {
			delete(h.hwConns, conn.HardwareID)
		}
		if perHW := h.voicemailUnheard[conn.Number]; perHW != nil {
			delete(perHW, conn.HardwareID)
			if len(perHW) == 0 {
				delete(h.voicemailUnheard, conn.Number)
			}
		}
		found = true
		break
	}
	d := h.dashEvents
	ds := h.state
	h.mu.Unlock()
	if !found {
		return
	}
	if d != nil {
		d.Notify()
	}
	if ds != nil {
		ds.SetOffline(context.Background(), conn.Number, conn.HardwareID, conn.ConnectionID)
	}
	if conn.WS != nil {
		_ = conn.WS.Close()
	}
}

// ErrSendTimeout is returned by SendToWithTimeout when the target's send
// buffer does not drain within the deadline.
var ErrSendTimeout = errors.New("send timed out: buffer full")

// publish sends msg to the target across pods via Redis. bridge must be
// non-nil. It centralizes Envelope construction so every cross-pod send path
// (the always-publish line/broadcast paths and the publishFallback no-local-
// conn path) frames messages identically; callers decide whether and when to
// publish.
func (h *Hub) publish(bridge redisPubSub, targetType, target string, msg *Message) {
	bridge.Publish(context.Background(), &Envelope{
		TargetType: targetType,
		Target:     target,
		Message:    msg,
	})
}

// publishFallback routes msg to the target across pods via Redis when no
// local connection was found, returning nil once published. In
// single-instance mode (no Redis bridge) there is nowhere else to deliver, so
// it returns ErrNotConnected. target is the envelope target; label and target
// together form the wrapped error ("<label> <target>: not connected").
func (h *Hub) publishFallback(bridge redisPubSub, targetType, target, label string, msg *Message) error {
	if bridge != nil {
		h.publish(bridge, targetType, target, msg)
		return nil
	}
	return fmt.Errorf("%s %s: %w", label, target, ErrNotConnected)
}

// SendTo marshals msg and sends it to every device on the given line number,
// wherever each device's WebSocket happens to be terminated. This is the POTS
// extension model: a ring (and every other line-targeted message) reaches all
// phones on the line. A line's devices can be spread across pods, so SendTo
// always both delivers to local connections AND, when Redis is configured,
// publishes for cross-pod delivery. The Redis subscriber filters out our own
// pod's envelope, so local devices are never double-sent. Publishing is
// unconditional rather than gated on per-line presence: a presence read per
// send would add a TOCTOU window (a device can connect on another pod between
// the check and the send) to shave a single missed map lookup off an already
// cheap call-signaling path, so the gate is deliberately omitted.
// Returns ErrNotConnected only when no devices are connected locally AND
// Redis is not configured.
func (h *Hub) SendTo(number string, msg *Message) error {
	targetLineID, allowLegacy, enforced, ok := h.lineIdentityPolicy(number)
	if !ok {
		return fmt.Errorf("resolve phone %s: identity unavailable", number)
	}
	return h.sendToLineIdentity(number, targetLineID, allowLegacy, enforced, msg)
}

// SendToLine sends only to connections matching a caller-known immutable line
// ID. Owner-authorized callers use this after loading a line so a later reuse
// of its number cannot retarget the message to the replacement owner.
func (h *Hub) SendToLine(number string, lineID int64, msg *Message) error {
	if lineID == 0 {
		_, allowLegacy, _, ok := h.lineIdentityPolicy(number)
		if !ok || !allowLegacy {
			return fmt.Errorf("resolve line %d: identity unavailable", lineID)
		}
		return h.sendToLineIdentity(number, 0, true, true, msg)
	}
	allowLegacy, enforced, ok := h.knownLineIdentityPolicy(lineID)
	if !ok {
		return fmt.Errorf("resolve line %d: identity unavailable", lineID)
	}
	return h.sendToLineIdentity(number, lineID, allowLegacy, enforced, msg)
}

func (h *Hub) sendToLineIdentity(number string, targetLineID int64, allowLegacy, enforced bool, msg *Message) error {
	data, err := msg.Marshal()
	if err != nil {
		return err
	}

	h.mu.RLock()
	conns := h.conns[number]
	bridge := h.redis
	dropHook := h.dropHook
	eligible := 0
	for _, conn := range conns {
		if !connectionMatchesLine(conn, targetLineID, allowLegacy, enforced) {
			continue
		}
		eligible++
		select {
		case conn.Send <- data:
		default:
			slog.Warn("SendTo: send buffer full, skipping device",
				"number", number, "hardware_id", conn.HardwareID)
			if dropHook != nil {
				dropHook()
			}
		}
	}
	h.mu.RUnlock()

	if bridge != nil {
		bridge.Publish(context.Background(), &Envelope{TargetType: "number", Target: number, TargetLineID: targetLineID, Message: msg})
		return nil
	}
	if eligible == 0 {
		return fmt.Errorf("phone %s: %w", number, ErrNotConnected)
	}
	return nil
}

// sendRetryInterval is how long SendToWithTimeout sleeps between attempts to
// re-offer a message to a device whose send buffer was full.
const sendRetryInterval = 20 * time.Millisecond

// SendToWithTimeout delivers msg to every device on number, retrying local
// buffer-full devices until the buffer drains or the timeout elapses. Each
// send is attempted under the read lock inside a non-blocking select, so a
// concurrent Unregister (which closes conn.Send under the write lock) can
// never close a channel out from under an in-flight send: a removed conn is
// simply absent from the next iteration's list. delivered (keyed by *Conn)
// prevents re-sending to a device that already accepted the message while a
// sibling's buffer was still full.
//
// A line's devices can be spread across pods, so when Redis is configured the
// message is always published once up front for cross-pod delivery (best
// effort, mirroring SendTo) so reliable callers (ICE-restart) reach peers on
// other pods even when a sibling device is connected locally. The Redis
// subscriber filters out our own pod's envelope, so local devices are not
// double-sent. Returns ErrSendTimeout if any local device's buffer stays full
// for the whole timeout, ErrNotConnected if there is neither a local conn nor
// a Redis bridge.
func (h *Hub) SendToWithTimeout(number string, msg *Message, timeout time.Duration) error {
	targetLineID, allowLegacy, enforced, ok := h.lineIdentityPolicy(number)
	if !ok {
		return fmt.Errorf("resolve phone %s: identity unavailable", number)
	}
	data, err := msg.Marshal()
	if err != nil {
		return err
	}

	h.mu.RLock()
	bridge := h.redis
	h.mu.RUnlock()
	if bridge != nil {
		bridge.Publish(context.Background(), &Envelope{TargetType: "number", Target: number, TargetLineID: targetLineID, Message: msg})
	}

	deadline := time.Now().Add(timeout)
	delivered := make(map[*Conn]bool)
	for {
		h.mu.RLock()
		conns := h.conns[number]
		dropHook := h.dropHook
		if len(conns) == 0 {
			h.mu.RUnlock()
			// No local conn: Redis (published above, if configured) is the only
			// delivery path. Without it there is nowhere to send.
			if bridge != nil {
				return nil
			}
			return fmt.Errorf("phone %s: %w", number, ErrNotConnected)
		}
		pending := false
		eligible := 0
		for _, conn := range conns {
			if !connectionMatchesLine(conn, targetLineID, allowLegacy, enforced) {
				continue
			}
			eligible++
			if delivered[conn] {
				continue
			}
			select {
			case conn.Send <- data:
				delivered[conn] = true
			default:
				pending = true
			}
		}
		h.mu.RUnlock()
		if eligible == 0 && bridge == nil {
			return fmt.Errorf("phone %s: %w", number, ErrNotConnected)
		}

		if !pending {
			return nil
		}
		if !time.Now().Before(deadline) {
			slog.Warn("SendToWithTimeout: send buffer full past deadline", "number", number)
			if dropHook != nil {
				dropHook()
			}
			return fmt.Errorf("phone %s: %w", number, ErrSendTimeout)
		}
		time.Sleep(sendRetryInterval)
	}
}

func (h *Hub) SendToHardware(hardwareID string, msg *Message) error {
	data, err := msg.Marshal()
	if err != nil {
		return err
	}

	err = h.enqueueHardware(hardwareID, data)
	if err == nil {
		return nil
	}

	if !errors.Is(err, ErrNotConnected) {
		return err
	}

	h.mu.RLock()
	bridge := h.redis
	h.mu.RUnlock()
	return h.publishFallback(bridge, "hardware", hardwareID, "hardware", msg)
}

// Broadcast marshals msg and sends it to every connected device without
// blocking. Devices whose Send buffers are full are skipped with a warning.
// When Redis is configured, the message is also published so other pods
// deliver to their local connections.
func (h *Hub) Broadcast(msg *Message) {
	data, err := msg.Marshal()
	if err != nil {
		slog.Error("broadcast marshal failed", "type", msg.Type, "err", err)
		return
	}
	h.mu.RLock()
	bridge := h.redis
	for number, conns := range h.conns {
		for _, conn := range conns {
			select {
			case conn.Send <- data:
			default:
				slog.Warn("broadcast: send buffer full, skipping", "number", number)
			}
		}
	}
	h.mu.RUnlock()

	if bridge != nil {
		h.publish(bridge, "broadcast", "", msg)
	}
}

// enqueueHardware pushes data onto the current hardware connection without
// blocking. The read lock protects the channel for the full lookup and enqueue
// operation because Register and Unregister close replaced or removed channels
// while holding the write lock.
func (h *Hub) enqueueHardware(hardwareID string, data []byte) error {
	h.mu.RLock()
	defer h.mu.RUnlock()

	conn := h.hwConns[hardwareID]
	if conn == nil {
		return fmt.Errorf("hardware %s: %w", hardwareID, ErrNotConnected)
	}
	select {
	case conn.Send <- data:
		return nil
	default:
		return fmt.Errorf("hardware %s: send buffer full", hardwareID)
	}
}

// UpdateStatusSnapshot holds the last update status reported by a phone.
type UpdateStatusSnapshot struct {
	Status    string    `json:"status"` // downloading, applying, rebooting, success, failed, ""
	Detail    string    `json:"detail"` // human-readable detail
	UpdatedAt time.Time `json:"updated_at"`
}

// DeviceInfoSnapshot holds a point-in-time copy of device version info.
type DeviceInfoSnapshot struct {
	HardwareID      string
	PiVersion       string
	PiCommit        string
	FirmwareVersion string
	FirmwareCommit  string

	// RemoteAddr is the resolved LAN address of the connected device.
	// Owner-scope HTML only: rendered on /phones and /phones/{number} for
	// the device owner, never serialized to JSON, SSE, or any other
	// external surface. The json:"-" tag enforces that for this type;
	// downstream code that copies the field into another type must apply
	// the same care.
	RemoteAddr string `json:"-"`

	DevMode bool `json:"-"`
}

// AllDeviceInfo returns version info for identity-eligible devices on a line.
func (h *Hub) AllDeviceInfo(number string) []DeviceInfoSnapshot {
	lineID, allowLegacy, enforced, ok := h.lineIdentityPolicy(number)
	if !ok {
		return nil
	}
	return h.allDeviceInfo(number, lineID, allowLegacy, enforced)
}

// AllDeviceInfoForLine returns device metadata for a caller-known line ID.
func (h *Hub) AllDeviceInfoForLine(number string, lineID int64) []DeviceInfoSnapshot {
	allowLegacy, enforced, ok := h.knownLineIdentityPolicy(lineID)
	if !ok {
		return nil
	}
	return h.allDeviceInfo(number, lineID, allowLegacy, enforced)
}

func (h *Hub) allDeviceInfo(number string, lineID int64, allowLegacy, enforced bool) []DeviceInfoSnapshot {
	h.mu.RLock()
	ds := h.state
	h.mu.RUnlock()
	if ds != nil {
		if enforced {
			return ds.AllDeviceInfoForIdentity(context.Background(), number, lineID, allowLegacy)
		}
		return ds.AllDeviceInfo(context.Background(), number)
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	var snapshots []DeviceInfoSnapshot
	for _, c := range h.conns[number] {
		if !connectionMatchesLine(c, lineID, allowLegacy, enforced) {
			continue
		}
		snapshots = append(snapshots, DeviceInfoSnapshot{
			HardwareID:      c.HardwareID,
			PiVersion:       c.PiVersion,
			PiCommit:        c.PiCommit,
			FirmwareVersion: c.FirmwareVersion,
			FirmwareCommit:  c.FirmwareCommit,
			RemoteAddr:      c.RemoteAddr,
			DevMode:         c.DevMode,
		})
	}
	return snapshots
}

// SetUpdateStatus stores the latest update status for a device identified by hardware ID.
func (h *Hub) SetUpdateStatus(hardwareID, status, detail string) {
	h.mu.Lock()
	h.updateStatus[hardwareID] = &UpdateStatusSnapshot{
		Status:    status,
		Detail:    detail,
		UpdatedAt: time.Now(),
	}
	ds := h.state
	h.mu.Unlock()
	if ds != nil {
		ds.SetUpdateStatus(context.Background(), hardwareID, status, detail)
	}
}

// GetUpdateStatus returns the latest update status for a device by hardware ID, or nil.
func (h *Hub) GetUpdateStatus(hardwareID string) *UpdateStatusSnapshot {
	h.mu.RLock()
	ds := h.state
	h.mu.RUnlock()
	if ds != nil {
		return ds.GetUpdateStatus(context.Background(), hardwareID)
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.updateStatus[hardwareID]
}

// ClearUpdateStatus removes update status for a device by hardware ID.
func (h *Hub) ClearUpdateStatus(hardwareID string) {
	h.mu.Lock()
	delete(h.updateStatus, hardwareID)
	ds := h.state
	h.mu.Unlock()
	if ds != nil {
		ds.ClearUpdateStatus(context.Background(), hardwareID)
	}
}

// SetVoicemailUnheard records the unheard-voicemail count last reported by
// the handset identified by hwID on this line. hwID is required: a missing
// hardware ID is silently dropped so a malformed message can't poison the
// shared map under a "" key. Per-handset because voicemail is local to the
// device; the per-line total is the sum across handsets.
func (h *Hub) SetVoicemailUnheard(number, hwID string, count int) {
	h.mu.RLock()
	lineID := int64(0)
	if conn := h.hwConns[hwID]; conn != nil && conn.Number == number {
		lineID = conn.LineID
	}
	h.mu.RUnlock()
	h.SetVoicemailUnheardForLine(number, lineID, hwID, count)
}

// SetVoicemailUnheardForLine binds a device report to the immutable line
// identity authenticated for its socket. This prevents a delayed old-owner
// report from becoming metadata for a replacement owner of the same number.
func (h *Hub) SetVoicemailUnheardForLine(number string, lineID int64, hwID string, count int) {
	if hwID == "" {
		return
	}
	if count < 0 {
		count = 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	perHW, ok := h.voicemailUnheard[number]
	if !ok {
		perHW = make(map[string]voicemailPresence)
		h.voicemailUnheard[number] = perHW
	}
	perHW[hwID] = voicemailPresence{LineID: lineID, Count: count}
}

// LineVoicemailUnheard returns the sum of unheard-voicemail counts across all
// handsets currently tracked on this line. Zero when the line has no entries
// (no handsets ever reported, or all handsets disconnected).
func (h *Hub) LineVoicemailUnheard(number string) int {
	lineID, allowLegacy, enforced, ok := h.lineIdentityPolicy(number)
	if !ok {
		return 0
	}
	return h.lineVoicemailUnheard(number, lineID, allowLegacy, enforced)
}

// LineVoicemailUnheardForLine returns counts for a caller-known line ID.
func (h *Hub) LineVoicemailUnheardForLine(number string, lineID int64) int {
	allowLegacy, enforced, ok := h.knownLineIdentityPolicy(lineID)
	if !ok {
		return 0
	}
	return h.lineVoicemailUnheard(number, lineID, allowLegacy, enforced)
}

func (h *Hub) lineVoicemailUnheard(number string, lineID int64, allowLegacy, enforced bool) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	perHW := h.voicemailUnheard[number]
	total := 0
	for _, presence := range perHW {
		if !enforced || presence.LineID == lineID || (allowLegacy && presence.LineID == 0) {
			total += presence.Count
		}
	}
	return total
}

// DeviceInfoParams carries the version and network fields a device reports in
// a device_info message. Using a named struct prevents mismatching the five
// positional string arguments (pi version, pi commit, fw version, fw commit,
// remote addr) that were previously passed individually.
// RemoteAddr holds the device's self-reported LAN address (LocalAddr on the
// wire); server-side code uses the RemoteAddr name to match Conn, DevicePresence,
// and DeviceInfoSnapshot.
type DeviceInfoParams struct {
	PiVersion       string
	PiCommit        string
	FirmwareVersion string
	FirmwareCommit  string
	RemoteAddr      string
	DevMode         bool
}

// UpdateDeviceInfo sets version info and the device-reported LAN address for
// the first (or only) device on number. RemoteAddr is filtered through
// httputil.IsPrivateAddr; non-private values (or unparseable input) are
// stored as "" so a compromised client cannot push a public IP into the
// owner UI. Multi-device callers that already know the hardware ID should
// use UpdateDeviceInfoByHardware instead.
func (h *Hub) UpdateDeviceInfo(number string, p DeviceInfoParams) bool {
	p.RemoteAddr = sanitizeLocalAddr(p.RemoteAddr)
	h.mu.Lock()
	conns := h.conns[number]
	if len(conns) == 0 {
		h.mu.Unlock()
		return false
	}
	conn := conns[0]
	presence := applyDeviceInfo(conn, p)
	hwID := conn.HardwareID
	ds := h.state
	h.mu.Unlock()
	if ds != nil {
		ds.UpdateDeviceInfo(context.Background(), hwID, conn.ConnectionID, presence)
	}
	return true
}

// UpdateDeviceInfoByHardware sets version info for a specific device by
// hardware ID. Used when the caller knows which device sent the message.
func (h *Hub) UpdateDeviceInfoByHardware(hardwareID string, p DeviceInfoParams) bool {
	h.mu.RLock()
	conn := h.hwConns[hardwareID]
	h.mu.RUnlock()
	if conn == nil {
		return false
	}
	return h.UpdateDeviceInfoByConnection(hardwareID, conn.ConnectionID, p)
}

// UpdateDeviceInfoByConnection rejects writes from a displaced socket.
func (h *Hub) UpdateDeviceInfoByConnection(hardwareID, connectionID string, p DeviceInfoParams) bool {
	p.RemoteAddr = sanitizeLocalAddr(p.RemoteAddr)
	h.mu.Lock()
	conn := h.hwConns[hardwareID]
	if conn == nil || conn.ConnectionID != connectionID {
		h.mu.Unlock()
		return false
	}
	presence := applyDeviceInfo(conn, p)
	ds := h.state
	h.mu.Unlock()
	if ds != nil {
		ds.UpdateDeviceInfo(context.Background(), hardwareID, connectionID, presence)
	}
	return true
}

// sanitizeLocalAddr accepts the raw LocalAddr string from the wire message and
// returns it unchanged only when it parses as a private address (RFC1918,
// loopback, link-local). Non-private or unparseable values are returned as ""
// so a compromised client cannot push a public IP into the owner UI.
func sanitizeLocalAddr(localAddr string) string {
	if !httputil.IsPrivateAddr(localAddr) {
		return ""
	}
	return localAddr
}

// applyDeviceInfo writes the device-reported fields onto conn and returns a
// matching DevicePresence to publish to the cluster-shared store. Caller
// must hold h.mu.
func applyDeviceInfo(conn *Conn, p DeviceInfoParams) DevicePresence {
	conn.PiVersion = p.PiVersion
	conn.PiCommit = p.PiCommit
	conn.FirmwareVersion = p.FirmwareVersion
	conn.FirmwareCommit = p.FirmwareCommit
	conn.RemoteAddr = p.RemoteAddr
	conn.DevMode = p.DevMode
	return DevicePresence{
		PiVersion:       p.PiVersion,
		PiCommit:        p.PiCommit,
		FirmwareVersion: p.FirmwareVersion,
		FirmwareCommit:  p.FirmwareCommit,
		RemoteAddr:      p.RemoteAddr,
		DevMode:         p.DevMode,
	}
}

// TouchLastSeen updates last-seen only when the reporting socket is still the
// authoritative hardware generation on this line.
func (h *Hub) TouchLastSeen(number, hardwareID, connectionID string) {
	now := time.Now()
	h.mu.Lock()
	conn := h.hwConns[hardwareID]
	if conn == nil || conn.Number != number || conn.ConnectionID != connectionID {
		h.mu.Unlock()
		return
	}
	conn.LastSeen = now
	ds := h.state
	h.mu.Unlock()
	if ds != nil {
		ds.TouchLastSeen(context.Background(), number, hardwareID, connectionID)
	}
}

// LastSeenAt returns the most recent last-seen timestamp across all
// connected devices on a line, or nil if no device is online.
func (h *Hub) LastSeenAt(number string) *time.Time {
	lineID, allowLegacy, enforced, ok := h.lineIdentityPolicy(number)
	if !ok {
		return nil
	}
	return h.lastSeenAt(number, lineID, allowLegacy, enforced)
}

// LastSeenAtForLine returns last-seen metadata for a caller-known line ID.
func (h *Hub) LastSeenAtForLine(number string, lineID int64) *time.Time {
	allowLegacy, enforced, ok := h.knownLineIdentityPolicy(lineID)
	if !ok {
		return nil
	}
	return h.lastSeenAt(number, lineID, allowLegacy, enforced)
}

func (h *Hub) lastSeenAt(number string, lineID int64, allowLegacy, enforced bool) *time.Time {
	h.mu.RLock()
	ds := h.state
	if ds == nil {
		defer h.mu.RUnlock()
		var latest time.Time
		for _, conn := range h.conns[number] {
			if connectionMatchesLine(conn, lineID, allowLegacy, enforced) && conn.LastSeen.After(latest) {
				latest = conn.LastSeen
			}
		}
		if latest.IsZero() {
			return nil
		}
		return &latest
	}
	h.mu.RUnlock()
	if enforced {
		return ds.LastSeenAtForIdentity(context.Background(), number, lineID, allowLegacy)
	}
	return ds.LastSeenAt(context.Background(), number)
}

// IsOnline returns true if the number has at least one active hub connection
// and is not an unpaired sentinel key.
func (h *Hub) IsOnline(number string) bool {
	if strings.HasPrefix(number, UnpairedPrefix) {
		return false
	}
	lineID, allowLegacy, enforced, ok := h.lineIdentityPolicy(number)
	if !ok {
		return false
	}
	return h.isOnline(number, lineID, allowLegacy, enforced)
}

// IsOnlineForLine checks presence for a caller-known line ID.
func (h *Hub) IsOnlineForLine(number string, lineID int64) bool {
	if strings.HasPrefix(number, UnpairedPrefix) {
		return false
	}
	allowLegacy, enforced, ok := h.knownLineIdentityPolicy(lineID)
	if !ok {
		return false
	}
	return h.isOnline(number, lineID, allowLegacy, enforced)
}

func (h *Hub) isOnline(number string, lineID int64, allowLegacy, enforced bool) bool {
	h.mu.RLock()
	ds := h.state
	conns := append([]*Conn(nil), h.conns[number]...)
	h.mu.RUnlock()
	if ds != nil {
		if enforced {
			return ds.LineIdentityOnline(context.Background(), number, lineID, allowLegacy)
		}
		return ds.IsOnline(context.Background(), number)
	}
	for _, conn := range conns {
		if connectionMatchesLine(conn, lineID, allowLegacy, enforced) {
			return true
		}
	}
	return false
}

// IsHardwareOnline reports whether the device with this hardware id is
// connected. When a shared device-state store is configured it answers
// cross-pod from Redis, matching IsOnline and AllDeviceInfo; otherwise it
// falls back to this pod's local connection map.
func (h *Hub) IsHardwareOnline(hardwareID string) bool {
	if hardwareID == "" {
		return false
	}
	h.mu.RLock()
	ds := h.state
	h.mu.RUnlock()
	if ds != nil {
		return ds.IsHardwareOnline(context.Background(), hardwareID)
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.hwConns[hardwareID] != nil
}

// HardwareOnlineOnLine reports whether the device with this hardware id has a
// live connection registered on number. When a shared device-state store is
// configured it answers cross-pod from Redis; otherwise it falls back to this
// pod's local connection map. Used by the grace-window expiry recheck, which
// must not trust an earlier snapshot of connectivity.
func (h *Hub) HardwareOnlineOnLine(number, hardwareID string) bool {
	if hardwareID == "" {
		return false
	}
	h.mu.RLock()
	ds := h.state
	h.mu.RUnlock()
	if ds != nil {
		return ds.HardwareOnlineOnLine(context.Background(), number, hardwareID)
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.conns[number] {
		if c.HardwareID == hardwareID {
			return true
		}
	}
	return false
}

func (h *Hub) LocalConnectionCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for key, conns := range h.conns {
		if strings.HasPrefix(key, UnpairedPrefix) {
			continue
		}
		n += len(conns)
	}
	return n
}

func (h *Hub) OnlineNumbers() []string {
	h.mu.RLock()
	ds := h.state
	h.mu.RUnlock()
	numbers := h.LocalNumbers()
	if ds != nil {
		numbers = ds.OnlineNumbers(context.Background())
	}
	online := numbers[:0]
	for _, number := range numbers {
		if h.IsOnline(number) {
			online = append(online, number)
		}
	}
	return online
}

// LineIdentity is a local connection's reusable number paired with its
// immutable line ID.
type LineIdentity struct {
	Number string
	LineID int64
}

// LocalNumbers returns the line numbers with at least one connection on THIS
// hub instance, regardless of Redis state. Unlike OnlineNumbers, it never
// consults the shared online roster, so in a multi-replica deployment each
// number is returned by exactly the one replica it is connected to. Unpaired
// keys are excluded. Used by the quiet-hours scheduler so a line is evaluated
// and pushed by a single replica (local push, no Redis fan-out, no
// duplication).
func (h *Hub) LocalNumbers() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	nums := make([]string, 0, len(h.conns))
	for n, conns := range h.conns {
		if strings.HasPrefix(n, UnpairedPrefix) || len(conns) == 0 {
			continue
		}
		nums = append(nums, n)
	}
	return nums
}

// LocalLineIdentities returns each distinct local number and line identity.
func (h *Hub) LocalLineIdentities() []LineIdentity {
	h.mu.RLock()
	legacyResolver := h.legacyIdentityAllowedResolver
	h.mu.RUnlock()
	allowLegacy := true
	if legacyResolver != nil {
		var err error
		allowLegacy, err = legacyResolver()
		if err != nil {
			allowLegacy = false
		}
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	seen := make(map[LineIdentity]struct{})
	identities := make([]LineIdentity, 0, len(h.conns))
	for number, conns := range h.conns {
		if strings.HasPrefix(number, UnpairedPrefix) {
			continue
		}
		for _, conn := range conns {
			if conn.LineID == 0 && !allowLegacy {
				continue
			}
			identity := LineIdentity{Number: number, LineID: conn.LineID}
			if _, ok := seen[identity]; ok {
				continue
			}
			seen[identity] = struct{}{}
			identities = append(identities, identity)
		}
	}
	return identities
}
