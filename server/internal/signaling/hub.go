package signaling

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/justinlindh/digits/server/internal/httputil"
)

// ErrNotConnected is returned by SendTo and SendToHardware when the target
// device has no active hub connection. Callers that treat "device offline" as
// expected (e.g., best-effort pushes) can check for it with errors.Is and skip
// logging.
var ErrNotConnected = errors.New("not connected")

// ErrDraining is returned by Register when the hub is draining (shutdown in
// progress). The WebSocket handler should reject new upgrade requests.
var ErrDraining = errors.New("hub is draining")

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
}

// dashNotifier is the subset of *dashboard/events.Broadcaster the Hub uses
// to wake dashboard SSE subscribers when the set of online lines changes.
// Optional; nil disables notifications.
type dashNotifier interface {
	Notify()
}

type Hub struct {
	mu           sync.RWMutex
	conns        map[string]*Conn                 // phone number -> connection
	hwConns      map[string]*Conn                 // hardware ID -> connection
	updateStatus map[string]*UpdateStatusSnapshot // phone number -> last update status
	dashEvents   dashNotifier
	redis        redisPubSub // nil = single-instance mode (no Redis)
	draining     bool        // set by StartDraining; blocks new Register calls
}

func NewHub() *Hub {
	return &Hub{
		conns:        make(map[string]*Conn),
		hwConns:      make(map[string]*Conn),
		updateStatus: make(map[string]*UpdateStatusSnapshot),
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

	switch env.TargetType {
	case "number":
		conn := h.Get(env.Target)
		if conn == nil {
			return
		}
		data, err := env.Message.Marshal()
		if err != nil {
			slog.Debug("redis: marshal for local delivery failed", "err", err)
			return
		}
		select {
		case conn.Send <- data:
			slog.Debug("redis: delivered to local connection", "pod", env.PodID, "delivered", true)
		default:
			slog.Debug("redis: local send buffer full", "pod", env.PodID)
		}

	case "hardware":
		h.mu.RLock()
		conn := h.hwConns[env.Target]
		h.mu.RUnlock()
		if conn == nil {
			return
		}
		data, err := env.Message.Marshal()
		if err != nil {
			slog.Debug("redis: marshal for local delivery failed", "err", err)
			return
		}
		select {
		case conn.Send <- data:
			slog.Debug("redis: delivered to local hardware connection", "pod", env.PodID, "delivered", true)
		default:
			slog.Debug("redis: local hw send buffer full", "pod", env.PodID)
		}

	case "broadcast":
		data, err := env.Message.Marshal()
		if err != nil {
			slog.Debug("redis: marshal for local broadcast failed", "err", err)
			return
		}
		h.mu.RLock()
		for _, conn := range h.conns {
			select {
			case conn.Send <- data:
			default:
			}
		}
		h.mu.RUnlock()
		slog.Debug("redis: delivered broadcast from remote pod", "pod", env.PodID)
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
	n := len(h.conns)
	snapshot := make([]*Conn, 0, n)
	for _, c := range h.conns {
		snapshot = append(snapshot, c)
	}
	h.mu.RUnlock()

	if n == 0 {
		slog.Info("drain: no connections to close")
		return
	}
	slog.Info("drain: sending close frames", "connections", n)

	// Send 1001 Going Away close frame to each connection.
	closeMsg := websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down")
	for _, c := range snapshot {
		if c.WS != nil {
			deadline, ok := ctx.Deadline()
			if !ok {
				deadline = time.Now().Add(5 * time.Second)
			}
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
			remaining := len(h.conns)
			h.mu.RUnlock()
			if remaining == 0 {
				slog.Info("drain: all connections closed gracefully")
				return
			}
		}
	}
}

// forceCloseAll hard-closes every remaining WebSocket connection.
func (h *Hub) forceCloseAll() {
	h.mu.RLock()
	remaining := len(h.conns)
	conns := make([]*Conn, 0, remaining)
	for _, c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.RUnlock()

	for _, c := range conns {
		if c.WS != nil {
			_ = c.WS.Close()
		}
	}
	slog.Info("drain: force-closed remaining connections", "connections", remaining)
}

// SetDashboardEvents registers an optional broadcaster that is signalled
// whenever the set of online lines changes. Wakes dashboard SSE subscribers.
// Safe to call once at startup; subsequent calls overwrite.
func (h *Hub) SetDashboardEvents(b dashNotifier) {
	h.mu.Lock()
	h.dashEvents = b
	h.mu.Unlock()
}

func (h *Hub) Register(number string, conn *Conn) error {
	h.mu.Lock()
	if h.draining {
		h.mu.Unlock()
		return ErrDraining
	}
	// Close existing connection for this number if any
	if old, ok := h.conns[number]; ok {
		if old.WS != nil {
			_ = old.WS.Close() // close WebSocket first so write pump exits
		}
		// Only close Send if it's not already closed
		select {
		case _, ok := <-old.Send:
			if ok {
				close(old.Send)
			}
		default:
			close(old.Send)
		}
	}
	conn.Number = number
	h.conns[number] = conn
	if conn.HardwareID != "" {
		h.hwConns[conn.HardwareID] = conn
	}
	d := h.dashEvents
	h.mu.Unlock()
	slog.Debug("hub registered", "number", number)

	if d != nil {
		d.Notify()
	}
	return nil
}

func (h *Hub) Unregister(number string, conn *Conn) {
	h.mu.Lock()
	var changed bool
	if existing, ok := h.conns[number]; ok && existing == conn {
		close(conn.Send)
		delete(h.conns, number)
		if conn.HardwareID != "" {
			delete(h.hwConns, conn.HardwareID)
		}
		changed = true
	}
	d := h.dashEvents
	h.mu.Unlock()

	if changed {
		slog.Debug("hub unregistered", "number", number)
		if d != nil {
			d.Notify()
		}
	}
}

func (h *Hub) Get(number string) *Conn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.conns[number]
}

func (h *Hub) SendTo(number string, msg *Message) error {
	err := sendToConn(h.Get(number), msg, "phone", number)
	if err == nil {
		return nil
	}

	// Only fall through to Redis when the device is not connected locally.
	// Buffer-full means the connection exists on this pod but is
	// backpressured; other pods won't have it either.
	if !errors.Is(err, ErrNotConnected) {
		return err
	}

	h.mu.RLock()
	bridge := h.redis
	h.mu.RUnlock()
	if bridge != nil {
		bridge.Publish(context.Background(), &Envelope{
			TargetType: "number",
			Target:     number,
			Message:    msg,
		})
		return nil
	}

	return fmt.Errorf("phone %s: %w", number, ErrNotConnected)
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

	if bridge != nil {
		bridge.Publish(context.Background(), &Envelope{
			TargetType: "hardware",
			Target:     hardwareID,
			Message:    msg,
		})
		return nil
	}

	return fmt.Errorf("hardware %s: %w", hardwareID, ErrNotConnected)
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
	for number, conn := range h.conns {
		select {
		case conn.Send <- data:
		default:
			slog.Warn("broadcast: send buffer full, skipping", "number", number)
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
}

// DeviceInfo returns version info for a connected phone. Returns nil if offline.
func (h *Hub) DeviceInfo(number string) *DeviceInfoSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	conn, ok := h.conns[number]
	if !ok {
		return nil
	}
	return &DeviceInfoSnapshot{
		PiVersion:       conn.PiVersion,
		PiCommit:        conn.PiCommit,
		FirmwareVersion: conn.FirmwareVersion,
		FirmwareCommit:  conn.FirmwareCommit,
		RemoteAddr:      conn.RemoteAddr,
	}
}

// SetUpdateStatus stores the latest update status for a phone.
func (h *Hub) SetUpdateStatus(number, status, detail string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.updateStatus[number] = &UpdateStatusSnapshot{
		Status:    status,
		Detail:    detail,
		UpdatedAt: time.Now(),
	}
}

// GetUpdateStatus returns the latest update status for a phone, or nil.
func (h *Hub) GetUpdateStatus(number string) *UpdateStatusSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.updateStatus[number]
}

// ClearUpdateStatus removes update status for a phone.
func (h *Hub) ClearUpdateStatus(number string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.updateStatus, number)
}

// UpdateDeviceInfo sets version info and the device-reported LAN address for
// a connected phone under the write lock. localAddr is filtered through
// httputil.IsPrivateAddr; non-private values (or unparseable input) are
// stored as "" so a compromised client cannot push a public IP into the
// owner UI.
func (h *Hub) UpdateDeviceInfo(number, piVer, piCommit, fwVer, fwCommit, localAddr string) bool {
	if !httputil.IsPrivateAddr(localAddr) {
		localAddr = ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	conn, ok := h.conns[number]
	if !ok {
		return false
	}
	conn.PiVersion = piVer
	conn.PiCommit = piCommit
	conn.FirmwareVersion = fwVer
	conn.FirmwareCommit = fwCommit
	conn.RemoteAddr = localAddr
	return true
}

// TouchLastSeen updates the in-memory last-seen timestamp for a connected phone.
func (h *Hub) TouchLastSeen(number string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if conn, ok := h.conns[number]; ok {
		conn.LastSeen = time.Now()
	}
}

// LastSeenAt returns the last-seen timestamp for a connected phone, or nil if offline.
func (h *Hub) LastSeenAt(number string) *time.Time {
	h.mu.RLock()
	defer h.mu.RUnlock()
	conn, ok := h.conns[number]
	if !ok {
		return nil
	}
	if conn.LastSeen.IsZero() {
		return nil
	}
	t := conn.LastSeen
	return &t
}

// IsOnline returns true if number has an active hub connection and is not in
// pairing mode. Unpaired devices connect under the sentinel "unpaired" number
// and must not be presented as online in the UI.
func (h *Hub) IsOnline(number string) bool {
	if number == "unpaired" {
		return false
	}
	return h.Get(number) != nil
}

func (h *Hub) OnlineNumbers() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	nums := make([]string, 0, len(h.conns))
	for n := range h.conns {
		if n == "unpaired" {
			continue // skip unpaired phones awaiting pairing
		}
		nums = append(nums, n)
	}
	return nums
}
