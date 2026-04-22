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
	TypeLinkHealth      = "link_health"        // Phone → Server: per-call stats snapshot
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
	SilentMode bool   `json:"silent_mode,omitempty"`
}

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
// ConnType is "host", "srflx", or "relay" (empty if nominated pair unknown).
// TS is phone-local unix milliseconds at the moment of sampling.
type LinkHealthPayload struct {
	TS       int64    `json:"ts"`
	LossPct  *float32 `json:"loss_pct,omitempty"`
	JitterMs *float32 `json:"jitter_ms,omitempty"`
	RttMs    *float32 `json:"rtt_ms,omitempty"`
	ConnType string   `json:"conn_type,omitempty"`
	BytesIn  *int64   `json:"bytes_in,omitempty"`
	BytesOut *int64   `json:"bytes_out,omitempty"`
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

	// Link-health telemetry (link_health messages)
	LinkHealth *LinkHealthPayload `json:"link_health,omitempty"`
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
