package signaling

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/justinlindh/digits/server/internal/calls"
	"github.com/justinlindh/digits/server/internal/turn"
)

var relayTracer = otel.Tracer("github.com/justinlindh/digits/server/internal/signaling")

const (
	// callReturnExpiry is how long a *69 busy-retry request remains active.
	callReturnExpiry = 30 * time.Minute
	// googleSTUN is the public STUN server included in every ICE-servers response.
	googleSTUN = "stun:stun.l.google.com:19302"
	// graceWindow is how long the server holds a 2-party call open after a
	// phone's signaling WebSocket drops, giving the phone time to reconnect
	// before the call is torn down. The Pi-side ICE recovery timeout
	// (iceRestartTimeout) must exceed this so a waiting peer does not give up
	// before a dropped phone can return.
	graceWindow = 20 * time.Second
)

// CallTracker is the subset of *calls.Tracker that the Relay needs to track
// call lifecycle events and query in-flight call state.
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
	DropMemberPersistent(ctx context.Context, confID uuid.UUID, phone, reason string) (remaining []string, err error)
	LastInboundCaller(ctx context.Context, number string) (string, error)
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

// MetricsObserver counts the events the relay can see as it routes signaling:
// categorized signaling errors, and media-negotiation events (which ICE
// candidate types and transports flow between peers, and whether ICE-server
// responses include TURN). Implemented by *metrics.Registry; the interface
// lives here so internal/signaling does not import internal/metrics directly.
// Categories and label values are untyped strings on this surface so the relay
// package stays independent; the metrics package validates them by exposing
// only a fixed set of constants, so a malformed value can never widen the
// label space. Media arguments are derived from the parsed candidate, never
// from raw user input, so no peer identity reaches a label.
type MetricsObserver interface {
	ObserveSignalingError(category string)
	ObserveICECandidate(candType, transport string)
	ObserveICEServersIssued(turn bool)
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

// pendingCallReturn tracks a *69 busy-retry request: the requester wants to
// be notified when target becomes free so it can ring back automatically.
type pendingCallReturn struct {
	Target    string
	ExpiresAt time.Time
}

// Relay routes signaling messages between connected devices. It sits above the
// Hub (which manages raw WebSocket connections) and owns call-lifecycle logic:
// ringing, answering, hanging up, DTMF, ICE restart, three-way merges, and
// extension pickup.
type Relay struct {
	Hub            *Hub
	Tracker        CallTracker
	TURNGen        *turn.CredentialGenerator
	TURNDomain     string
	CallAuthorizer CallAuthorizer
	LineStore      LineStore
	HealthStore    HealthRecorder
	// Metrics is optional. When set, the relay emits signaling-error counters
	// for the cases it can categorize cleanly (auth failed, peer unreachable,
	// etc) and media-negotiation counters (ICE candidate types/transports
	// relayed, ICE-server issuance). nil disables instrumentation; production
	// wires it in cmd/signald/main.go.
	Metrics MetricsObserver

	// GraceWindow is how long a 2-party call is held open after the last
	// device on a line disconnects, before teardown. Defaults to
	// graceWindow; overridable in tests. Must be set before the relay starts
	// handling messages; it is read without synchronization.
	GraceWindow time.Duration

	extMu      sync.Mutex
	extensions map[string]*activeExtension // hardware_id -> extension state

	pendingReturnsMu sync.Mutex
	pendingReturns   map[string]*pendingCallReturn // requester number -> pending retry

	graceMu     sync.Mutex
	graceTimers map[string]*graceEntry // key: graceKey(number, hardwareID)
}

// graceEntry holds a pending grace timer plus a cancel flag that closes the
// time.AfterFunc race: a fire that is already past the deadline but blocked
// on graceMu observes canceled == true (set by cancelGraceLocal under the
// same lock) and bails instead of tearing down a call that just reconnected.
type graceEntry struct {
	timer    *time.Timer
	canceled bool
}

func graceKey(number, hardwareID string) string {
	return number + "\x00" + hardwareID
}

// observeError is a nil-safe pass-through to the MetricsObserver.
// Centralizing it here means a missing observer never panics, and there is
// only one place to look when reviewing what categories the relay emits.
func (r *Relay) observeError(category string) {
	if r.Metrics != nil {
		r.Metrics.ObserveSignalingError(category)
	}
}

// lineAttrs resolves the line_id and household_id for a phone number and
// returns them as slog key-value pairs suitable for appending to a log call.
// Returns nil when the LineStore is unavailable or the number is not found,
// so callers can always append unconditionally.
func (r *Relay) lineAttrs(ctx context.Context, number string) []any {
	if r.LineStore == nil {
		return nil
	}
	lineID, householdID, err := r.LineStore.LineIdentifiers(ctx, number)
	if err != nil {
		return nil
	}
	return []any{"line_id", lineID, "household_id", householdID}
}

// setSpanCallID attaches signaling.call_id to the current span when known.
// HandleMessage starts the span before the call is resolved, so this fills
// in the attribute once the tracker has it. Listed as a relay-span
// attribute by the tracing package privacy contract.
func setSpanCallID(ctx context.Context, callID int64) {
	if callID == 0 {
		return
	}
	trace.SpanFromContext(ctx).SetAttributes(attribute.Int64("signaling.call_id", callID))
}

// NewRelay constructs a Relay wired to the given hub and tracker. TURN,
// HealthStore, and Errors are optional; set them on the returned struct before
// the server begins accepting connections.
func NewRelay(hub *Hub, tracker CallTracker, authorizer CallAuthorizer, lineStore LineStore) *Relay {
	return &Relay{
		Hub:            hub,
		Tracker:        tracker,
		CallAuthorizer: authorizer,
		LineStore:      lineStore,
		extensions:     make(map[string]*activeExtension),
		pendingReturns: make(map[string]*pendingCallReturn),
		GraceWindow:    graceWindow,
		graceTimers:    make(map[string]*graceEntry),
	}
}

func (r *Relay) HandleMessage(ctx context.Context, from string, msg *Message) {
	msg.From = from

	ctx, span := relayTracer.Start(ctx, "relay."+string(msg.Type),
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("signaling.type", string(msg.Type)),
			attribute.String("signaling.from", from),
			attribute.String("signaling.to", msg.To),
		),
	)
	defer span.End()

	slog.DebugContext(ctx, "relay message", "from", from, "to", msg.To, "type", msg.Type)

	switch msg.Type {
	case TypeCall:
		r.handleCall(ctx, from, msg)
	case TypeSDP, TypeICE:
		r.handleSignalingForward(ctx, from, msg)
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
		r.handleRequestICE(ctx, from, msg.HardwareID)
	case TypeDeviceInfo:
		// msg.HardwareID is always set: the WS handler stamps it from
		// conn.HardwareID and rejects registrations without a hardware_id.
		updated := r.Hub.UpdateDeviceInfoByHardware(msg.HardwareID, DeviceInfoParams{
			PiVersion:       msg.PiVersion,
			PiCommit:        msg.PiCommit,
			FirmwareVersion: msg.FirmwareVersion,
			FirmwareCommit:  msg.FirmwareCommit,
			RemoteAddr:      msg.LocalAddr,
			DevMode:         msg.DevMode,
		})
		if updated {
			slog.InfoContext(ctx, "device_info", "number", from,
				"hardware_id", msg.HardwareID,
				"pi_version", msg.PiVersion,
				"fw_version", msg.FirmwareVersion,
				"local_addr", msg.LocalAddr)
		}
		// If device reconnects after a rebooting update, mark it as success
		if status := r.Hub.GetUpdateStatus(msg.HardwareID); status != nil && status.Status == UpdateStatusRebooting {
			r.Hub.SetUpdateStatus(msg.HardwareID, UpdateStatusSuccess, "Updated to "+msg.PiVersion)
			slog.InfoContext(ctx, "update_status", "number", from, "hardware_id", msg.HardwareID,
				"status", UpdateStatusSuccess, "detail", "device reconnected with "+msg.PiVersion)
		}
		return // No relay: server consumes this
	case TypeUpdateStatus:
		slog.InfoContext(ctx, "update_status", "number", from, "hardware_id", msg.HardwareID,
			"status", msg.UpdateStatus, "detail", msg.UpdateDetail)
		r.Hub.SetUpdateStatus(msg.HardwareID, msg.UpdateStatus, msg.UpdateDetail)
		return // No relay: server consumes this
	case TypeRestart:
		return // Server to device only; ignore if echoed back
	case TypeLinkHealth:
		r.handleLinkHealth(ctx, from, msg)
		return
	case TypeCallReturn:
		r.handleCallReturn(ctx, from)
		return
	case TypeCallReturnRetry:
		r.handleCallReturnRetry(ctx, from, msg)
		return
	case TypeCallReturnCancel:
		r.handleCallReturnCancel(ctx, from)
		return
	case TypeVoicemailState:
		// Per-handset unheard-count snapshot. Hardware ID identifies which
		// handset on a multi-handset line emitted this; without it we have
		// no way to dedupe so we silently drop.
		if msg.HardwareID == "" {
			slog.WarnContext(ctx, "voicemail_state missing hardware_id", "number", from)
			return
		}
		slog.InfoContext(ctx, "voicemail_state", "number", from,
			"hardware_id", msg.HardwareID, "unheard_count", msg.VoicemailUnheardCount)
		r.Hub.SetVoicemailUnheard(from, msg.HardwareID, msg.VoicemailUnheardCount)
		return
	default:
		slog.WarnContext(ctx, "unknown message type", "type", msg.Type, "from", from)
	}
}

func (r *Relay) handleCall(ctx context.Context, from string, msg *Message) {
	if !r.Hub.IsOnline(msg.To) {
		// During the grace window a line's WebSocket is offline but its call
		// is still tracked (Busy == true). Return busy instead of
		// "phone not connected" so the caller gets the correct signal.
		// Dashboard/presence remains transport-truth; this only corrects
		// new-call routing to a grace-held line.
		if r.Tracker != nil && r.Tracker.Busy(ctx, msg.To) {
			_ = r.Hub.SendTo(from, &Message{Type: TypeBusy, From: msg.To})
			return
		}
		// Dialing a phone that is offline or unregistered is normal user
		// behavior, not a signaling fault: just tell the caller it didn't
		// connect. It must not count toward signaling_errors_total.
		_ = r.Hub.SendTo(from, &Message{Type: TypeError, Error: "phone not connected"})
		return
	}

	// Enforce call authorization
	if r.CallAuthorizer != nil {
		allowed, err := r.CallAuthorizer.CanCall(ctx, from, msg.To)
		if err != nil || !allowed {
			if err != nil {
				slog.ErrorContext(ctx, "call authorization failed", "from", from, "to", msg.To, "err", err)
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
		if r.Tracker.Busy(ctx, msg.To) {
			_ = r.Hub.SendTo(from, &Message{Type: TypeBusy, From: msg.To})
			return
		}
		if r.Tracker.Busy(ctx, from) && !r.Tracker.CanAddAsHost(ctx, from) {
			_ = r.Hub.SendTo(from, &Message{Type: TypeBusy, From: msg.To})
			return
		}
		callID, err := r.Tracker.OnCallInitiated(ctx, from, msg.To)
		if err != nil {
			slog.ErrorContext(ctx, "failed to track call initiation", "err", err)
			r.observeError("call_setup_failed")
		} else {
			attrs := []any{"call_id", callID, "from", from, "to", msg.To, "hardware_id", msg.HardwareID}
			attrs = append(attrs, r.lineAttrs(ctx, from)...)
			slog.InfoContext(ctx, "call initiated", attrs...)
			setSpanCallID(ctx, callID)
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
func (r *Relay) inCallOrConference(ctx context.Context, from, to string) bool {
	if r.Tracker == nil {
		return true
	}
	if r.Tracker.InCall(ctx, from, to) {
		return true
	}
	ct := r.Tracker.Conferences()
	if conf := ct.ConferenceByPhone(ctx, from); conf != nil {
		return ct.ConferenceContains(ctx, conf.ID, from, to)
	}
	return false
}

// rejectNoDest reports a relay message that names no destination and returns
// true if the caller should stop. An empty To is a malformed message
// (invalid_message): a genuine fault, distinct from a valid destination with
// no active call. It is checked before the active-call guard so it is counted
// rather than collapsing into that guard's benign drop.
func (r *Relay) rejectNoDest(ctx context.Context, from string, msg *Message) bool {
	if msg.To != "" {
		return false
	}
	slog.WarnContext(ctx, "no destination for message", "type", msg.Type, "from", from)
	r.observeError("invalid_message")
	return true
}

func (r *Relay) handleDTMF(ctx context.Context, from string, msg *Message) {
	if r.rejectNoDest(ctx, from, msg) {
		return
	}
	if !r.inCallOrConference(ctx, from, msg.To) {
		// Stray control/media for a call that isn't active (raced past teardown,
		// or trailing a dial to an unreachable number) is normal, not an error.
		slog.DebugContext(ctx, "dtmf without active call", "from", from, "to", msg.To)
		return
	}
	r.forward(ctx, msg)
}

// iceRestartDeliveryTimeout is how long handleICERestart waits for the
// peer's send buffer to accept the offer before giving up. Losing the
// ICE-restart offer during recovery stalls reconnection into a hangup, so a
// short bounded retry is preferable to the silent drop that SendTo's
// best-effort path would apply. When the peer has no local connection the
// send falls back to Redis for cross-pod delivery, like SendTo.
const iceRestartDeliveryTimeout = 2 * time.Second

func (r *Relay) handleICERestart(ctx context.Context, from string, msg *Message) {
	if r.rejectNoDest(ctx, from, msg) {
		return
	}
	if !r.inCallOrConference(ctx, from, msg.To) {
		// Recovery raced past call teardown: tell the client there's no call to
		// restart, but don't count it as a signaling fault.
		slog.DebugContext(ctx, "ice_restart without active call", "from", from, "to", msg.To)
		_ = r.Hub.SendTo(from, &Message{Type: TypeError, Error: "no active call"})
		return
	}
	// Bounded retry so the offer is not silently dropped when the peer's send
	// buffer is temporarily full during recovery. SendToWithTimeout falls back
	// to Redis when the peer has no local connection (cross-pod delivery).
	if err := r.Hub.SendToWithTimeout(msg.To, msg, iceRestartDeliveryTimeout); err != nil {
		slog.ErrorContext(ctx, "ice_restart delivery failed", "to", msg.To, "err", err)
		r.observeError("relay_delivery")
	}
}

func (r *Relay) handleAnswer(ctx context.Context, from string, msg *Message) {
	if r.rejectNoDest(ctx, from, msg) {
		return
	}
	if !r.inCallOrConference(ctx, from, msg.To) {
		// Answer landing after the caller already hung up is a benign race, not
		// an error.
		slog.DebugContext(ctx, "answer without active call", "from", from, "to", msg.To)
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

	var callID int64
	if r.Tracker != nil {
		if err := r.Tracker.OnCallAnswered(ctx, msg.To, from); err != nil {
			slog.ErrorContext(ctx, "failed to track call answer", "err", err)
		}
		if callID = r.Tracker.CallIDForPair(ctx, msg.To, from); callID != 0 {
			attrs := []any{"call_id", callID, "from", msg.To, "to", from, "hardware_id", msg.HardwareID}
			attrs = append(attrs, r.lineAttrs(ctx, from)...)
			slog.InfoContext(ctx, "call answered", attrs...)
			setSpanCallID(ctx, callID)
		}
	}
	// Log the answer SDP relay with the same shape summary as the offer path.
	if msg.SDP != "" {
		r.logSDPRelay(ctx, from, msg, callID, msg.ConfID != "")
	}
	r.forward(ctx, msg)
}

func (r *Relay) handleHangup(ctx context.Context, from string, msg *Message) {
	// If the hangup comes from an extension device, only tear down the
	// extension connection -- the primary call continues.
	if msg.HardwareID != "" {
		r.extMu.Lock()
		_, isExt := r.extensions[msg.HardwareID]
		r.extMu.Unlock()
		if isExt {
			r.clearExtension(ctx, msg.HardwareID)
			return
		}
	}

	if r.Tracker != nil {
		if conf := r.Tracker.Conferences().ConferenceByPhone(ctx, from); conf != nil {
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
	r.endActiveCallsAsHangup(ctx, from)
}

// endActiveCallsAsHangup ends every active 2-party call involving number the
// same way an explicit hangup does: it records each call end (DB persistence
// plus the OnCallEndedNotify observer that drives *69 retries) and forwards a
// Hangup to each peer, then clears extension state. Shared by the hangup
// handler and the grace-window expiry path so the two cannot drift.
func (r *Relay) endActiveCallsAsHangup(ctx context.Context, number string) {
	if r.Tracker == nil {
		return
	}
	peers := r.Tracker.AllPeersOf(ctx, number)
	if len(peers) == 0 {
		slog.DebugContext(ctx, "end calls: phone not in any active call", "number", number)
		return
	}
	for _, peer := range peers {
		callID := r.Tracker.CallIDForPair(ctx, number, peer)
		if err := r.Tracker.OnCallEnded(ctx, number, peer); err != nil {
			slog.ErrorContext(ctx, "failed to track call end", "err", err)
		}
		if callID != 0 {
			attrs := []any{"call_id", callID, "from", number, "to", peer}
			attrs = append(attrs, r.lineAttrs(ctx, number)...)
			slog.InfoContext(ctx, "call ended", attrs...)
			setSpanCallID(ctx, callID)
		}
		_ = r.Hub.SendTo(peer, &Message{Type: TypeHangup, From: number, To: peer})
	}
	r.clearExtensionsForCall(ctx, number)
}

// handleSignalingForward relays an SDP or ICE message between in-call peers.
// msg.Type ("sdp" or "ice") is used verbatim in log lines. The conference
// fast path copies both payload fields; the one not set for this type is
// empty and omitted from the encoded message, so the wire format per type
// is unchanged.
func (r *Relay) handleSignalingForward(ctx context.Context, from string, msg *Message) {
	if r.rejectNoDest(ctx, from, msg) {
		return
	}
	if msg.Extension && r.routeExtensionSignaling(ctx, from, msg) {
		return
	}
	if msg.ConfID != "" {
		id, err := uuid.Parse(msg.ConfID)
		if err == nil && r.Tracker != nil && r.Tracker.Conferences().ConferenceContains(ctx, id, from, msg.To) {
			var confCallID int64
			if confCallID = r.Tracker.CallIDForPair(ctx, from, msg.To); confCallID != 0 {
				setSpanCallID(ctx, confCallID)
			}
			r.logSignalingRelay(ctx, from, msg, confCallID, true)
			_ = r.Hub.SendTo(msg.To, &Message{
				Type:      msg.Type,
				From:      from,
				To:        msg.To,
				ConfID:    msg.ConfID,
				SDP:       msg.SDP,
				Candidate: msg.Candidate,
			})
			return
		}
	}
	if !r.inCallOrConference(ctx, from, msg.To) {
		// SDP/ICE for a call that isn't active: the peer was never reachable
		// (dialed an offline or unregistered number) or the call already ended
		// and candidates are still trickling in. Both are normal, not errors.
		slog.DebugContext(ctx, "signaling without active call", "type", msg.Type, "from", from, "to", msg.To)
		return
	}
	var callID int64
	if r.Tracker != nil {
		if callID = r.Tracker.CallIDForPair(ctx, from, msg.To); callID != 0 {
			setSpanCallID(ctx, callID)
		}
	}
	r.logSignalingRelay(ctx, from, msg, callID, false)
	r.forward(ctx, msg)
}

// logSignalingRelay emits structured observability for an SDP or ICE message
// as it is relayed between peers. ICE candidates are logged at Debug (one line
// per trickled candidate) with the parsed candidate type, transport, and
// address:port, so an operator can see whether a phone is offering host,
// srflx, or relay candidates without decoding the raw SDP attribute by hand.
// SDP offers/answers are logged at Info with a content-free shape summary
// (media-section and bundled-candidate counts, body size); the SDP body is
// never logged because it carries DTLS fingerprints and ICE credentials.
//
// from/to/hardware_id/call_id are attached to every line so a relay event can
// be attributed to a specific joined device on a multi-device line, not just
// the line number. conf is true on the conference fast path; it adds conf_id.
func (r *Relay) logSignalingRelay(ctx context.Context, from string, msg *Message, callID int64, conf bool) {
	switch msg.Type {
	case TypeICE:
		// Empty Candidate is the end-of-candidates marker: nothing to count or
		// log. Bail before any parsing on this per-trickled-candidate hot path.
		if strings.TrimSpace(msg.Candidate) == "" {
			return
		}
		debug := slog.Default().Enabled(ctx, slog.LevelDebug)
		if r.Metrics == nil && !debug {
			return
		}
		// Parse once: the metric needs the type/transport, and so does the log.
		cand := ParseCandidate(msg.Candidate)
		if r.Metrics != nil {
			// Count every non-empty candidate. An unparseable one carries empty
			// type/transport, which the metric's label allowlist collapses to
			// other/other, so a rising malformed-candidate rate shows up on a
			// dashboard, not only in Debug logs that are off by default.
			r.Metrics.ObserveICECandidate(cand.Type, cand.Transport)
		}
		if !debug {
			return
		}
		attrs := []any{
			"from", from,
			"to", msg.To,
			"hardware_id", msg.HardwareID,
		}
		if callID != 0 {
			attrs = append(attrs, "call_id", callID)
		}
		if conf {
			attrs = append(attrs, "conf_id", msg.ConfID)
		}
		if cand.Parsed() {
			attrs = append(attrs,
				"cand_type", cand.Type,
				"transport", cand.Transport,
				"address", cand.Address,
				"port", cand.Port)
			if cand.RelatedAddress != "" {
				attrs = append(attrs, "raddr", cand.RelatedAddress, "rport", cand.RelatedPort)
			}
			slog.DebugContext(ctx, "ice candidate relayed", attrs...)
		} else {
			// A non-empty but unparseable line is worth surfacing so a
			// malformed-candidate bug is not silently relayed.
			slog.DebugContext(ctx, "ice candidate relayed (unparsed)", attrs...)
		}
	case TypeSDP:
		r.logSDPRelay(ctx, from, msg, callID, conf)
	}
}

// logSDPRelay logs an SDP offer or answer at Info with a content-free shape
// summary. Shared by the offer path (handleSignalingForward via
// logSignalingRelay) and the answer path (handleAnswer), so both carry the
// same from/to/hardware_id/call_id attribution and the SDP body is never
// logged.
func (r *Relay) logSDPRelay(ctx context.Context, from string, msg *Message, callID int64, conf bool) {
	sum := SummarizeSDP(msg.SDP)
	// An offer arrives as a "sdp" message and an answer as an "answer" message;
	// normalize the wire type to the SDP role so offer/answer pairs read
	// symmetrically.
	kind := msg.Type
	if kind == TypeSDP {
		kind = "offer"
	}
	attrs := []any{
		"from", from,
		"to", msg.To,
		"hardware_id", msg.HardwareID,
		"sdp_kind", kind,
		"m_lines", sum.MLines,
		"embedded_candidates", sum.Candidates,
		"sdp_bytes", sum.Bytes,
	}
	if callID != 0 {
		attrs = append(attrs, "call_id", callID)
	}
	if conf {
		attrs = append(attrs, "conf_id", msg.ConfID)
	}
	slog.InfoContext(ctx, "sdp relayed", attrs...)
}

func (r *Relay) handleRequestICE(ctx context.Context, from, hardwareID string) {
	resp := &Message{Type: TypeICEServers}

	// Always include a STUN server
	stunServer := ICEServer{
		URLs: []string{googleSTUN},
	}
	resp.Servers = append(resp.Servers, stunServer)

	turnOffered := false
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
		turnOffered = true
	}

	// Log whether TURN was issued (never the TURN username or credential). STUN
	// is always included, so it carries no signal; turn=false is the one that
	// matters, flagging a misconfigured pod handing out STUN only.
	slog.InfoContext(ctx, "ice servers issued", "number", from, "hardware_id", hardwareID, "turn", turnOffered)
	if r.Metrics != nil {
		r.Metrics.ObserveICEServersIssued(turnOffered)
	}

	if err := r.Hub.SendTo(from, resp); err != nil {
		slog.ErrorContext(ctx, "send ice-servers failed", "to", from, "err", err)
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
		slog.DebugContext(ctx, "line settings lookup on register skipped", "number", number, "err", err)
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
		slog.WarnContext(ctx, "push line settings on register failed", "number", number, "err", err)
	}
}

// OnConnClosed is the disconnect entry point for a websocket read loop. It
// runs OnDisconnect only when conn is still the hub's current connection for
// its line. A device that reconnects with the same hardware_id replaces its
// previous conn in place, and the new conn's OnReconnect runs before the old
// conn's read loop unwinds. Without this guard the old loop would call
// OnDisconnect, see the (new) sibling as the sole remaining connection, and
// start a grace timer that nothing cancels, tearing the reconnected call down
// when the window expires.
//
// The guard is a snapshot, not a lock: a reconnect can land between this
// check and startGraceTimer arming, in which case OnReconnect's cancel finds
// no timer and the orphaned timer survives anyway. The expiry callback
// therefore rechecks live presence (Hub.HardwareOnlineOnLine) before tearing
// down.
func (r *Relay) OnConnClosed(ctx context.Context, conn *Conn) {
	if conn == nil {
		return
	}
	if !r.Hub.ConnIsCurrent(conn) {
		return
	}
	r.OnDisconnect(ctx, conn.Number, conn.HardwareID)
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
		r.clearExtension(ctx, hardwareID)
	}

	if r.Tracker == nil {
		return
	}
	// Other devices remain on the line: the call is still held by a sibling.
	if r.Hub.ConnectionCount(number) > 1 {
		return
	}
	// Conferences are out of scope for the grace window: tear down now.
	if conf := r.Tracker.Conferences().ConferenceByPhone(ctx, number); conf != nil {
		r.endConference(ctx, conf.ID, "disconnect")
		r.Tracker.ClearByNumber(ctx, number)
		r.clearExtensionsForCall(ctx, number)
		return
	}
	// Active 2-party call: hold it open through a reconnect grace window
	// instead of tearing down immediately. The peer is NOT notified yet.
	if peer := r.Tracker.PeerOf(ctx, number); peer != "" {
		r.startGraceTimer(number, hardwareID, peer)
		return
	}
	// Not in a call: nothing to hold; clear (no-op for an idle line).
	r.Tracker.ClearByNumber(ctx, number)
	r.clearExtensionsForCall(ctx, number)
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

	peer := r.Tracker.PeerOf(ctx, from)
	if peer == "" {
		if conf := r.Tracker.Conferences().ConferenceByPhone(ctx, from); conf != nil {
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

	pickupAttrs := []any{"line", from, "hardware_id", msg.HardwareID, "peer", peer}
	if callID := r.Tracker.CallIDForPair(ctx, from, peer); callID != 0 {
		pickupAttrs = append(pickupAttrs, "call_id", callID)
		setSpanCallID(ctx, callID)
	}
	pickupAttrs = append(pickupAttrs, r.lineAttrs(ctx, from)...)
	slog.InfoContext(ctx, "extension pickup", pickupAttrs...)

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
func (r *Relay) routeExtensionSignaling(ctx context.Context, from string, msg *Message) bool {
	// Resolve the target in one critical section, then send after unlock.
	var toPeer string
	var toHardware string
	r.extMu.Lock()
	if ext := r.extensions[msg.HardwareID]; ext != nil && msg.To == ext.PeerNumber {
		toPeer = ext.PeerNumber
	} else {
		// The message might be from the remote peer going back to the extension
		// device. Find which extension expects traffic from this sender.
		for _, e := range r.extensions {
			if e.PeerNumber == from && e.LineNumber == msg.To {
				toHardware = e.HardwareID
				break
			}
		}
	}
	r.extMu.Unlock()

	if toPeer == "" && toHardware == "" {
		return false
	}

	// Extension legs carry their own SDP/ICE; log them like any other relayed
	// signaling so a struggling extension media path is observable too.
	var callID int64
	if r.Tracker != nil {
		if callID = r.Tracker.CallIDForPair(ctx, from, msg.To); callID != 0 {
			setSpanCallID(ctx, callID)
		}
	}
	r.logSignalingRelay(ctx, from, msg, callID, false)

	if toPeer != "" {
		_ = r.Hub.SendTo(toPeer, msg)
	} else {
		_ = r.Hub.SendToHardware(toHardware, msg)
	}
	return true
}

// clearExtension removes a device from the active extension set and notifies
// the remote peer that the extension has hung up. Called when the extension
// device sends a hangup or disconnects.
func (r *Relay) clearExtension(ctx context.Context, hardwareID string) {
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
		attrs := []any{"hardware_id", hardwareID, "line", ext.LineNumber, "peer", ext.PeerNumber}
		if r.Tracker != nil {
			if callID := r.Tracker.CallIDForPair(ctx, ext.LineNumber, ext.PeerNumber); callID != 0 {
				attrs = append(attrs, "call_id", callID)
				setSpanCallID(ctx, callID)
			}
		}
		attrs = append(attrs, r.lineAttrs(ctx, ext.LineNumber)...)
		slog.InfoContext(ctx, "extension cleared", attrs...)
	}
}

// clearExtensionsForCall removes all extensions on a line, called when the
// main call ends. Sends hangup to each extension device. Note: callers
// invoke this after the tracker has already cleared the call, so call_id
// is captured before the extension is removed and may already be zero.
func (r *Relay) clearExtensionsForCall(ctx context.Context, lineNumber string) {
	r.extMu.Lock()
	type cleared struct {
		hwID string
		peer string
	}
	var toRemove []cleared
	for hwID, ext := range r.extensions {
		if ext.LineNumber == lineNumber {
			toRemove = append(toRemove, cleared{hwID: hwID, peer: ext.PeerNumber})
		}
	}
	for _, c := range toRemove {
		delete(r.extensions, c.hwID)
	}
	r.extMu.Unlock()

	lineAttrs := r.lineAttrs(ctx, lineNumber)
	for _, c := range toRemove {
		_ = r.Hub.SendToHardware(c.hwID, &Message{Type: TypeHangup, From: lineNumber})
		attrs := []any{"hardware_id", c.hwID, "line", lineNumber, "peer", c.peer}
		if r.Tracker != nil {
			if callID := r.Tracker.CallIDForPair(ctx, lineNumber, c.peer); callID != 0 {
				attrs = append(attrs, "call_id", callID)
				setSpanCallID(ctx, callID)
			}
		}
		attrs = append(attrs, lineAttrs...)
		slog.InfoContext(ctx, "extension cleared (call ended)", attrs...)
	}
}

// startGraceTimer holds a 2-party call open for GraceWindow after the last
// device on `number` disconnects. If the device re-registers within the
// window (cancelGraceLocal), the call survives. Otherwise the call is torn
// down and `peer` receives an explicit hangup.
func (r *Relay) startGraceTimer(number, hardwareID, peer string) {
	key := graceKey(number, hardwareID)
	r.graceMu.Lock()
	if old, ok := r.graceTimers[key]; ok {
		old.canceled = true
		old.timer.Stop()
	}
	entry := &graceEntry{}
	entry.timer = time.AfterFunc(r.GraceWindow, func() {
		r.graceMu.Lock()
		if entry.canceled {
			r.graceMu.Unlock()
			return
		}
		delete(r.graceTimers, key)
		r.graceMu.Unlock()

		ctx := context.Background()
		// Fire-time recheck: a reconnect can race the timer arm and find no
		// timer to cancel (see OnConnClosed); hub state is authoritative.
		if r.Hub.HardwareOnlineOnLine(number, hardwareID) {
			slog.InfoContext(ctx, "grace: device online again at expiry, keeping call", "number", number, "peer", peer)
			return
		}
		slog.InfoContext(ctx, "grace: window expired, tearing down call", "number", number, "peer", peer)
		r.endActiveCallsAsHangup(ctx, number)
	})
	r.graceTimers[key] = entry
	r.graceMu.Unlock()
	slog.InfoContext(context.Background(), "grace: holding call through reconnect window", "number", number, "peer", peer, "window", r.GraceWindow)
}

// cancelGraceLocal stops a pending grace timer held by THIS pod. Returns
// true if a live timer was found and canceled. Does not publish anything.
func (r *Relay) cancelGraceLocal(number, hardwareID string) bool {
	key := graceKey(number, hardwareID)
	r.graceMu.Lock()
	defer r.graceMu.Unlock()
	entry, ok := r.graceTimers[key]
	if !ok {
		return false
	}
	entry.canceled = true
	entry.timer.Stop()
	delete(r.graceTimers, key)
	slog.InfoContext(context.Background(), "grace: canceled by reconnect", "number", number)
	return true
}

// OnReconnect is called when a paired device re-registers. It cancels any
// grace timer held locally and broadcasts the reconnect so a sibling pod
// holding the timer cancels too.
func (r *Relay) OnReconnect(ctx context.Context, number, hardwareID string) {
	r.cancelGraceLocal(number, hardwareID)
	r.Hub.PublishReconnect(number, hardwareID)
}

// HandleRemoteReconnect is invoked from the Redis reconnect dispatch. It
// cancels a locally held grace timer only; it never re-publishes.
func (r *Relay) HandleRemoteReconnect(number, hardwareID string) {
	r.cancelGraceLocal(number, hardwareID)
}

// forward sends msg to msg.To. Callers must reject an empty destination first
// (see rejectNoDest); forward assumes a non-empty To.
func (r *Relay) forward(ctx context.Context, msg *Message) {
	if err := r.Hub.SendTo(msg.To, msg); err != nil {
		slog.ErrorContext(ctx, "forward failed", "to", msg.To, "err", err)
		r.observeError("relay_delivery")
	}
}

// ForceHangup sends a TypeHangup message to both peers of a call. Intended
// for server-initiated teardown (e.g. the observation deck "End call" action).
// Errors are logged per-peer and do not stop delivery to the other peer.
func (r *Relay) ForceHangup(ctx context.Context, caller, callee string) {
	msg := &Message{Type: TypeHangup}
	if err := r.Hub.SendTo(caller, msg); err != nil {
		slog.DebugContext(ctx, "ForceHangup: send to caller failed", "number", caller, "err", err)
	}
	if err := r.Hub.SendTo(callee, msg); err != nil {
		slog.DebugContext(ctx, "ForceHangup: send to callee failed", "number", callee, "err", err)
	}
}

func (r *Relay) handleCallReturn(ctx context.Context, from string) {
	var number string
	if r.Tracker != nil {
		var err error
		number, err = r.Tracker.LastInboundCaller(ctx, from)
		if err != nil {
			slog.ErrorContext(ctx, "call_return query failed", "from", from, "err", err)
		}
	}
	_ = r.Hub.SendTo(from, &Message{Type: TypeCallReturnResult, Number: number})
}

func (r *Relay) handleCallReturnRetry(ctx context.Context, from string, msg *Message) {
	target := msg.Number
	if target == "" {
		return
	}
	r.pendingReturnsMu.Lock()
	r.pendingReturns[from] = &pendingCallReturn{
		Target:    target,
		ExpiresAt: time.Now().Add(callReturnExpiry),
	}
	r.pendingReturnsMu.Unlock()
	slog.InfoContext(ctx, "call_return: retry registered", "requester", from, "target", target)
	r.checkPendingReturn(ctx, from)
}

func (r *Relay) handleCallReturnCancel(ctx context.Context, from string) {
	r.pendingReturnsMu.Lock()
	_, had := r.pendingReturns[from]
	delete(r.pendingReturns, from)
	r.pendingReturnsMu.Unlock()
	if had {
		slog.InfoContext(ctx, "call_return: retry cancelled", "requester", from)
	}
	_ = r.Hub.SendTo(from, &Message{Type: TypeCallReturnCancelled})
}

func (r *Relay) checkPendingReturn(ctx context.Context, requester string) {
	r.pendingReturnsMu.Lock()
	pending, ok := r.pendingReturns[requester]
	if !ok {
		r.pendingReturnsMu.Unlock()
		return
	}
	if time.Now().After(pending.ExpiresAt) {
		delete(r.pendingReturns, requester)
		r.pendingReturnsMu.Unlock()
		return
	}
	target := pending.Target
	r.pendingReturnsMu.Unlock()

	if r.Tracker != nil && !r.Tracker.Busy(ctx, target) && !r.Tracker.Busy(ctx, requester) &&
		r.Hub.IsOnline(requester) && r.Hub.IsOnline(target) {
		r.pendingReturnsMu.Lock()
		_, stillPending := r.pendingReturns[requester]
		if stillPending {
			delete(r.pendingReturns, requester)
		}
		r.pendingReturnsMu.Unlock()
		if !stillPending {
			return
		}
		slog.InfoContext(ctx, "call_return: target free, ringing requester", "requester", requester, "target", target)
		_ = r.Hub.SendTo(requester, &Message{Type: TypeCallReturnRing, Number: target})
	}
}

// OnCallEndedNotify is called by the tracker when a 2-party call ends.
// It checks if any pending call-return retries should now fire.
func (r *Relay) OnCallEndedNotify(ctx context.Context, caller, callee string) {
	r.pendingReturnsMu.Lock()
	var toCheck []string
	for requester, pending := range r.pendingReturns {
		if time.Now().After(pending.ExpiresAt) {
			delete(r.pendingReturns, requester)
			continue
		}
		if pending.Target == caller || pending.Target == callee ||
			requester == caller || requester == callee {
			toCheck = append(toCheck, requester)
		}
	}
	r.pendingReturnsMu.Unlock()

	for _, requester := range toCheck {
		r.checkPendingReturn(ctx, requester)
	}

	if callee == "" {
		r.pendingReturnsMu.Lock()
		delete(r.pendingReturns, caller)
		r.pendingReturnsMu.Unlock()
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
			slog.DebugContext(ctx, "link_health for endpoint not in active call", "endpoint", from)
			return
		}
		r.HealthStore.Record(callID, from, sample)
		return
	}

	ct := r.Tracker.Conferences()
	conf := ct.ConferenceByPhone(ctx, from)
	if conf == nil {
		slog.DebugContext(ctx, "link_health peer set but endpoint not in an active conference",
			"endpoint", from, "peer", p.Peer)
		return
	}
	if !ct.ConferenceContains(ctx, conf.ID, from, p.Peer) {
		slog.DebugContext(ctx, "link_health peer not a co-member (phantom edge, dropping)",
			"endpoint", from, "peer", p.Peer, "conf_id", conf.ID)
		return
	}
	r.HealthStore.RecordEdge(conf.ID, from, p.Peer, sample)
}
