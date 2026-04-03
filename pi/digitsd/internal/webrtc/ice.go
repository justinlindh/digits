package owebrtc

import "github.com/pion/webrtc/v4"

// ICEServerConfig holds configuration for a single ICE/TURN/STUN server.
type ICEServerConfig struct {
	URLs       []string
	Username   string
	Credential string
}

// ICEConfig holds a list of ICE server configurations.
type ICEConfig struct {
	servers []ICEServerConfig
}

// NewICEConfig creates a new ICEConfig from a slice of server configs.
func NewICEConfig(servers []ICEServerConfig) *ICEConfig {
	return &ICEConfig{servers: servers}
}

// WebRTCConfig converts the ICEConfig to a Pion webrtc.Configuration.
// For TURN servers (Username non-empty), sets CredentialType to ICECredentialTypePassword.
func (c *ICEConfig) WebRTCConfig() webrtc.Configuration {
	if len(c.servers) == 0 {
		return webrtc.Configuration{}
	}

	iceServers := make([]webrtc.ICEServer, 0, len(c.servers))
	for _, s := range c.servers {
		srv := webrtc.ICEServer{
			URLs: s.URLs,
		}
		if s.Username != "" {
			srv.Username = s.Username
			srv.Credential = s.Credential
			srv.CredentialType = webrtc.ICECredentialTypePassword
		}
		iceServers = append(iceServers, srv)
	}

	return webrtc.Configuration{
		ICEServers: iceServers,
	}
}
