package signaling

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Conn struct {
	WS         *websocket.Conn
	Number     string
	HardwareID string
	Send       chan []byte

	// Device info (reported on connect via device_info message)
	PiVersion       string
	PiCommit        string
	FirmwareVersion string
	FirmwareCommit  string
	LastSeen        time.Time
}

type Hub struct {
	mu           sync.RWMutex
	conns        map[string]*Conn                // phone number → connection
	hwConns      map[string]*Conn                // hardware ID → connection
	updateStatus map[string]*UpdateStatusSnapshot // phone number → last update status
}

func NewHub() *Hub {
	return &Hub{
		conns:   make(map[string]*Conn),
		hwConns: make(map[string]*Conn),
	}
}

func (h *Hub) Register(number string, conn *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Close existing connection for this number if any
	if old, ok := h.conns[number]; ok {
		if old.WS != nil {
			old.WS.Close() // close WebSocket first so write pump exits
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
	slog.Debug("hub registered", "number", number)
}

func (h *Hub) Unregister(number string, conn *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if existing, ok := h.conns[number]; ok && existing == conn {
		close(conn.Send)
		delete(h.conns, number)
		if conn.HardwareID != "" {
			delete(h.hwConns, conn.HardwareID)
		}
		slog.Debug("hub unregistered", "number", number)
	}
}

func (h *Hub) Get(number string) *Conn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.conns[number]
}

func (h *Hub) SendTo(number string, msg *Message) error {
	conn := h.Get(number)
	if conn == nil {
		return fmt.Errorf("phone %s not connected", number)
	}
	data, err := msg.Marshal()
	if err != nil {
		return err
	}
	select {
	case conn.Send <- data:
		return nil
	default:
		return fmt.Errorf("send buffer full for %s", number)
	}
}

func (h *Hub) SendToHardware(hardwareID string, msg *Message) error {
	h.mu.RLock()
	conn := h.hwConns[hardwareID]
	h.mu.RUnlock()
	if conn == nil {
		return fmt.Errorf("hardware %s not connected", hardwareID)
	}
	data, err := msg.Marshal()
	if err != nil {
		return err
	}
	select {
	case conn.Send <- data:
		return nil
	default:
		return fmt.Errorf("send buffer full for hardware %s", hardwareID)
	}
}

// UpdateStatusSnapshot holds the last update status reported by a phone.
type UpdateStatusSnapshot struct {
	Status    string    `json:"status"`    // downloading, applying, rebooting, success, failed, ""
	Detail    string    `json:"detail"`    // human-readable detail
	UpdatedAt time.Time `json:"updated_at"`
}

// DeviceInfoSnapshot holds a point-in-time copy of device version info.
type DeviceInfoSnapshot struct {
	PiVersion       string
	PiCommit        string
	FirmwareVersion string
	FirmwareCommit  string
	LastSeen        time.Time
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
		LastSeen:        conn.LastSeen,
	}
}

// SetUpdateStatus stores the latest update status for a phone.
func (h *Hub) SetUpdateStatus(number, status, detail string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.updateStatus == nil {
		h.updateStatus = make(map[string]*UpdateStatusSnapshot)
	}
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
	if h.updateStatus == nil {
		return nil
	}
	return h.updateStatus[number]
}

// ClearUpdateStatus removes update status for a phone.
func (h *Hub) ClearUpdateStatus(number string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.updateStatus != nil {
		delete(h.updateStatus, number)
	}
}

// UpdateDeviceInfo sets version info for a connected phone under the write lock.
func (h *Hub) UpdateDeviceInfo(number, piVer, piCommit, fwVer, fwCommit string) bool {
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
	conn.LastSeen = time.Now()
	return true
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
