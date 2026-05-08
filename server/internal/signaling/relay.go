package signaling

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
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
	InCall(a, b string) bool
	Busy(number string) bool
	CanAddAsHost(number string) bool
	PeerOf(number string) string
	AllPeersOf(number string) []string
	Conferences() *calls.ConferenceTracker
	CreateConferencePersistent(ctx context.Context, host string, originatingCallID int64, addedMembers []string) (*calls.Conference, error)
	CallIDForPair(a, b string) int64
	CallIDFor(number string) (int64, bool)
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

// SignalingErrorObserver counts signaling errors by category. Implemented
// by *metrics.Registry; the interface lives here so internal/signaling does
// not import internal/metrics directly. Categories are defined as untyped
// strings on this surface so the relay package stays independent; the
// metrics package validates them by exposing only a fixed set of constants.
type SignalingErrorObserver interface {
	ObserveSignalingErrorCategory(category string)
}

// activeExtension tracks a device that picked up an extension phone during
// an active call on its line. The extension device has its own WebRTC peer
// connection to the remote party, running in parallel with the original
// answering device's connection.
type activeExtension struct {
	HardwareID string // the picking-up device
	LineNumber string // the line the extension is on
	PeerNumber string // the remote party's line number
}

type Relay struct {
	Hub            *Hub
	Tracker        CallTracker
	TURNGen        *turn.CredentialGenerator
	TURNDomain     string
	CallAuthorizer CallAuthorizer
	LineStore      LineStore
	HealthStore    HealthRecorder
	// Errors is optional. When set, signaling-error counters are emitted
	// for the cases the relay can categorize cleanly (auth failed, peer
	// unreachable, etc). nil disables instrumentation; production wires it
	// in cmd/signald/main.go.
	Errors SignalingErrorObserver

	extMu      sync.Mutex
	extensions map[string]*activeExtension // hardware_id -> extension state
}

// observeError is a nil-safe pass-through to the SignalingErrorObserver.
// Centralizing it here means a missing observer never panics, and there is
// only one place to look when reviewing what categories the relay emits.
func (r *Relay) observeError(category string) {
	if r.Errors != nil {
		r.Errors.ObserveSignalingErrorCategory(category)
	}
}

func NewRelay(hub *Hub, tracker CallTracker, authorizer CallAuthorizer, lineStore LineStore) *Relay {
	return &Relay{
		Hub:            hub,
		Tracker:        tracker,
		CallAuthorizer: authorizer,
		LineStore:      lineStore,
		extensions:     make(map[string]*activeExtension),
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
	case TypeExtensionPickup:
		r.handleExtensionPickup(ctx, from, msg)
	case TypeDTMF:
		r.handleDTMF(ctx, from, msg)
	case TypeRequestICE:
		r.handleRequestICE(from)
	case TypeDeviceInfo:
		updated := false
		if msg.HardwareID != "" {
			updated = r.Hub.UpdateDeviceInfoByHardware(msg.HardwareID, msg.PiVersion, msg.PiCommit, msg.FirmwareVersion, msg.FirmwareCommit, msg.LocalAddr)
		} else {
			updated = r.Hub.UpdateDeviceInfo(from, msg.PiVersion, msg.PiCommit, msg.FirmwareVersion, msg.FirmwareCommit, msg.LocalAddr)
		}
		if updated {
			slog.Info("device_info", "number", from,
				"hardware_id", msg.HardwareID,
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
	if !r.Hub.IsOnline(msg.To) {
		r.observeError("peer_unreachable")
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
			r.observeError("auth_failed")
			_ = r.Hub.SendTo(from, &Message{Type: TypeError, Error: "not_authorized"})
			return
		}
	}

	if r.Tracker != nil {
		// Callee must be idle. The caller is usually required to be idle too,
		// except for the party-line add-dial case: a host already in one
		// 2-party call (as caller) may initiate a second call to a third
		// party, which a subsequent conference_merge will bond into a 3-way.
		if r.Tracker.Busy(msg.To) {
			_ = r.Hub.SendTo(from, &Message{Type: TypeBusy, From: msg.To})
			return
		}
		if r.Tracker.Busy(from) && !r.Tracker.CanAddAsHost(from) {
			_ = r.Hub.SendTo(from, &Message{Type: TypeBusy, From: msg.To})
			return
		}
		if _, err := r.Tracker.OnCallInitiated(ctx, from, msg.To); err != nil {
			slog.Error("failed to track call initiation", "err", err)
			r.observeError("call_setup_failed")
		}
	}

	_ = r.Hub.SendTo(msg.To, &Message{
		Type: TypeRing,
		From: from,
	})
}

// inCallOrConference returns true if from and to are in an active 2-party
// call or are co-members of the same conference. When the tracker is nil
// (tests), all traffic is allowed.
func (r *Relay) inCallOrConference(from, to string) bool {
	if r.Tracker == nil {
		return true
	}
	if r.Tracker.InCall(from, to) {
		return true
	}
	ct := r.Tracker.Conferences()
	if conf := ct.ConferenceByPhone(from); conf != nil {
		return ct.ConferenceContains(conf.ID, from, to)
	}
	return false
}

func (r *Relay) handleDTMF(ctx context.Context, from string, msg *Message) {
	if !r.inCallOrConference(from, msg.To) {
		slog.Warn("dtmf without active call", "from", from, "to", msg.To)
		r.observeError("invalid_message")
		return
	}
	r.forward(msg)
}

func (r *Relay) handleICERestart(ctx context.Context, from string, msg *Message) {
	if !r.inCallOrConference(from, msg.To) {
		slog.Warn("ice_restart without active call", "from", from, "to", msg.To)
		r.observeError("invalid_message")
		_ = r.Hub.SendTo(from, &Message{Type: TypeError, Error: "no active call"})
		return
	}
	r.forward(msg)
}

func (r *Relay) handleAnswer(ctx context.Context, from string, msg *Message) {
	if !r.inCallOrConference(from, msg.To) {
		slog.Warn("answer without active call", "from", from, "to", msg.To)
		r.observeError("invalid_message")
		return
	}

	// Cancel ringing on sibling devices BEFORE forwarding the answer so
	// a near-simultaneous second answer doesn't reach the caller.
	if msg.HardwareID != "" {
		cancelMsg := &Message{Type: TypeHangup, From: msg.To}
		for _, conn := range r.Hub.GetAll(from) {
			if conn.HardwareID != msg.HardwareID {
				_ = r.Hub.SendToHardware(conn.HardwareID, cancelMsg)
			}
		}
	}

	if r.Tracker != nil {
		if err := r.Tracker.OnCallAnswered(ctx, msg.To, from); err != nil {
			slog.Error("failed to track call answer", "err", err)
		}
	}
	r.forward(msg)
}

func (r *Relay) handleHangup(ctx context.Context, from string, msg *Message) {
	// If the hangup comes from an extension device, only tear down the
	// extension connection -- the primary call continues.
	if msg.HardwareID != "" {
		r.extMu.Lock()
		_, isExt := r.extensions[msg.HardwareID]
		r.extMu.Unlock()
		if isExt {
			r.clearExtension(msg.HardwareID)
			return
		}
	}

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
		peers = r.Tracker.AllPeersOf(from)
	}
	if len(peers) == 0 {
		slog.Debug("hangup from phone not in any active call", "from", from)
		return
	}
	for _, peer := range peers {
		if r.Tracker != nil {
			if err := r.Tracker.OnCallEnded(ctx, from, peer); err != nil {
				slog.Error("failed to track call end", "err", err)
			}
		}
		_ = r.Hub.SendTo(peer, &Message{Type: TypeHangup, From: from, To: peer})
	}
	r.clearExtensionsForCall(from)
}

func (r *Relay) handleSDP(ctx context.Context, from string, msg *Message) {
	if msg.Extension && r.routeExtensionSignaling(from, msg) {
		return
	}
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
	if !r.inCallOrConference(from, msg.To) {
		slog.Warn("sdp without active call", "from", from, "to", msg.To)
		r.observeError("invalid_message")
		return
	}
	r.forward(msg)
}

func (r *Relay) handleICE(ctx context.Context, from string, msg *Message) {
	if msg.Extension && r.routeExtensionSignaling(from, msg) {
		return
	}
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
	if !r.inCallOrConference(from, msg.To) {
		slog.Warn("ice without active call", "from", from, "to", msg.To)
		r.observeError("invalid_message")
		return
	}
	r.forward(msg)
}

func (r *Relay) handleRequestICE(from string) {
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
// phone that disconnected. With multiple devices per line, only tear down
// the call when the last device on that line disconnects. OnDisconnect
// runs before Unregister (LIFO defer order in handler_ws.go), so the
// departing conn is still counted; >1 means siblings remain.
func (r *Relay) OnDisconnect(ctx context.Context, number string, hardwareID string) {
	// Clear any extension state for this specific device, regardless of
	// whether other devices remain on the line.
	if hardwareID != "" {
		r.clearExtension(hardwareID)
	}

	if r.Tracker == nil {
		return
	}
	if r.Hub.ConnectionCount(number) > 1 {
		return
	}
	if conf := r.Tracker.Conferences().ConferenceByPhone(number); conf != nil {
		r.endConference(ctx, conf.ID, "disconnect")
	}
	r.Tracker.ClearByNumber(ctx, number)
	r.clearExtensionsForCall(number)
}

// handleExtensionPickup is the POTS extension model: a second device on the
// same line picks up during an active call. The server establishes a parallel
// WebRTC connection between the picking-up device and the remote peer,
// without disrupting the original answering device's connection.
func (r *Relay) handleExtensionPickup(ctx context.Context, from string, msg *Message) {
	if msg.HardwareID == "" {
		_ = r.Hub.SendTo(from, &Message{Type: TypeError, Error: "hardware_id required for extension pickup"})
		return
	}

	if r.Tracker == nil {
		_ = r.Hub.SendTo(from, &Message{Type: TypeError, Error: "no active call on this line"})
		return
	}

	peer := r.Tracker.PeerOf(from)
	if peer == "" {
		if conf := r.Tracker.Conferences().ConferenceByPhone(from); conf != nil {
			_ = r.Hub.SendTo(from, &Message{Type: TypeError, Error: "extension pickup not supported during conferences"})
			return
		}
		_ = r.Hub.SendToHardware(msg.HardwareID, &Message{Type: TypeError, Error: "no active call on this line"})
		return
	}

	r.extMu.Lock()
	if _, exists := r.extensions[msg.HardwareID]; exists {
		r.extMu.Unlock()
		return
	}
	r.extensions[msg.HardwareID] = &activeExtension{
		HardwareID: msg.HardwareID,
		LineNumber: from,
		PeerNumber: peer,
	}
	r.extMu.Unlock()

	slog.Info("extension pickup", "line", from, "hardware_id", msg.HardwareID, "peer", peer)

	_ = r.Hub.SendToHardware(msg.HardwareID, &Message{
		Type:      TypeExtensionConnect,
		Peer:      peer,
		Initiator: true,
	})

	_ = r.Hub.SendTo(peer, &Message{
		Type:      TypeExtensionConnect,
		Peer:      from,
		Initiator: false,
		Extension: true,
	})

	for _, conn := range r.Hub.GetAll(from) {
		if conn.HardwareID != msg.HardwareID {
			_ = r.Hub.SendToHardware(conn.HardwareID, &Message{
				Type:       TypeExtensionActive,
				HardwareID: msg.HardwareID,
			})
		}
	}
}

// routeExtensionSignaling routes SDP/ICE messages for extension connections.
// Extension signaling is identified by the Extension flag on the message.
// Returns true if the message was handled.
func (r *Relay) routeExtensionSignaling(from string, msg *Message) bool {
	r.extMu.Lock()
	ext := r.extensions[msg.HardwareID]
	r.extMu.Unlock()

	if ext != nil {
		if msg.To == ext.PeerNumber {
			_ = r.Hub.SendTo(ext.PeerNumber, msg)
			return true
		}
	}

	// The message might be from the remote peer going back to the extension device.
	// Find which extension expects traffic from this sender.
	r.extMu.Lock()
	for _, e := range r.extensions {
		if e.PeerNumber == from && e.LineNumber == msg.To {
			r.extMu.Unlock()
			_ = r.Hub.SendToHardware(e.HardwareID, msg)
			return true
		}
	}
	r.extMu.Unlock()
	return false
}

// clearExtension removes a device from the active extension set and notifies
// the remote peer that the extension has hung up. Called when the extension
// device sends a hangup or disconnects.
func (r *Relay) clearExtension(hardwareID string) {
	r.extMu.Lock()
	ext, ok := r.extensions[hardwareID]
	if ok {
		delete(r.extensions, hardwareID)
	}
	r.extMu.Unlock()
	if ok {
		_ = r.Hub.SendTo(ext.PeerNumber, &Message{
			Type:      TypeHangup,
			From:      ext.LineNumber,
			Extension: true,
		})
		slog.Info("extension cleared", "hardware_id", hardwareID, "line", ext.LineNumber, "peer", ext.PeerNumber)
	}
}

// clearExtensionsForCall removes all extensions on a line, called when the
// main call ends. Sends hangup to each extension device.
func (r *Relay) clearExtensionsForCall(lineNumber string) {
	r.extMu.Lock()
	var toRemove []string
	for hwID, ext := range r.extensions {
		if ext.LineNumber == lineNumber {
			toRemove = append(toRemove, hwID)
		}
	}
	for _, hwID := range toRemove {
		delete(r.extensions, hwID)
	}
	r.extMu.Unlock()

	for _, hwID := range toRemove {
		_ = r.Hub.SendToHardware(hwID, &Message{Type: TypeHangup, From: lineNumber})
		slog.Info("extension cleared (call ended)", "hardware_id", hwID, "line", lineNumber)
	}
}

func (r *Relay) forward(msg *Message) {
	if msg.To == "" {
		slog.Warn("no destination for message", "type", msg.Type, "from", msg.From)
		r.observeError("invalid_message")
		return
	}
	if err := r.Hub.SendTo(msg.To, msg); err != nil {
		slog.Error("forward failed", "to", msg.To, "err", err)
		r.observeError("relay_delivery")
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
		callID, ok := r.Tracker.CallIDFor(from)
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
