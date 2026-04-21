package owebrtc

import "testing"

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
