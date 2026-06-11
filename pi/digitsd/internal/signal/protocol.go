package signal

import "encoding/json"

// Message types (must match server/internal/signaling/protocol.go)
const (
	TypeRegister   = "register"
	TypeCall       = "call"
	TypeRing       = "ring"
	TypeSDP        = "sdp"
	TypeICE        = "ice"
	TypeAnswer     = "answer"
	TypeHangup     = "hangup"
	TypeBusy       = "busy"
	TypeDTMF       = "dtmf"
	TypeError      = "error"
	TypeICEServers = "ice-servers"
	TypeRequestICE = "request-ice-servers"

	// TypePairingCode is sent by the server during registration when a device
	// is not yet paired. The client should display the code (e.g. on an LED
	// indicator or via a log message) so an admin can claim the phone.
	TypePairingCode = "pairing_code"

	// TypePaired is sent by the server after a successful pairing exchange.
	// The message includes a DeviceToken the client should persist to config.
	TypePaired = "paired"

	// TypeDeviceInfo is sent to the server on connect with version info.
	TypeDeviceInfo = "device_info"

	// TypeUpdateTrigger is sent by the server to trigger an update check.
	TypeUpdateTrigger = "update_trigger"

	// TypeUpdateStatus is sent to the server to report update progress.
	TypeUpdateStatus = "update_status"

	// TypeICERestart is sent by either peer to initiate an ICE restart.
	// Carries a new SDP offer with rotated ICE credentials.
	TypeICERestart = "ice_restart"

	// TypeFactoryReset is sent by the server to trigger a factory reset.
	TypeFactoryReset = "factory_reset"

	// TypeRestart is sent by the server to restart the service or reboot the device.
	TypeRestart = "restart"

	// TypeRingTest is sent by the server to briefly ring the bell for hardware verification.
	TypeRingTest = "ring_test"

	// TypeDevMode is sent by the server to enable or disable developer mode
	// (SSH + the on-device dev web UI). DevMode carries the on/off intent and,
	// when enabling, DevModePassword carries the new SSH login password.
	TypeDevMode = "dev_mode"

	// TypeLineSettings is sent by the server to push an updated Settings blob
	// for the line this device is registered as. Applied live.
	TypeLineSettings = "line_settings"

	// TypeVoicemailState is sent by the phone to the server when its local
	// unheard-message count changes (post-connect snapshot, after a recording
	// finalizes, after retrieval marks a message heard, after a delete).
	// Carries VoicemailUnheardCount; the server stores the value per-phone so
	// the web UI can render a badge without polling. Phone -> Server only.
	TypeVoicemailState = "voicemail_state"

	// TypeReleaseAvailable is sent by the server to notify the device that a
	// new release is available. Carries LatestPiVersion and LatestFWVersion.
	TypeReleaseAvailable = "release_available"

	// TypeLinkHealth is sent by the phone to the server with a per-call
	// quality telemetry snapshot. Phone → Server only.
	TypeLinkHealth = "link_health"

	// TypeRepair is sent by the phone over the authenticated WS to invalidate
	// its server-side pairing (paired_at, device_token) before *#0* reboots
	// digitsd. Without this, the next register-without-token from the same
	// hardware ID is rejected as "device_token required" because the server
	// still thinks the device is paired. Phone → Server only.
	TypeRepair = "repair"

	// TypeCallReturn is sent by the phone to request the last inbound caller
	// for *69 (Call Return). Server replies with TypeCallReturnResult.
	TypeCallReturn = "call_return"

	// TypeCallReturnResult is sent by the server with the last inbound caller's
	// number (in the Number field), or empty string if no eligible call exists.
	TypeCallReturnResult = "call_return_result"

	TypeCallReturnRetry     = "call_return_retry"
	TypeCallReturnRing      = "call_return_ring"
	TypeCallReturnCancel    = "call_return_cancel"
	TypeCallReturnCancelled = "call_return_cancelled"
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

// Conference member role constants. These match the server-side DB CHECK
// constraint and the wire representation in ConferenceMemberInfo.Role.
const (
	RoleHost  = "host"
	RoleAdded = "added"
)

// LinkHealthPayload carries per-sample call-quality telemetry from phone to
// signald. Mirrors server/internal/signaling.LinkHealthPayload; the two must
// stay in sync. All numeric fields are pointers so "not available" is omitted
// from JSON.
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

// LineSettings is the wire-format copy of server-side line.Settings used in
// signaling messages. Mirrors the server definition so digitsd doesn't need
// to import any server code.
//
// Valid VoiceStyle values are defined canonically in internal/config as
// VoiceStyleCopper and VoiceStyleModern. Any new voice style must be added
// there, in server/internal/line, and in server/internal/signaling.
type LineSettings struct {
	VoiceStyle string     `json:"voice_style,omitempty"`
	SilentMode bool       `json:"silent_mode,omitempty"`
	AutoUpdate bool       `json:"auto_update,omitempty"`
	Voicemail  *Voicemail `json:"voicemail,omitempty"`
}

// Voicemail is the wire-format copy of the per-line voicemail settings sub-
// blob pushed by the server. Mirrors server/internal/signaling.Voicemail.
//
// Field-name gotcha: durations cross the wire as integer seconds. The
// receiver converts to time.Duration when writing to config.
type Voicemail struct {
	Enabled            bool `json:"enabled"`
	RingTimeoutSeconds int  `json:"ring_timeout_seconds"`
}

// ConferenceMemberInfo describes one participant in a conference call.
type ConferenceMemberInfo struct {
	Phone string `json:"phone"`
	Role  string `json:"role"` // "host" or "added"
}

// ICEServer represents a STUN or TURN server configuration.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// Message is the shared signaling envelope used between client and server.
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
	// PairingCodeTTL is the server-reported validity of PairingCode, in
	// seconds. Drives the spoken countdown and the pre-expiry refresh.
	PairingCodeTTL int    `json:"pairing_code_ttl,omitempty"`
	HardwareID     string `json:"hardware_id,omitempty"`
	DeviceToken    string `json:"device_token,omitempty"`

	// Version info (device_info messages)
	PiVersion       string `json:"pi_version,omitempty"`
	PiCommit        string `json:"pi_commit,omitempty"`
	FirmwareVersion string `json:"firmware_version,omitempty"`
	FirmwareCommit  string `json:"firmware_commit,omitempty"`

	// LocalAddr is the device's primary LAN address as the device sees
	// itself, reported in device_info so the server can display it on the
	// owner's /phones page. The path through CF or NPM strips the source
	// IP, so the device is the authoritative source.
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

	// DevMode indicates the device has dev-mode enabled (device_info messages)
	// and, on dev_mode command messages, the requested on/off state.
	DevMode bool `json:"dev_mode,omitempty"`

	// DevModePassword is the new SSH login password carried on a dev_mode
	// command when enabling. Server -> device only.
	DevModePassword string `json:"dev_mode_password,omitempty"`

	// Restart fields (restart messages)
	RestartMode string `json:"restart_mode,omitempty"` // "service" or "reboot"

	// Per-line settings updates (line_settings messages)
	LineSettings *LineSettings `json:"line_settings,omitempty"`

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

	// Voicemail state (voicemail_state messages). Explicit zero must round-
	// trip after MarkHeard-all clears the badge, so this field is NOT
	// omitempty: the server distinguishes "0 unheard" from "field missing"
	// when reconciling its per-phone cache.
	VoicemailUnheardCount int `json:"voicemail_unheard_count"`
}

// ParseMessage deserializes a JSON-encoded signaling message.
func ParseMessage(data []byte) (*Message, error) {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// Marshal serializes the message to JSON.
func (m *Message) Marshal() ([]byte, error) {
	return json.Marshal(m)
}
