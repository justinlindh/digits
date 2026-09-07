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
// queued here and delivered by the per-connection write pump goroutine. A nil
// element is the close sentinel: the pump delivers everything queued before
// it, then closes the socket (queued by closeConn for CloseLine and
// CloseHardware; Register closes the channel outright when it evicts a
// connection).
type Conn struct {
	WS         *websocket.Conn
	Number     string
	HardwareID string
	Send       chan []byte
	LastSeen   time.Time

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

// Hub manages all active device WebSocket connections and routes signaling
// messages between them. In single-instance mode it holds connections in
// memory; in cluster mode a RedisBridge fans out to sibling pods and a
// DeviceState tracks presence across the fleet.
type Hub struct {
	mu           sync.RWMutex
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
	voicemailUnheard map[string]map[string]int
	dashEvents       dashNotifier
	redis            redisPubSub  // nil = single-instance mode (no Redis)
	state            *DeviceState // nil = single-instance mode (no cluster state)
	draining         bool         // set by StartDraining; blocks new Register calls
	reconnectHook    func(number, hardwareID string)
	// dropHook is called each time a best-effort SendTo skips a device whose
	// send buffer is full. Optional; nil disables. Wired in cmd/signald/main.go
	// to the metrics registry to count dropped signaling sends.
	dropHook func()
}

// NewHub creates a Hub ready for use. Call SetRedis and SetDeviceState before
// Run to enable cluster mode; omitting them leaves the hub in single-instance
// mode.
func NewHub() *Hub {
	return &Hub{
		conns:            make(map[string][]*Conn),
		hwConns:          make(map[string]*Conn),
		updateStatus:     make(map[string]*UpdateStatusSnapshot),
		voicemailUnheard: make(map[string]map[string]int),
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
	data, err := marshalOptional(env.Message)
	if err != nil {
		slog.Debug("redis: marshal for local delivery failed", "err", err)
		return
	}

	// The close types are the only ones where a nil Message is meaningful
	// (no farewell). Everywhere else a nil payload would be queued as the
	// close sentinel, so it is refused below.
	switch env.TargetType {
	case "close":
		h.closeLocalLine(env.Target, data)
		return
	case "close_hardware":
		h.closeLocalHardware(env.Target, data)
		return
	}
	if data == nil {
		return
	}

	switch env.TargetType {
	case "number":
		h.mu.RLock()
		for _, conn := range h.conns[env.Target] {
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

	case "reconnect":
		// env.Message is guaranteed non-nil by the nil-data return above.
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
// same line each get their own connection (POTS extension model). If the
// same HardwareID already has a connection, on this number or another, the
// old one is closed and removed.
func (h *Hub) Register(number string, conn *Conn) error {
	h.mu.Lock()
	if h.draining {
		h.mu.Unlock()
		return ErrDraining
	}

	conn.Number = number

	// A device holds one connection: on a plain reconnect the old socket is
	// under the same number, after a renumber or move under a different
	// one. Either way the old connection would keep answering as its number
	// until its read loop unwinds, and its late Unregister would otherwise
	// tear down this connection's hardware entry and presence record.
	// removeLocked closes old.Send (the write pump exits on !ok) without
	// draining, so a queued outbound frame still gets flushed; double-close
	// is impossible because the removal makes old invisible to any later
	// Unregister, and every send into old.Send happens under h.mu.
	var evictedNumber string
	if old := h.hwConns[conn.HardwareID]; old != nil {
		if old.WS != nil {
			_ = old.WS.Close()
		}
		h.removeLocked(old.Number, old)
		if old.Number != number {
			evictedNumber = old.Number
		}
	}
	h.conns[number] = append(h.conns[number], conn)

	if conn.HardwareID != "" {
		h.hwConns[conn.HardwareID] = conn
	}

	devCount := len(h.conns[number])
	d := h.dashEvents
	ds := h.state
	h.mu.Unlock()

	slog.Debug("hub registered", "number", number, "hardware_id", conn.HardwareID,
		"devices_on_line", devCount)

	if d != nil {
		d.Notify()
	}
	if ds != nil {
		ds.SetOnline(context.Background(), number, DevicePresence{
			PodID:           ds.PodID(),
			HardwareID:      conn.HardwareID,
			PiVersion:       conn.PiVersion,
			PiCommit:        conn.PiCommit,
			FirmwareVersion: conn.FirmwareVersion,
			FirmwareCommit:  conn.FirmwareCommit,
			RemoteAddr:      conn.RemoteAddr,
			DevMode:         conn.DevMode,
		})
	}
	if evictedNumber != "" {
		slog.Info("hub evicted stale connection", "hardware_id", conn.HardwareID,
			"old_number", evictedNumber, "number", number)
		// The presence record already names the new number, so this only
		// drops the old line's membership.
		if ds != nil {
			ds.SetOffline(context.Background(), evictedNumber, conn.HardwareID)
		}
	}
	return nil
}

// Unregister removes the specific connection from the hub. Only the exact
// conn pointer is removed; other devices on the same line are not affected.
func (h *Hub) Unregister(number string, conn *Conn) {
	h.mu.Lock()
	changed := h.removeLocked(number, conn)
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
			ds.SetOffline(context.Background(), number, conn.HardwareID)
		}
	}
}

// removeLocked takes conn out of number's bucket, closing its Send channel,
// and reports whether it was there. Must be called under h.mu (write).
func (h *Hub) removeLocked(number string, conn *Conn) bool {
	conns := h.conns[number]
	i := slices.Index(conns, conn)
	if i < 0 {
		return false
	}
	close(conn.Send)
	h.conns[number] = append(conns[:i], conns[i+1:]...)
	if len(h.conns[number]) == 0 {
		delete(h.conns, number)
	}
	if conn.HardwareID != "" {
		if h.hwConns[conn.HardwareID] == conn {
			delete(h.hwConns, conn.HardwareID)
		}
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
	return true
}

// CloseLine ends every connection on number, on this pod and (via Redis) on
// every other pod, sending farewell first when it is non-nil. A connection's
// number is fixed for its lifetime: the WebSocket read loop routes every
// inbound frame as the number captured at register time and unregisters
// under it. So anything that changes which number a device answers to, or
// removes the line it answers on, must end the device's current connections
// and let it register again; the farewell tells it what to register as.
//
// Closing is asynchronous: the farewell and then the nil close sentinel are
// queued on each connection's Send channel, and whoever drains the channel
// closes the socket and unregisters. The line is empty once every read loop
// has unwound.
func (h *Hub) CloseLine(number string, farewell *Message) {
	data, err := marshalOptional(farewell)
	if err != nil {
		slog.Error("CloseLine: marshal farewell failed", "number", number, "err", err)
		return
	}
	h.closeLocalLine(number, data)

	h.mu.RLock()
	bridge := h.redis
	h.mu.RUnlock()
	if bridge != nil {
		h.publish(bridge, "close", number, farewell)
	}
}

// CloseHardware ends the connection for hardwareID on this pod and (via
// Redis) on every other pod, sending farewell first when it is non-nil.
// Unlike SendToHardware it publishes even when a local connection exists:
// a device that just reconnected to another pod can still have its dying
// socket registered here, and the point is that the live one ends too.
func (h *Hub) CloseHardware(hardwareID string, farewell *Message) {
	data, err := marshalOptional(farewell)
	if err != nil {
		slog.Error("CloseHardware: marshal farewell failed", "hardware_id", hardwareID, "err", err)
		return
	}
	h.closeLocalHardware(hardwareID, data)

	h.mu.RLock()
	bridge := h.redis
	h.mu.RUnlock()
	if bridge != nil {
		h.publish(bridge, "close_hardware", hardwareID, farewell)
	}
}

// marshalOptional marshals msg, mapping a nil message to a nil payload.
func marshalOptional(msg *Message) ([]byte, error) {
	if msg == nil {
		return nil, nil
	}
	return msg.Marshal()
}

// closeLocalLine queues farewell (when non-nil) then the close sentinel on
// every local connection for number.
func (h *Hub) closeLocalLine(number string, farewell []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, conn := range h.conns[number] {
		h.closeConn(conn, farewell)
	}
}

// closeLocalHardware queues farewell (when non-nil) then the close sentinel
// on the local connection for hardwareID, if there is one.
func (h *Hub) closeLocalHardware(hardwareID string, farewell []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if conn := h.hwConns[hardwareID]; conn != nil {
		h.closeConn(conn, farewell)
	}
}

// closeConn queues farewell (when non-nil) then the nil close sentinel on
// conn. Must be called under h.mu like every other send path so the channel
// cannot be closed mid-send. A dropped farewell counts as a dropped send; a
// connection whose buffer cannot take the sentinel is retried in the
// background so whatever is queued ahead of it is still delivered.
func (h *Hub) closeConn(conn *Conn, farewell []byte) {
	if farewell != nil {
		select {
		case conn.Send <- farewell:
		default:
			slog.Warn("close: send buffer full, farewell dropped",
				"number", conn.Number, "hardware_id", conn.HardwareID)
			if h.dropHook != nil {
				h.dropHook()
			}
		}
	}
	select {
	case conn.Send <- nil:
	default:
		go h.retryClose(conn)
	}
}

// closeRetryTimeout bounds retryClose. It is longer than the WebSocket write
// timeout so a pump that is blocked on a dead peer fails its write and
// unwinds on its own before the socket is closed under it. Polling backs off
// from sendRetryInterval to closeRetryMaxInterval.
const (
	closeRetryTimeout     = 15 * time.Second
	closeRetryMaxInterval = 500 * time.Millisecond
)

// retryClose keeps re-offering the close sentinel to a connection whose
// buffer was full until it fits or the connection has unregistered; past
// closeRetryTimeout the socket is closed outright so the read loop still
// unwinds, which drops whatever is still queued and counts as a drop.
func (h *Hub) retryClose(conn *Conn) {
	deadline := time.Now().Add(closeRetryTimeout)
	interval := sendRetryInterval
	for time.Now().Before(deadline) {
		time.Sleep(interval)
		interval = min(2*interval, closeRetryMaxInterval)
		if h.offerClose(conn) {
			return
		}
	}
	slog.Warn("close: send buffer still full, closing socket",
		"number", conn.Number, "hardware_id", conn.HardwareID)
	h.mu.RLock()
	dropHook := h.dropHook
	h.mu.RUnlock()
	if dropHook != nil {
		dropHook()
	}
	if conn.WS != nil {
		_ = conn.WS.Close()
	}
}

// offerClose queues the close sentinel on conn if there is room, reporting
// whether retryClose is done: the sentinel is queued or conn is gone.
func (h *Hub) offerClose(conn *Conn) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if !h.connIsCurrentLocked(conn) {
		return true
	}
	select {
	case conn.Send <- nil:
		return true
	default:
		return false
	}
}

// Get returns the first active connection for a number, or nil if none.
// Used for connectivity checks (is anyone online on this line?).
func (h *Hub) Get(number string) *Conn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	conns := h.conns[number]
	if len(conns) == 0 {
		return nil
	}
	return conns[0]
}

// GetAll returns all active connections for a number. Returns nil if none.
func (h *Hub) GetAll(number string) []*Conn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	conns := h.conns[number]
	if len(conns) == 0 {
		return nil
	}
	out := make([]*Conn, len(conns))
	copy(out, conns)
	return out
}

// ConnectionCount returns the number of active connections for a line.
func (h *Hub) ConnectionCount(number string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns[number])
}

// ConnIsCurrent reports whether conn is still the hub's registered connection
// on its line, compared by identity. When a device reconnects with the same
// hardware_id, Register removes the previous conn, so the displaced conn is
// no longer current even before its read loop runs Unregister. Disconnect
// handling uses this to avoid tearing down a line that a newer connection
// already owns.
func (h *Hub) ConnIsCurrent(conn *Conn) bool {
	if conn == nil {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.connIsCurrentLocked(conn)
}

// connIsCurrentLocked is ConnIsCurrent for callers already holding h.mu.
func (h *Hub) connIsCurrentLocked(conn *Conn) bool {
	return slices.Contains(h.conns[conn.Number], conn)
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
	data, err := msg.Marshal()
	if err != nil {
		return err
	}

	h.mu.RLock()
	conns := h.conns[number]
	bridge := h.redis
	dropHook := h.dropHook
	for _, conn := range conns {
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

	// Always publish for cross-pod delivery, even when a local device received
	// the message: a line's devices can live on other pods.
	if bridge != nil {
		h.publish(bridge, "number", number, msg)
		return nil
	}
	if len(conns) == 0 {
		return fmt.Errorf("phone %s: %w", number, ErrNotConnected)
	}
	return nil
}

// sendRetryInterval is how long SendToWithTimeout and retryClose sleep
// between attempts to re-offer a frame to a device whose send buffer was full.
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
	data, err := msg.Marshal()
	if err != nil {
		return err
	}

	h.mu.RLock()
	bridge := h.redis
	h.mu.RUnlock()
	if bridge != nil {
		h.publish(bridge, "number", number, msg)
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
		for _, conn := range conns {
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

// AllDeviceInfo returns version info for all connected devices on a line.
func (h *Hub) AllDeviceInfo(number string) []DeviceInfoSnapshot {
	h.mu.RLock()
	ds := h.state
	h.mu.RUnlock()
	if ds != nil {
		return ds.AllDeviceInfo(context.Background(), number)
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	conns := h.conns[number]
	if len(conns) == 0 {
		return nil
	}
	snapshots := make([]DeviceInfoSnapshot, len(conns))
	for i, c := range conns {
		snapshots[i] = DeviceInfoSnapshot{
			HardwareID:      c.HardwareID,
			PiVersion:       c.PiVersion,
			PiCommit:        c.PiCommit,
			FirmwareVersion: c.FirmwareVersion,
			FirmwareCommit:  c.FirmwareCommit,
			RemoteAddr:      c.RemoteAddr,
			DevMode:         c.DevMode,
		}
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
		perHW = make(map[string]int)
		h.voicemailUnheard[number] = perHW
	}
	perHW[hwID] = count
}

// LineVoicemailUnheard returns the sum of unheard-voicemail counts across all
// handsets currently tracked on this line. Zero when the line has no entries
// (no handsets ever reported, or all handsets disconnected).
func (h *Hub) LineVoicemailUnheard(number string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	perHW := h.voicemailUnheard[number]
	total := 0
	for _, n := range perHW {
		total += n
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
		ds.UpdateDeviceInfo(context.Background(), hwID, presence)
	}
	return true
}

// UpdateDeviceInfoByHardware sets version info for a specific device by
// hardware ID. Used when the caller knows which device sent the message.
func (h *Hub) UpdateDeviceInfoByHardware(hardwareID string, p DeviceInfoParams) bool {
	p.RemoteAddr = sanitizeLocalAddr(p.RemoteAddr)
	h.mu.Lock()
	conn, ok := h.hwConns[hardwareID]
	if !ok {
		h.mu.Unlock()
		return false
	}
	presence := applyDeviceInfo(conn, p)
	ds := h.state
	h.mu.Unlock()
	if ds != nil {
		ds.UpdateDeviceInfo(context.Background(), hardwareID, presence)
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

// TouchLastSeen updates the last-seen timestamp for the device identified
// by hardwareID on the given line. hardwareID must be non-empty; the WS
// handler enforces a hardware_id at register time, so every caller already
// has one in scope.
func (h *Hub) TouchLastSeen(number, hardwareID string) {
	now := time.Now()
	h.mu.Lock()
	for _, c := range h.conns[number] {
		if c.HardwareID == hardwareID {
			c.LastSeen = now
		}
	}
	ds := h.state
	h.mu.Unlock()
	if ds != nil {
		ds.TouchLastSeen(context.Background(), number, hardwareID)
	}
}

// LastSeenAt returns the most recent last-seen timestamp across all
// connected devices on a line, or nil if no device is online.
func (h *Hub) LastSeenAt(number string) *time.Time {
	h.mu.RLock()
	ds := h.state
	h.mu.RUnlock()
	if ds != nil {
		return ds.LastSeenAt(context.Background(), number)
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	var latest time.Time
	for _, conn := range h.conns[number] {
		if conn.LastSeen.After(latest) {
			latest = conn.LastSeen
		}
	}
	if latest.IsZero() {
		return nil
	}
	return &latest
}

// IsOnline returns true if the number has at least one active hub connection
// and is not an unpaired sentinel key.
func (h *Hub) IsOnline(number string) bool {
	if strings.HasPrefix(number, UnpairedPrefix) {
		return false
	}
	h.mu.RLock()
	ds := h.state
	h.mu.RUnlock()
	if ds != nil {
		return ds.IsOnline(context.Background(), number)
	}
	return h.Get(number) != nil
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
	if ds != nil {
		return ds.OnlineNumbers(context.Background())
	}
	return h.LocalNumbers()
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
