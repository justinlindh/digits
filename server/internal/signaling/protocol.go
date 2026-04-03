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
	TypeError      = "error"
	TypeICEServers = "ice-servers"
	TypeRequestICE = "request-ice-servers"
	TypePairingCode = "pairing_code"
	TypePaired      = "paired"
	TypeDeviceInfo  = "device_info" // Phone → Server: version info on connect
	TypeUpdateTrigger   = "update_trigger"    // Server → Phone: check and apply updates
	TypeUpdateStatus    = "update_status"     // Phone → Server: update progress report
)

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
