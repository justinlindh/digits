package owebrtc

import (
	"testing"
)

func TestMesh_AddRemovePeers(t *testing.T) {
	m := NewMeshManager(NewICEConfig(nil))
	defer m.CloseAll()

	pa, err := m.AddPeer("5550001")
	if err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
	if pa == nil {
		t.Fatalf("expected non-nil PeerManager")
	}
	if got := m.GetPeer("5550001"); got != pa {
		t.Fatalf("GetPeer returned different PM")
	}

	_, _ = m.AddPeer("5550002")
	peers := m.ActivePeers()
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}

	m.RemovePeer("5550001")
	if m.GetPeer("5550001") != nil {
		t.Fatalf("expected peer removed")
	}
	if len(m.ActivePeers()) != 1 {
		t.Fatalf("expected 1 remaining peer, got %d", len(m.ActivePeers()))
	}
}

func TestMesh_AddPeerIsIdempotent(t *testing.T) {
	m := NewMeshManager(NewICEConfig(nil))
	defer m.CloseAll()

	p1, err := m.AddPeer("5550001")
	if err != nil {
		t.Fatalf("first AddPeer: %v", err)
	}
	p2, err := m.AddPeer("5550001")
	if err != nil {
		t.Fatalf("second AddPeer: %v", err)
	}
	if p1 != p2 {
		t.Fatalf("expected same PeerManager returned on repeated Add")
	}
}

func TestMesh_CloseAll(t *testing.T) {
	m := NewMeshManager(NewICEConfig(nil))
	_, _ = m.AddPeer("5550001")
	_, _ = m.AddPeer("5550002")
	m.CloseAll()
	if len(m.ActivePeers()) != 0 {
		t.Fatalf("expected no peers after CloseAll, got %d", len(m.ActivePeers()))
	}
}

func TestMesh_RemovePeer_UnknownIsNoop(t *testing.T) {
	m := NewMeshManager(NewICEConfig(nil))
	defer m.CloseAll()
	// Should not panic.
	m.RemovePeer("5550999")
}

func TestMesh_AdoptPreservesExisting(t *testing.T) {
	m := NewMeshManager(NewICEConfig(nil))
	defer m.CloseAll()

	pm1, err := NewPeerManager(NewICEConfig(nil))
	if err != nil {
		t.Fatalf("NewPeerManager: %v", err)
	}
	m.Adopt("5550001", pm1)

	if got := m.GetPeer("5550001"); got != pm1 {
		t.Fatalf("Adopt: expected pm1 stored, got %p vs %p", got, pm1)
	}

	// Adopt a second PM with same phone: should not replace; should close the incoming.
	pm2, err := NewPeerManager(NewICEConfig(nil))
	if err != nil {
		t.Fatalf("NewPeerManager: %v", err)
	}
	m.Adopt("5550001", pm2)

	if got := m.GetPeer("5550001"); got != pm1 {
		t.Fatalf("Adopt-collision: expected pm1 retained, got different")
	}
}

// TestMesh_SendPCMFrameToAll_NoMutation verifies that SendPCMFrameToAll does not
// modify the caller's frame slice, even when multiple goroutines encode in parallel.
// SendPCMFrame may replace its local frame pointer when muted but must never write
// back to the shared input slice.
func TestMesh_SendPCMFrameToAll_NoMutation(t *testing.T) {
	m := NewMeshManager(NewICEConfig(nil))
	defer m.CloseAll()

	// Add two peers so the parallel fan-out path is exercised.
	pm1, err := m.AddPeer("5550001")
	if err != nil {
		t.Fatalf("AddPeer 1: %v", err)
	}
	pm2, err := m.AddPeer("5550002")
	if err != nil {
		t.Fatalf("AddPeer 2: %v", err)
	}
	// Mute both peers so SendPCMFrame's zeroBuf logic runs.
	pm1.SetOutboundMuted(true)
	pm2.SetOutboundMuted(true)

	const frameLen = 960
	frame := make([]int16, frameLen)
	for i := range frame {
		frame[i] = int16(i + 1) // non-zero sentinel values
	}
	snapshot := make([]int16, frameLen)
	copy(snapshot, frame)

	m.SendPCMFrameToAll(frame)

	for i := range frame {
		if frame[i] != snapshot[i] {
			t.Fatalf("frame[%d] was mutated: got %d, want %d", i, frame[i], snapshot[i])
		}
	}
}
