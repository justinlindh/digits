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
// queued here and delivered by the per-connection write pump goroutine.
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
	bridge.Publish(context.Background(), &Envelope{
		TargetType: "reconnect",
		Target:     number,
		Message:    &Message{HardwareID: hardwareID},
	})
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
		h.mu.RLock()
		conn := h.hwConns[env.Target]
		h.mu.RUnlock()
		if conn == nil {
			return
		}
		select {
		case conn.Send <- data:
			slog.Debug("redis: delivered to local hardware connection", "pod", env.PodID, "delivered", true)
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
	h.mu.Lock()
	if h.draining {
		h.mu.Unlock()
		return ErrDraining
	}

	conn.Number = number

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
	return nil
}

// Unregister removes the specific connection from the hub. Only the exact
// conn pointer is removed; other devices on the same line are not affected.
func (h *Hub) Unregister(number string, conn *Conn) {
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
			ds.SetOffline(context.Background(), number, conn.HardwareID)
		}
	}
}

// RekeyNumber moves all connections and state from oldNumber to newNumber.
// Safe to call even if oldNumber has no entries.
func (h *Hub) RekeyNumber(oldNumber, newNumber string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cs, ok := h.conns[oldNumber]; ok {
		h.conns[newNumber] = cs
		delete(h.conns, oldNumber)
	}
	if vm, ok := h.voicemailUnheard[oldNumber]; ok {
		h.voicemailUnheard[newNumber] = vm
		delete(h.voicemailUnheard, oldNumber)
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

// ErrSendTimeout is returned by SendToWithTimeout when the target's send
// buffer does not drain within the deadline.
var ErrSendTimeout = errors.New("send timed out: buffer full")

// publishFallback routes msg to the target across pods via Redis when no
// local connection was found, returning nil once published. In
// single-instance mode (no Redis bridge) there is nowhere else to deliver, so
// it returns ErrNotConnected. target is the envelope target; label and target
// together form the wrapped error ("<label> <target>: not connected").
func (h *Hub) publishFallback(bridge redisPubSub, targetType, target, label string, msg *Message) error {
	if bridge != nil {
		bridge.Publish(context.Background(), &Envelope{
			TargetType: targetType,
			Target:     target,
			Message:    msg,
		})
		return nil
	}
	return fmt.Errorf("%s %s: %w", label, target, ErrNotConnected)
}

// SendTo marshals msg and sends it to every device on the given line number.
// This is the POTS extension model: a ring reaches all phones on the line.
// Returns ErrNotConnected only when no devices are connected locally AND
// Redis cannot deliver.
func (h *Hub) SendTo(number string, msg *Message) error {
	data, err := msg.Marshal()
	if err != nil {
		return err
	}

	h.mu.RLock()
	conns := h.conns[number]
	bridge := h.redis
	if len(conns) == 0 {
		h.mu.RUnlock()
		return h.publishFallback(bridge, "number", number, "phone", msg)
	}
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
	return nil
}

// sendRetryInterval is how long SendToWithTimeout sleeps between attempts to
// re-offer a message to a device whose send buffer was full.
const sendRetryInterval = 20 * time.Millisecond

// SendToWithTimeout delivers msg to every local device on number, retrying
// buffer-full devices until the buffer drains or the timeout elapses. Each
// send is attempted under the read lock inside a non-blocking select, so a
// concurrent Unregister (which closes conn.Send under the write lock) can
// never close a channel out from under an in-flight send: a removed conn is
// simply absent from the next iteration's list. delivered (keyed by *Conn)
// prevents re-sending to a device that already accepted the message while a
// sibling's buffer was still full.
//
// When no local device is connected the message is published to Redis for
// cross-pod delivery, mirroring SendTo, so reliable callers (ICE-restart)
// reach peers on other pods. Returns ErrSendTimeout if any device's buffer
// stays full for the whole timeout, ErrNotConnected if there is neither a
// local conn nor a Redis bridge.
func (h *Hub) SendToWithTimeout(number string, msg *Message, timeout time.Duration) error {
	data, err := msg.Marshal()
	if err != nil {
		return err
	}

	deadline := time.Now().Add(timeout)
	delivered := make(map[*Conn]bool)
	for {
		h.mu.RLock()
		conns := h.conns[number]
		bridge := h.redis
		dropHook := h.dropHook
		if len(conns) == 0 {
			h.mu.RUnlock()
			// No local conn: fall back to cross-pod delivery via Redis,
			// best-effort, exactly as SendTo does.
			return h.publishFallback(bridge, "number", number, "phone", msg)
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
	h.mu.RLock()
	conn := h.hwConns[hardwareID]
	bridge := h.redis
	h.mu.RUnlock()

	err := sendToConn(conn, msg, "hardware", hardwareID)
	if err == nil {
		return nil
	}

	if !errors.Is(err, ErrNotConnected) {
		return err
	}

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
		bridge.Publish(context.Background(), &Envelope{
			TargetType: "broadcast",
			Message:    msg,
		})
	}
}

// sendToConn marshals msg and pushes it onto conn.Send without blocking.
// label/id are only used to label the resulting error: "<label> <id>: <reason>".
// Returns ErrNotConnected when conn is nil so callers can errors.Is past
// expected offline cases.
func sendToConn(conn *Conn, msg *Message, label, id string) error {
	if conn == nil {
		return fmt.Errorf("%s %s: %w", label, id, ErrNotConnected)
	}
	data, err := msg.Marshal()
	if err != nil {
		return err
	}
	select {
	case conn.Send <- data:
		return nil
	default:
		return fmt.Errorf("send buffer full for %s %s", label, id)
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
