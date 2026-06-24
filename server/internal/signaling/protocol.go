package signaling

import "encoding/json"

// Message types
const (
	TypeRegister            = "register"
	TypeCall                = "call"
	TypeRing                = "ring"
	TypeSDP                 = "sdp"
	TypeICE                 = "ice"
	TypeAnswer              = "answer"
	TypeHangup              = "hangup"
	TypeBusy                = "busy"
	TypeDTMF                = "dtmf"
	TypeError               = "error"
	TypeICEServers          = "ice-servers"
	TypeRequestICE          = "request-ice-servers"
	TypePairingCode         = "pairing_code"
	TypePaired              = "paired"
	TypeDeviceInfo          = "device_info"           // Phone → Server: version info on connect
	TypeUpdateTrigger       = "update_trigger"        // Server → Phone: check and apply updates
	TypeUpdateStatus        = "update_status"         // Phone → Server: update progress report
	TypeICERestart          = "ice_restart"           // Bidirectional: ICE restart offer with new credentials
	TypeFactoryReset        = "factory_reset"         // Server → Phone: trigger factory reset
	TypeRestart             = "restart"               // Server → Phone: restart service or reboot
	TypeRingTest            = "ring_test"             // Server → Phone: brief ring for hardware verification
	TypeDevMode             = "dev_mode"              // Server → Phone: enable/disable developer mode (SSH + dev web UI)
	TypeLineSettings        = "line_settings"         // Server → Phone: per-line config update
	TypeLinkHealth          = "link_health"           // Phone → Server: per-call stats snapshot
	TypeRepair              = "repair"                // Phone → Server: invalidate pairing (used by *#0* before reboot)
	TypeCallReturn          = "call_return"           // Phone → Server: request last inbound caller
	TypeCallReturnResult    = "call_return_result"    // Server → Phone: last inbound caller number
	TypeCallReturnRetry     = "call_return_retry"     // Phone → Server: register busy-retry for *69
	TypeCallReturnRing      = "call_return_ring"      // Server → Phone: target is free, ring with distinctive pattern
	TypeCallReturnCancel    = "call_return_cancel"    // Phone → Server: cancel pending retry (*89)
	TypeCallReturnCancelled = "call_return_cancelled" // Server → Phone: confirm cancellation

	TypeReleaseAvailable = "release_available" // Server → All: new release detected

	TypeLineRenumber = "line_renumber" // Server → Phone: persist corrected line number after a register reconcile

	TypeVoicemailState = "voicemail_state" // Phone → Server: per-handset unheard voicemail count snapshot
)

// Update status values reported by phones during OTA updates. The full wire
// vocabulary is downloading, applying, rebooting, success, failed (consumed by
// the phone-detail UI); these are the values the server itself acts on.
const (
	UpdateStatusRebooting = "rebooting" // device is rebooting to apply an update
	UpdateStatusSuccess   = "success"   // update applied, device reconnected
)

// Extension pickup types (POTS extension model: pick up a second handset mid-call)
const (
	TypeExtensionPickup  = "extension_pickup"  // Phone → Server: device is picking up during active call on its line
	TypeExtensionConnect = "extension_connect" // Server → Phone: establish peer connection for the extension
	TypeExtensionActive  = "extension_active"  // Server → Phone: notify original device that an extension joined
)

// Conference message types (three-way calling)
const (
	TypeConferenceMerge    = "conference_merge"    // client -> server
	TypeConferenceMember   = "conference_member"   // server -> client
	TypeConferenceConnect  = "conference_connect"  // server -> client
	TypeConferenceLeave    = "conference_leave"    // server -> client
	TypeConferenceEnd      = "conference_end"      // server -> client
	TypeConferenceRejected = "conference_rejected" // server -> client (merge validation failed)
)

// Conference member role constants. These match the DB CHECK constraint in
// db.go and the wire representation in ConferenceMemberInfo.Role.
const (
	RoleHost  = "host"
	RoleAdded = "added"
)

// LineSettings is the wire-format copy of server/internal/line.Settings used
// in signaling messages. Kept as a separate struct (not imported directly
// from internal/line) so the dependency on internal/line is isolated to
// linestore_adapter.go.
//
// Valid VoiceStyle values are defined canonically in server/internal/line
// as VoiceStyleCopper and VoiceStyleModern. Any new voice style must be
// added there, in pi/digitsd/internal/config, and in pi/digitsd/internal/signal.
type LineSettings struct {
	VoiceStyle string     `json:"voice_style,omitempty"`
	SilentMode bool       `json:"silent_mode,omitempty"`
	AutoUpdate bool       `json:"auto_update,omitempty"`
	Voicemail  *Voicemail `json:"voicemail,omitempty"`
}

// Voicemail is the wire-format copy of server/internal/line.Voicemail.
// Pointer-typed on LineSettings so old daemons that don't yet decode the
// "voicemail" key see no surprise field, and newer daemons can distinguish
// "settings push from a pre-voicemail server" (nil pointer) from "voicemail
// explicitly disabled" (non-nil with Enabled=false).
//
// Inner fields deliberately omit omitempty: when the server sends an
// authoritative voicemail block, we want explicit `enabled: false` and
// `ring_timeout_seconds: 0` to round-trip literally rather than disappear
// into the zero-value hole. Otherwise the daemon receiver can't tell
// "server didn't set this" from "server set it to zero". The outer
// pointer's omitempty handles the optional-on-the-envelope semantics on
// its own.
//
// Field names mirror the daemon config keys exactly. The daemon's local
// config holds RingTimeout as a time.Duration; the wire uses integer seconds
// and the daemon converts on receipt.
type Voicemail struct {
	Enabled            bool `json:"enabled"`
	RingTimeoutSeconds int  `json:"ring_timeout_seconds"`
}

// ConferenceMemberInfo describes one participant in a conference call.
type ConferenceMemberInfo struct {
	Phone string `json:"phone"`
	Role  string `json:"role"` // "host" or "added"
}

// NOTE: this struct is mirrored in pi/digitsd/internal/signal/protocol.go.
// Any field change must be applied in both places; drift-detection tests
// on each side assert the round-trip shape.
//
// LinkHealthPayload carries per-sample call-quality telemetry from phone to
// signald. All numeric fields are pointers so "not available this sample" is
// expressed as nil (omitted from JSON). Units:
//
//	LossPct:  packet loss as percent (0-100)
//	JitterMs: RTCP jitter, milliseconds
//	RttMs:    ICE nominated-pair round-trip time, milliseconds
//	BytesIn:  bytes received on the nominated pair since call start
//	BytesOut: bytes sent on the nominated pair since call start
//
// ConnType is one of "host", "srflx", "prflx", "relay" (ICE candidate type
// of the local end of the nominated pair per RFC 8445). Empty if no pair
// is nominated. "prflx" (peer-reflexive) is less common than srflx but
// legitimate when the remote observes an address the local side didn't
// anticipate; downstream consumers should treat it as a valid state.
// TS is phone-local unix milliseconds at the moment of sampling.
type LinkHealthPayload struct {
	TS       int64    `json:"ts"`
	LossPct  *float32 `json:"loss_pct,omitempty"`
	JitterMs *float32 `json:"jitter_ms,omitempty"`
	RttMs    *float32 `json:"rtt_ms,omitempty"`
	ConnType string   `json:"conn_type,omitempty"`
	BytesIn  *int64   `json:"bytes_in,omitempty"`
	BytesOut *int64   `json:"bytes_out,omitempty"`
	// Peer identifies the remote endpoint this sample is about. Empty for
	// 2-party samples (backward compatible). Set to the peer's phone
	// number for 3-way mesh samples; each participant emits one sample
	// per remote peer per tick.
	Peer string `json:"peer,omitempty"`
}

// ICEServer represents a STUN or TURN server configuration.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// Message is the JSON wire format exchanged between signald and devices over
// WebSocket. Fields are tagged omitempty so each message type carries only
// the fields relevant to its Type constant; unused fields are omitted from
// the encoded payload.
type Message struct {
	Type        string      `json:"type"`
	From        string      `json:"from,omitempty"`
	To          string      `json:"to,omitempty"`
	Number      string      `json:"number,omitempty"`
	Digit       string      `json:"digit,omitempty"`
	SDP         string      `json:"sdp,omitempty"`
	Candidate   string      `json:"candidate,omitempty"`
	Error       string      `json:"error,omitempty"`
	Servers     []ICEServer `json:"servers,omitempty"`
	PairingCode string      `json:"pairing_code,omitempty"`
	// PairingCodeTTL is how many seconds the accompanying PairingCode stays
	// valid. Sent so the device can announce an accurate countdown and refresh
	// the code before the server expires it, rather than guessing from a local
	// constant.
	PairingCodeTTL int    `json:"pairing_code_ttl,omitempty"`
	HardwareID     string `json:"hardware_id,omitempty"`
	DeviceToken    string `json:"device_token,omitempty"`

	// Version info (device_info messages)
	PiVersion       string `json:"pi_version,omitempty"`
	PiCommit        string `json:"pi_commit,omitempty"`
	FirmwareVersion string `json:"firmware_version,omitempty"`
	FirmwareCommit  string `json:"firmware_commit,omitempty"`

	// LocalAddr is the device's primary LAN address as it sees itself,
	// reported in device_info. The server filters non-private values
	// before storing so a compromised client cannot push a public IP into
	// the operator UI.
	LocalAddr string `json:"local_addr,omitempty"`

	// Update trigger fields (update_trigger messages)
	TargetPiVersion string `json:"target_pi_version,omitempty"`
	TargetFWVersion string `json:"target_fw_version,omitempty"`

	// Release notification fields (release_available messages)
	LatestPiVersion string `json:"latest_pi_version,omitempty"`
	LatestFWVersion string `json:"latest_fw_version,omitempty"`

	// Update status fields (update_status messages)
	UpdateStatus string `json:"update_status,omitempty"` // downloading, applying, rebooting, success, failed
	UpdateDetail string `json:"update_detail,omitempty"` // human-readable detail

	// Restart fields (restart messages)
	RestartMode string `json:"restart_mode,omitempty"` // "service" or "reboot"

	// Per-line settings updates (line_settings messages)
	LineSettings *LineSettings `json:"line_settings,omitempty"`

	// DevMode indicates the device has dev-mode enabled (device_info messages)
	// and, on dev_mode command messages, the requested on/off state.
	DevMode bool `json:"dev_mode,omitempty"`

	// DevModePassword is the new SSH login password carried on a dev_mode
	// command when enabling. Server -> device only.
	DevModePassword string `json:"dev_mode_password,omitempty"`

	// Extension pickup fields (POTS extension model).
	// Extension is true when this SDP/ICE message belongs to an extension
	// pickup connection rather than the primary call. The relay routes
	// extension SDP/ICE to specific hardware IDs rather than broadcasting
	// to all devices on the line.
	Extension bool `json:"extension,omitempty"`

	// Conference fields (party-line / three-way calling).
	ConfID     string                 `json:"conf_id,omitempty"`
	HeldPeer   string                 `json:"held_peer,omitempty"`
	ActivePeer string                 `json:"active_peer,omitempty"`
	Peer       string                 `json:"peer,omitempty"`
	Initiator  bool                   `json:"initiator,omitempty"`
	Members    []ConferenceMemberInfo `json:"members,omitempty"`
	Reason     string                 `json:"reason,omitempty"`

	// Link-health telemetry (link_health messages)
	LinkHealth *LinkHealthPayload `json:"link_health,omitempty"`

	// Voicemail state (voicemail_state messages). Reports the current
	// unheard-message count for the originating handset. No omitempty: an
	// explicit zero must round-trip after MarkHeard-all so the server can
	// distinguish "feature is on, mailbox empty" from "absent / unknown".
	VoicemailUnheardCount int `json:"voicemail_unheard_count"`
}

// ParseMessage decodes a JSON-encoded signaling message from the wire.
func ParseMessage(data []byte) (*Message, error) {
	var m Message
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Marshal encodes the message to JSON for transmission over WebSocket.
func (m *Message) Marshal() ([]byte, error) {
	return json.Marshal(m)
}
