package signaling

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/turn"
)

type CallTracker interface {
	OnCallInitiated(ctx context.Context, from, to string) (int64, error)
	OnCallAnswered(ctx context.Context, caller, callee string) error
	OnCallEnded(ctx context.Context, caller, callee string) error
	ClearByNumber(ctx context.Context, number string)
	InCall(ctx context.Context, a, b string) bool
	Busy(ctx context.Context, number string) bool
	CanAddAsHost(ctx context.Context, number string) bool
	PeerOf(ctx context.Context, number string) string
	AllPeersOf(ctx context.Context, number string) []string
	Conferences() *calls.ConferenceTracker
	CreateConferencePersistent(ctx context.Context, host string, originatingCallID int64, addedMembers []string) (*calls.Conference, error)
	CallIDForPair(ctx context.Context, a, b string) int64
	CallIDFor(ctx context.Context, number string) (int64, bool)
	EndConferencePersistent(ctx context.Context, confID uuid.UUID, reason string) error
	DropMemberPersistent(ctx context.Context, confID uuid.UUID, phone, reason string) (remaining []string, ended bool, err error)
}

// CallAuthorizer determines whether a call from one number to another is permitted.
type CallAuthorizer interface {
	CanCall(ctx context.Context, fromNumber, toNumber string) (bool, error)
}

// HealthRecorder is the subset of *calls.HealthStore used by Relay.
type HealthRecorder interface {
	Record(callID int64, endpoint string, sample calls.Sample)
	RecordEdge(confID uuid.UUID, from, peer string, sample calls.Sample)
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

func (r *Relay) HandleMessage(ctx context.Context, from string, msg *Message) {
	msg.From = from
	slog.Debug("relay message", "from", from, "to", msg.To, "type", msg.Type)

	switch msg.Type {
	case TypeCall:
		r.handleCall(ctx, from, msg)
	case TypeSDP:
		r.handleSDP(ctx, from, msg)
	case TypeICE:
		r.handleICE(ctx, from, msg)
	case TypeICERestart:
		r.handleICERestart(ctx, from, msg)
	case TypeAnswer:
		r.handleAnswer(ctx, from, msg)
	case TypeHangup:
		r.handleHangup(ctx, from, msg)
	case TypeConferenceMerge:
		r.handleConferenceMerge(ctx, from, msg)
	case TypeDTMF:
		r.forward(ctx, msg)
	case TypeRequestICE:
		r.handleRequestICE(ctx, from, msg)
	case TypeDeviceInfo:
		if r.Hub.UpdateDeviceInfo(from, msg.PiVersion, msg.PiCommit, msg.FirmwareVersion, msg.FirmwareCommit, msg.LocalAddr) {
			slog.Info("device_info", "number", from,
				"pi_version", msg.PiVersion,
				"fw_version", msg.FirmwareVersion,
				"local_addr", msg.LocalAddr)
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
		r.handleLinkHealth(ctx, from, msg)
		return
	default:
		slog.Warn("unknown message type", "type", msg.Type, "from", from)
	}
}

func (r *Relay) handleCall(ctx context.Context, from string, msg *Message) {
	target := r.Hub.Get(msg.To)
	if target == nil {
		_ = r.Hub.SendTo(from, &Message{Type: TypeError, Error: "phone not connected"})
		return
	}

	// Enforce call authorization
	if r.CallAuthorizer != nil {
		allowed, err := r.CallAuthorizer.CanCall(ctx, from, msg.To)
		if err != nil || !allowed {
			if err != nil {
				slog.Error("call authorization failed", "from", from, "to", msg.To, "err", err)
			}
			_ = r.Hub.SendTo(from, &Message{Type: TypeError, Error: "not_authorized"})
			return
		}
	}

	if r.Tracker != nil {
		// Callee must be idle. The caller is usually required to be idle too,
		// except for the party-line add-dial case: a host already in one
		// 2-party call (as caller) may initiate a second call to a third
		// party, which a subsequent conference_merge will bond into a 3-way.
		if r.Tracker.Busy(ctx, msg.To) {
			_ = r.Hub.SendTo(from, &Message{Type: TypeBusy, From: msg.To})
			return
		}
		if r.Tracker.Busy(ctx, from) && !r.Tracker.CanAddAsHost(ctx, from) {
			_ = r.Hub.SendTo(from, &Message{Type: TypeBusy, From: msg.To})
			return
		}
		if _, err := r.Tracker.OnCallInitiated(ctx, from, msg.To); err != nil {
			slog.Error("failed to track call initiation", "err", err)
		}
	}

	_ = r.Hub.SendTo(msg.To, &Message{
		Type: TypeRing,
		From: from,
	})
}

func (r *Relay) handleICERestart(ctx context.Context, from string, msg *Message) {
	if r.Tracker != nil && !r.Tracker.InCall(ctx, from, msg.To) {
		slog.Warn("ice_restart without active call", "from", from, "to", msg.To)
		_ = r.Hub.SendTo(from, &Message{Type: TypeError, Error: "no active call"})
		return
	}
	r.forward(ctx, msg)
}

func (r *Relay) handleAnswer(ctx context.Context, from string, msg *Message) {
	if r.Tracker != nil {
		if err := r.Tracker.OnCallAnswered(ctx, msg.To, from); err != nil {
			slog.Error("failed to track call answer", "err", err)
		}
	}
	r.forward(ctx, msg)
}

func (r *Relay) handleHangup(ctx context.Context, from string, msg *Message) {
	if r.Tracker != nil {
		if conf := r.Tracker.Conferences().ConferenceByPhone(from); conf != nil {
			if conf.Host == from {
				r.endConference(ctx, conf.ID, "host_hangup")
				return
			}
			r.dropMemberFromConference(ctx, conf.ID, from, "hangup")
			return
		}
		// NOTE: If a member previously left and a 2-party continuation call was
		// created (dropMemberFromConference ended the conference and left the
		// remaining two in the active-call map), the conference is already gone
		// by the time the host hangs up. ConferenceByPhone(from) returns nil here,
		// so we fall through to the normal 2-party hangup path below, which
		// correctly calls OnCallEnded and forwards Hangup to the peer. No special
		// handling needed.
	}
	// Resolve the set of peers to notify. In pre-merge ADD_* flows the host
	// may have multiple active 2-party calls (A-B held and A-C active); a
	// single hook-on ends both. For the normal 2-party case this is one peer.
	var peers []string
	if r.Tracker != nil {
		peers = r.Tracker.AllPeersOf(ctx, from)
	}
	if len(peers) == 0 && msg.To != "" {
		peers = []string{msg.To}
	}
	for _, peer := range peers {
		if r.Tracker != nil {
			if err := r.Tracker.OnCallEnded(ctx, from, peer); err != nil {
				slog.Error("failed to track call end", "err", err)
			}
		}
		_ = r.Hub.SendTo(peer, &Message{Type: TypeHangup, From: from, To: peer})
	}
}

func (r *Relay) handleSDP(ctx context.Context, from string, msg *Message) {
	if msg.ConfID != "" {
		id, err := uuid.Parse(msg.ConfID)
		if err == nil && r.Tracker != nil && r.Tracker.Conferences().ConferenceContains(id, from, msg.To) {
			_ = r.Hub.SendTo(msg.To, &Message{
				Type:   msg.Type,
				From:   from,
				To:     msg.To,
				ConfID: msg.ConfID,
				SDP:    msg.SDP,
			})
			return
		}
	}
	r.forward(ctx, msg)
}

func (r *Relay) handleICE(ctx context.Context, from string, msg *Message) {
	if msg.ConfID != "" {
		id, err := uuid.Parse(msg.ConfID)
		if err == nil && r.Tracker != nil && r.Tracker.Conferences().ConferenceContains(id, from, msg.To) {
			_ = r.Hub.SendTo(msg.To, &Message{
				Type:      msg.Type,
				From:      from,
				To:        msg.To,
				ConfID:    msg.ConfID,
				Candidate: msg.Candidate,
			})
			return
		}
	}
	r.forward(ctx, msg)
}

func (r *Relay) handleRequestICE(ctx context.Context, from string, _ *Message) {
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
// It pushes the current effective line settings (per-line settings already
// OR'd with the household DND flag) so the device boots with the right
// silent state. Best-effort: unknown lines (unpaired) or send failures are
// logged and skipped, and the device keeps its locally-cached last setting.
func (r *Relay) OnRegistered(ctx context.Context, number string) {
	if r.LineStore == nil {
		return
	}
	settings, err := r.LineStore.EffectiveLineSettings(ctx, number)
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

// OnDisconnect cleans up any active calls or conference membership for a
// phone that disconnected.
func (r *Relay) OnDisconnect(ctx context.Context, number string) {
	if r.Tracker == nil {
		return
	}
	// If the phone was in a conference, end the conference cleanly: persist
	// the end, notify remaining members. This runs BEFORE ClearByNumber so
	// the conference cleanup happens through the structured path.
	if conf := r.Tracker.Conferences().ConferenceByPhone(number); conf != nil {
		r.endConference(ctx, conf.ID, "disconnect")
	}
	r.Tracker.ClearByNumber(ctx, number)
}

func (r *Relay) forward(ctx context.Context, msg *Message) {
	if msg.To == "" {
		slog.Warn("no destination for message", "type", msg.Type, "from", msg.From)
		return
	}
	if err := r.Hub.SendTo(msg.To, msg); err != nil {
		slog.Error("forward failed", "to", msg.To, "err", err)
	}
}

// ForceHangup sends a TypeHangup message to both peers of a call. Intended
// for server-initiated teardown (e.g. the observation deck "End call" action).
// Errors are logged per-peer and do not stop delivery to the other peer.
func (r *Relay) ForceHangup(ctx context.Context, caller, callee string) {
	msg := &Message{Type: TypeHangup}
	if err := r.Hub.SendTo(caller, msg); err != nil {
		slog.Debug("ForceHangup: send to caller failed", "number", caller, "err", err)
	}
	if err := r.Hub.SendTo(callee, msg); err != nil {
		slog.Debug("ForceHangup: send to callee failed", "number", callee, "err", err)
	}
}

// handleLinkHealth records a telemetry sample. 2-party calls route through
// CallIDFor; 3-way conferences route through ConferenceByPhone with a
// co-membership guard to reject phantom edges from a rogue Pi. msg.From
// is ignored by design (forgery defense) - the authenticated from wins.
func (r *Relay) handleLinkHealth(ctx context.Context, from string, msg *Message) {
	if r.HealthStore == nil || r.Tracker == nil || msg.LinkHealth == nil {
		return
	}
	p := msg.LinkHealth

	sample := calls.Sample{
		TS:       time.UnixMilli(p.TS),
		LossPct:  p.LossPct,
		JitterMs: p.JitterMs,
		RttMs:    p.RttMs,
		ConnType: p.ConnType,
		BytesIn:  p.BytesIn,
		BytesOut: p.BytesOut,
	}

	if p.Peer == "" {
		callID, ok := r.Tracker.CallIDFor(ctx, from)
		if !ok {
			slog.Debug("link_health for endpoint not in active call", "endpoint", from)
			return
		}
		r.HealthStore.Record(callID, from, sample)
		return
	}

	ct := r.Tracker.Conferences()
	conf := ct.ConferenceByPhone(from)
	if conf == nil {
		slog.Debug("link_health peer set but endpoint not in an active conference",
			"endpoint", from, "peer", p.Peer)
		return
	}
	if !ct.ConferenceContains(conf.ID, from, p.Peer) {
		slog.Debug("link_health peer not a co-member (phantom edge, dropping)",
			"endpoint", from, "peer", p.Peer, "conf_id", conf.ID)
		return
	}
	r.HealthStore.RecordEdge(conf.ID, from, p.Peer, sample)
}
