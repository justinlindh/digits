package signaling

import (
	"fmt"
	"log/slog"

	"github.com/justinlindh/digits/server/internal/turn"
)

type CallTracker interface {
	OnCallInitiated(from, to string) error
	OnCallAnswered(caller, callee string) error
	OnCallEnded(caller, callee string) error
}

// CallAuthorizer determines whether a call from one number to another is permitted.
type CallAuthorizer interface {
	CanCall(fromNumber, toNumber string) (bool, error)
}

type Relay struct {
	Hub          *Hub
	Tracker      CallTracker
	TURNGen      *turn.CredentialGenerator
	TURNDomain   string
	CallAuthorizer CallAuthorizer
}

func NewRelay(hub *Hub, tracker CallTracker, authorizer CallAuthorizer) *Relay {
	return &Relay{
		Hub:            hub,
		Tracker:        tracker,
		CallAuthorizer: authorizer,
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
		r.forward(msg)
	case TypeAnswer:
		r.handleAnswer(from, msg)
	case TypeHangup:
		r.handleHangup(from, msg)
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
	default:
		slog.Warn("unknown message type", "type", msg.Type, "from", from)
	}
}

func (r *Relay) handleCall(from string, msg *Message) {
	target := r.Hub.Get(msg.To)
	if target == nil {
		r.Hub.SendTo(from, &Message{Type: TypeError, Error: "phone not connected"})
		return
	}

	// Enforce call authorization
	if r.CallAuthorizer != nil {
		allowed, err := r.CallAuthorizer.CanCall(from, msg.To)
		if err == nil && !allowed {
			r.Hub.SendTo(from, &Message{Type: TypeError, Error: "not_authorized"})
			return
		}
	}

	if r.Tracker != nil {
		r.Tracker.OnCallInitiated(from, msg.To)
	}

	r.Hub.SendTo(msg.To, &Message{
		Type: TypeRing,
		From: from,
	})
}

func (r *Relay) handleAnswer(from string, msg *Message) {
	if r.Tracker != nil {
		r.Tracker.OnCallAnswered(msg.To, from)
	}
	r.forward(msg)
}

func (r *Relay) handleHangup(from string, msg *Message) {
	if r.Tracker != nil {
		r.Tracker.OnCallEnded(from, msg.To)
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

func (r *Relay) forward(msg *Message) {
	if msg.To == "" {
		slog.Warn("no destination for message", "type", msg.Type, "from", msg.From)
		return
	}
	if err := r.Hub.SendTo(msg.To, msg); err != nil {
		slog.Error("forward failed", "to", msg.To, "err", err)
	}
}
