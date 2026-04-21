package owebrtc

import (
	"fmt"
	"sync"
)

// MeshManager owns N PeerManager instances, one per remote phone number, for
// multi-party (conference) calls. For 2-party calls, a single peer with the
// peer number as the key works equivalently. The zero value is not ready;
// use NewMeshManager.
type MeshManager struct {
	mu     sync.Mutex
	iceCfg *ICEConfig
	peers  map[string]*PeerManager
}

// NewMeshManager creates a MeshManager using the given ICE configuration for
// all peer connections it creates.
func NewMeshManager(cfg *ICEConfig) *MeshManager {
	return &MeshManager{
		iceCfg: cfg,
		peers:  make(map[string]*PeerManager),
	}
}

// AddPeer creates a new PeerManager for phone, or returns the existing one if
// a peer with that number already exists. Idempotent.
func (m *MeshManager) AddPeer(phone string) (*PeerManager, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.peers[phone]; ok {
		return existing, nil
	}
	pm, err := NewPeerManager(m.iceCfg)
	if err != nil {
		return nil, fmt.Errorf("new peer manager for %s: %w", phone, err)
	}
	m.peers[phone] = pm
	return pm, nil
}

// GetPeer returns the PeerManager for phone, or nil if none exists.
func (m *MeshManager) GetPeer(phone string) *PeerManager {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.peers[phone]
}

// RemovePeer tears down the PeerManager for phone, if any. Safe to call with
// an unknown phone -- no-op.
func (m *MeshManager) RemovePeer(phone string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if pm, ok := m.peers[phone]; ok {
		_ = pm.Close()
		delete(m.peers, phone)
	}
}

// ActivePeers returns a snapshot of the current peer phone numbers.
func (m *MeshManager) ActivePeers() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.peers))
	for p := range m.peers {
		out = append(out, p)
	}
	return out
}

// SendPCMFrameToAll sends a PCM frame to every peer's local track. Each peer
// encodes the frame independently using its own Opus encoder, respecting per-peer
// outbound mute: muted peers encode silence (Opus DTX comfort noise) while
// unmuted peers encode the live mic audio. The input frame is never modified.
func (m *MeshManager) SendPCMFrameToAll(frame []int16) {
	m.mu.Lock()
	peers := make([]*PeerManager, 0, len(m.peers))
	for _, pm := range m.peers {
		peers = append(peers, pm)
	}
	m.mu.Unlock()

	for _, pm := range peers {
		pm.SendPCMFrame(frame)
	}
}

// CloseAll tears down every peer. After CloseAll the MeshManager can be
// reused: new AddPeer calls will construct fresh PeerManagers.
func (m *MeshManager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for p, pm := range m.peers {
		_ = pm.Close()
		delete(m.peers, p)
	}
}
