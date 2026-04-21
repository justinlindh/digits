package signaling

import "encoding/json"

// Message types
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
	TypePairingCode = "pairing_code"
	TypePaired      = "paired"
	TypeDeviceInfo  = "device_info" // Phone → Server: version info on connect
	TypeUpdateTrigger   = "update_trigger"    // Server → Phone: check and apply updates
	TypeUpdateStatus    = "update_status"     // Phone → Server: update progress report
	TypeICERestart      = "ice_restart"       // Bidirectional: ICE restart offer with new credentials
	TypeFactoryReset    = "factory_reset"    // Server → Phone: trigger factory reset
	TypeRestart         = "restart"            // Server → Phone: restart service or reboot
	TypeLineSettings    = "line_settings"      // Server → Phone: per-line config update
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

// LineSettings is the wire-format copy of server/internal/line.Settings used
// in signaling messages. Kept as a separate struct (not imported directly
// from internal/line) so the dependency on internal/line is isolated to
// linestore_adapter.go.
//
// Valid VoiceStyle values are defined canonically in server/internal/line
// as VoiceStyleCopper and VoiceStyleModern. Any new voice style must be
// added there, in pi/digitsd/internal/config, and in pi/digitsd/internal/signal.
type LineSettings struct {
	VoiceStyle string `json:"voice_style,omitempty"`
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

type Message struct {
	Type        string         `json:"type"`
	From        string         `json:"from,omitempty"`
	To          string         `json:"to,omitempty"`
	Number      string         `json:"number,omitempty"`
	Digit       string         `json:"digit,omitempty"`
	SDP         string         `json:"sdp,omitempty"`
	Candidate   string         `json:"candidate,omitempty"`
	Error       string         `json:"error,omitempty"`
	Servers     []ICEServer `json:"servers,omitempty"`
	PairingCode string      `json:"pairing_code,omitempty"`
	HardwareID  string      `json:"hardware_id,omitempty"`
	DeviceToken string      `json:"device_token,omitempty"`

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

	// Conference fields (party-line / three-way calling).
	ConfID     string                 `json:"conf_id,omitempty"`
	HeldPeer   string                 `json:"held_peer,omitempty"`
	ActivePeer string                 `json:"active_peer,omitempty"`
	Peer       string                 `json:"peer,omitempty"`
	Initiator  bool                   `json:"initiator,omitempty"`
	Members    []ConferenceMemberInfo `json:"members,omitempty"`
	Reason     string                 `json:"reason,omitempty"`
}

func ParseMessage(data []byte) (*Message, error) {
	var m Message
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Message) Marshal() ([]byte, error) {
	return json.Marshal(m)
}
