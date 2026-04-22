package signal

import "encoding/json"

// Message types (must match server/internal/signaling/protocol.go)
const (
	TypeRegister        = "register"
	TypeCall            = "call"
	TypeRing            = "ring"
	TypeSDP             = "sdp"
	TypeICE             = "ice"
	TypeAnswer          = "answer"
	TypeHangup          = "hangup"
	TypeBusy            = "busy"
	TypeDTMF            = "dtmf"
	TypeError           = "error"
	TypeICEServers      = "ice-servers"
	TypeRequestICE      = "request-ice-servers"
	TypeSync            = "sync"
	TypeContacts        = "contacts"
	TypeContactsUpdated = "contacts_updated"

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

	// TypeLineSettings is sent by the server to push an updated Settings blob
	// for the line this device is registered as. Applied live.
	TypeLineSettings = "line_settings"

	// TypeLinkHealth is sent by the phone to the server with a per-call
	// quality telemetry snapshot. Phone → Server only.
	TypeLinkHealth = "link_health"
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
}

// ContactEntry represents a single contact in a sync payload.
type ContactEntry struct {
	Number string `json:"number"`
	Name   string `json:"name"`
}

// LineSettings is the wire-format copy of server-side line.Settings used in
// signaling messages. Mirrors the server definition so digitsd doesn't need
// to import any server code.
//
// Valid VoiceStyle values are defined canonically in internal/config as
// VoiceStyleCopper and VoiceStyleModern. Any new voice style must be added
// there, in server/internal/line, and in server/internal/signaling.
type LineSettings struct {
	VoiceStyle string `json:"voice_style,omitempty"`
	SilentMode bool   `json:"silent_mode,omitempty"`
}

// ICEServer represents a STUN or TURN server configuration.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// Message is the shared signaling envelope used between client and server.
type Message struct {
	Type        string         `json:"type"`
	From        string         `json:"from,omitempty"`
	To          string         `json:"to,omitempty"`
	Number      string         `json:"number,omitempty"`
	Digit       string         `json:"digit,omitempty"`
	SDP         string         `json:"sdp,omitempty"`
	Candidate   string         `json:"candidate,omitempty"`
	Error       string         `json:"error,omitempty"`
	Servers     []ICEServer    `json:"servers,omitempty"`
	Contacts    []ContactEntry `json:"contacts,omitempty"`
	PairingCode string         `json:"pairing_code,omitempty"`
	HardwareID  string         `json:"hardware_id,omitempty"`
	DeviceToken string         `json:"device_token,omitempty"`

	// Version info (device_info messages)
	PiVersion       string `json:"pi_version,omitempty"`
	PiCommit        string `json:"pi_commit,omitempty"`
	FirmwareVersion string `json:"firmware_version,omitempty"`
	FirmwareCommit  string `json:"firmware_commit,omitempty"`

	// Update trigger fields (update_trigger messages)
	TargetPiVersion string `json:"target_pi_version,omitempty"`
	TargetFWVersion string `json:"target_fw_version,omitempty"`

	// Update status fields (update_status messages)
	UpdateStatus string `json:"update_status,omitempty"` // downloading, applying, rebooting, success, failed
	UpdateDetail string `json:"update_detail,omitempty"` // human-readable detail

	// Flash capability (device_info messages)
	FlashCapable bool `json:"flash_capable,omitempty"`

	// Restart fields (restart messages)
	RestartMode string `json:"restart_mode,omitempty"` // "service" or "reboot"

	// Per-line settings updates (line_settings messages)
	LineSettings *LineSettings `json:"line_settings,omitempty"`

	// Link-health telemetry (link_health messages)
	LinkHealth *LinkHealthPayload `json:"link_health,omitempty"`
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
