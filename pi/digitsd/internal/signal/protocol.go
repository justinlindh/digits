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
)

// ContactEntry represents a single contact in a sync payload.
type ContactEntry struct {
	Number string `json:"number"`
	Name   string `json:"name"`
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
