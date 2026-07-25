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

// DetachPeer removes and returns the PeerManager for phone without closing
// it, or nil if none exists. Ownership transfers to the caller, who must
// close it. Teardown paths use this so the potentially minutes-long Close
// (see PeerManager.Close) runs off the caller's locks.
func (m *MeshManager) DetachPeer(phone string) *PeerManager {
	m.mu.Lock()
	defer m.mu.Unlock()
	pm := m.peers[phone]
	delete(m.peers, phone)
	return pm
}

// DetachAll removes and returns every peer without closing them, keyed by
// phone number. Ownership transfers to the caller. After DetachAll the
// MeshManager is empty and reusable.
func (m *MeshManager) DetachAll() map[string]*PeerManager {
	m.mu.Lock()
	defer m.mu.Unlock()
	detached := m.peers
	m.peers = make(map[string]*PeerManager)
	return detached
}

// RemovePeer tears down the PeerManager for phone, if any. Safe to call with
// an unknown phone -- no-op. The Close runs outside m.mu: holding the lock
// across a blocked Close would wedge SendPCMFrameToAll and every other mesh
// accessor for the duration.
func (m *MeshManager) RemovePeer(phone string) {
	if pm := m.DetachPeer(phone); pm != nil {
		_ = pm.Close()
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
//
// With two or more peers, each encode runs in its own goroutine so that a
// WriteSample stall on one peer does not delay the others.
func (m *MeshManager) SendPCMFrameToAll(frame []int16) {
	m.mu.Lock()
	peers := make([]*PeerManager, 0, len(m.peers))
	for _, pm := range m.peers {
		peers = append(peers, pm)
	}
	m.mu.Unlock()

	if len(peers) == 0 {
		return
	}
	if len(peers) == 1 {
		// Single-peer fast path: no goroutine overhead.
		peers[0].SendPCMFrame(frame)
		return
	}
	var wg sync.WaitGroup
	wg.Add(len(peers))
	for _, pm := range peers {
		go func(p *PeerManager) {
			defer wg.Done()
			p.SendPCMFrame(frame)
		}(pm)
	}
	wg.Wait()
}

// Adopt inserts an existing PeerManager into the mesh under phone. Unlike
// AddPeer, no new PeerConnection is constructed; the caller is transferring
// ownership of pm. If a peer with that phone already exists, the incoming pm
// is closed to avoid leaking and the existing entry is retained.
func (m *MeshManager) Adopt(phone string, pm *PeerManager) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.peers[phone]; ok {
		// Don't replace; close the incoming to avoid leak.
		_ = pm.Close()
		return
	}
	m.peers[phone] = pm
}

// CloseAll tears down every peer. After CloseAll the MeshManager can be
// reused: new AddPeer calls will construct fresh PeerManagers. The Closes
// run outside m.mu for the same reason as RemovePeer.
func (m *MeshManager) CloseAll() {
	for _, pm := range m.DetachAll() {
		_ = pm.Close()
	}
}
