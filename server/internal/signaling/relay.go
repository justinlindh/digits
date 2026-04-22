package signaling

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/turn"
)

type CallTracker interface {
	OnCallInitiated(from, to string) (int64, error)
	OnCallAnswered(caller, callee string) error
	OnCallEnded(caller, callee string) error
	ClearByNumber(number string)
	InCall(a, b string) bool
	Busy(number string) bool
	PeerOf(number string) string
	CallIDFor(number string) (int64, bool)
}

// CallAuthorizer determines whether a call from one number to another is permitted.
type CallAuthorizer interface {
	CanCall(fromNumber, toNumber string) (bool, error)
}

// HealthRecorder is the subset of *calls.HealthStore used by Relay.
type HealthRecorder interface {
	Record(callID int64, endpoint string, sample calls.Sample)
}

type Relay struct {
	Hub            *Hub
	Tracker        CallTracker
	TURNGen        *turn.CredentialGenerator
	TURNDomain     string
	CallAuthorizer CallAuthorizer
	LineStore      LineStore
	HealthStore    HealthRecorder
}

func NewRelay(hub *Hub, tracker CallTracker, authorizer CallAuthorizer, lineStore LineStore) *Relay {
	return &Relay{
		Hub:            hub,
		Tracker:        tracker,
		CallAuthorizer: authorizer,
		LineStore:      lineStore,
	}
}

func (r *Relay) HandleMessage(from string, msg *Message) {
	msg.From = from
	slog.Debug("relay message", "from", from, "to", msg.To, "type", msg.Type)

	switch msg.Type {
	case TypeCall:
		r.handleCall(from, msg)
	case TypeSDP:
		r.forward(msg)
	case TypeICE:
		r.forward(msg)
	case TypeICERestart:
		r.handleICERestart(from, msg)
	case TypeAnswer:
		r.handleAnswer(from, msg)
	case TypeHangup:
		r.handleHangup(from, msg)
	case TypeDTMF:
		r.forward(msg)
	case TypeRequestICE:
		r.handleRequestICE(from, msg)
	case TypeDeviceInfo:
		if r.Hub.UpdateDeviceInfo(from, msg.PiVersion, msg.PiCommit, msg.FirmwareVersion, msg.FirmwareCommit, msg.FlashCapable) {
			slog.Info("device_info", "number", from,
				"pi_version", msg.PiVersion,
				"fw_version", msg.FirmwareVersion)
		}
		// If device reconnects after a rebooting update, mark it as success
		if status := r.Hub.GetUpdateStatus(from); status != nil && status.Status == "rebooting" {
			r.Hub.SetUpdateStatus(from, "success", "Updated to "+msg.PiVersion)
			slog.Info("update_status", "number", from, "status", "success",
				"detail", "device reconnected with "+msg.PiVersion)
		}
		return // No relay — server consumes this
	case TypeUpdateStatus:
		slog.Info("update_status", "number", from,
			"status", msg.UpdateStatus, "detail", msg.UpdateDetail)
		r.Hub.SetUpdateStatus(from, msg.UpdateStatus, msg.UpdateDetail)
		return // No relay — server consumes this
	case TypeRestart:
		return // Server → device only; ignore if echoed back
	case TypeLinkHealth:
		r.handleLinkHealth(from, msg)
		return
	default:
		slog.Warn("unknown message type", "type", msg.Type, "from", from)
	}
}

func (r *Relay) handleCall(from string, msg *Message) {
	target := r.Hub.Get(msg.To)
	if target == nil {
		_ = r.Hub.SendTo(from, &Message{Type: TypeError, Error: "phone not connected"})
		return
	}

	// Enforce call authorization
	if r.CallAuthorizer != nil {
		allowed, err := r.CallAuthorizer.CanCall(from, msg.To)
		if err != nil || !allowed {
			if err != nil {
				slog.Error("call authorization failed", "from", from, "to", msg.To, "err", err)
			}
			_ = r.Hub.SendTo(from, &Message{Type: TypeError, Error: "not_authorized"})
			return
		}
	}

	if r.Tracker != nil {
		if r.Tracker.Busy(from) || r.Tracker.Busy(msg.To) {
			_ = r.Hub.SendTo(from, &Message{Type: TypeBusy, From: msg.To})
			return
		}
		if _, err := r.Tracker.OnCallInitiated(from, msg.To); err != nil {
			slog.Error("failed to track call initiation", "err", err)
		}
	}

	_ = r.Hub.SendTo(msg.To, &Message{
		Type: TypeRing,
		From: from,
	})
}

func (r *Relay) handleICERestart(from string, msg *Message) {
	if r.Tracker != nil && !r.Tracker.InCall(from, msg.To) {
		slog.Warn("ice_restart without active call", "from", from, "to", msg.To)
		_ = r.Hub.SendTo(from, &Message{Type: TypeError, Error: "no active call"})
		return
	}
	r.forward(msg)
}

func (r *Relay) handleAnswer(from string, msg *Message) {
	if r.Tracker != nil {
		if err := r.Tracker.OnCallAnswered(msg.To, from); err != nil {
			slog.Error("failed to track call answer", "err", err)
		}
	}
	r.forward(msg)
}

func (r *Relay) handleHangup(from string, msg *Message) {
	// Resolve peer if client didn't specify a To field
	if msg.To == "" && r.Tracker != nil {
		if peer := r.Tracker.PeerOf(from); peer != "" {
			msg.To = peer
		}
	}
	if r.Tracker != nil {
		if err := r.Tracker.OnCallEnded(from, msg.To); err != nil {
			slog.Error("failed to track call end", "err", err)
		}
	}
	r.forward(msg)
}

func (r *Relay) handleRequestICE(from string, _ *Message) {
	resp := &Message{Type: TypeICEServers}

	// Always include a STUN server
	stunServer := ICEServer{
		URLs: []string{"stun:stun.l.google.com:19302"},
	}
	resp.Servers = append(resp.Servers, stunServer)

	// Add TURN servers if configured
	if r.TURNGen != nil && r.TURNDomain != "" {
		creds := r.TURNGen.Generate(from)
		turnServers := ICEServer{
			URLs: []string{
				fmt.Sprintf("turn:%s:3478?transport=udp", r.TURNDomain),
				fmt.Sprintf("turn:%s:3478?transport=tcp", r.TURNDomain),
				fmt.Sprintf("turns:%s:5349?transport=tcp", r.TURNDomain),
			},
			Username:   creds.Username,
			Credential: creds.Credential,
		}
		resp.Servers = append(resp.Servers, turnServers)
	}

	if err := r.Hub.SendTo(from, resp); err != nil {
		slog.Error("send ice-servers failed", "to", from, "err", err)
	}
}

// OnRegistered is called immediately after a device successfully registers.
// It pushes the current line settings to the device so it boots with the
// right voice style. Best-effort: unknown lines (unpaired) or send failures
// are logged and skipped -- the device keeps its locally-cached last setting.
func (r *Relay) OnRegistered(number string) {
	if r.LineStore == nil {
		return
	}
	settings, err := r.LineStore.LineSettingsByNumber(number)
	if err != nil {
		slog.Debug("line settings lookup on register skipped", "number", number, "err", err)
		return
	}
	if settings == nil {
		return
	}
	if err := r.Hub.SendTo(number, &Message{
		Type:         TypeLineSettings,
		To:           number,
		LineSettings: settings,
	}); err != nil {
		slog.Warn("push line settings on register failed", "number", number, "err", err)
	}
}

// OnDisconnect cleans up any active calls for a phone that disconnected.
func (r *Relay) OnDisconnect(number string) {
	if r.Tracker != nil {
		r.Tracker.ClearByNumber(number)
	}
}

func (r *Relay) forward(msg *Message) {
	if msg.To == "" {
		slog.Warn("no destination for message", "type", msg.Type, "from", msg.From)
		return
	}
	if err := r.Hub.SendTo(msg.To, msg); err != nil {
		slog.Error("forward failed", "to", msg.To, "err", err)
	}
}

// handleLinkHealth records a telemetry sample for the active call the
// session endpoint (from, derived from the authenticated websocket) is
// currently on. msg.From is ignored by design (forgery defense). Unknown
// calls and missing payloads are dropped silently.
func (r *Relay) handleLinkHealth(from string, msg *Message) {
	if r.HealthStore == nil || r.Tracker == nil || msg.LinkHealth == nil {
		return
	}
	callID, ok := r.Tracker.CallIDFor(from)
	if !ok {
		slog.Debug("link_health for endpoint not in active call", "endpoint", from)
		return
	}
	p := msg.LinkHealth
	r.HealthStore.Record(callID, from, calls.Sample{
		TS:       time.UnixMilli(p.TS),
		LossPct:  p.LossPct,
		JitterMs: p.JitterMs,
		RttMs:    p.RttMs,
		ConnType: p.ConnType,
		BytesIn:  p.BytesIn,
		BytesOut: p.BytesOut,
	})
}
