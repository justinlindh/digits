package owebrtc

import (
	"testing"
	"time"
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

// blockingClosePM returns a stub peer whose Close signals entry on closing,
// then blocks until release is closed. Simulates pion's minutes-long Close
// when a TURN dial is blackholed.
func blockingClosePM(closing chan<- struct{}, release <-chan struct{}) *PeerManager {
	return &PeerManager{closeFn: func() error {
		closing <- struct{}{}
		<-release
		return nil
	}}
}

func TestMesh_DetachPeerDoesNotClose(t *testing.T) {
	m := NewMeshManager(NewICEConfig(nil))
	closed := false
	m.Adopt("5550001", &PeerManager{closeFn: func() error { closed = true; return nil }})

	pm := m.DetachPeer("5550001")
	if pm == nil {
		t.Fatal("DetachPeer returned nil for a known peer")
	}
	if closed {
		t.Fatal("DetachPeer must not close the peer; ownership transfers to the caller")
	}
	if m.GetPeer("5550001") != nil {
		t.Fatal("peer still in mesh after detach")
	}
	if m.DetachPeer("5550001") != nil {
		t.Fatal("second DetachPeer should return nil")
	}
}

func TestMesh_RemovePeerDoesNotHoldLockAcrossClose(t *testing.T) {
	m := NewMeshManager(NewICEConfig(nil))
	closing := make(chan struct{}, 1)
	release := make(chan struct{})
	m.Adopt("5550001", blockingClosePM(closing, release))

	done := make(chan struct{})
	go func() {
		m.RemovePeer("5550001")
		close(done)
	}()
	<-closing

	// While the close is blocked, every other mesh accessor must stay usable:
	// the render loop calls SendPCMFrameToAll 50 times a second.
	ok := make(chan struct{})
	go func() {
		if m.GetPeer("5550001") != nil {
			t.Error("peer still visible during blocked close")
		}
		if n := len(m.ActivePeers()); n != 0 {
			t.Errorf("ActivePeers returned %d peers during blocked close, want 0", n)
		}
		m.SendPCMFrameToAll(make([]int16, 960))
		close(ok)
	}()
	select {
	case <-ok:
	case <-time.After(2 * time.Second):
		t.Fatal("mesh accessors blocked while a peer close is in flight")
	}
	close(release)
	<-done
}

func TestMesh_CloseAllDoesNotHoldLockAcrossClose(t *testing.T) {
	m := NewMeshManager(NewICEConfig(nil))
	closing := make(chan struct{}, 2)
	release := make(chan struct{})
	m.Adopt("5550001", blockingClosePM(closing, release))
	m.Adopt("5550002", blockingClosePM(closing, release))

	done := make(chan struct{})
	go func() {
		m.CloseAll()
		close(done)
	}()
	<-closing

	// The mesh must be empty and reusable while the closes are in flight.
	ok := make(chan struct{})
	go func() {
		if n := len(m.ActivePeers()); n != 0 {
			t.Errorf("ActivePeers returned %d peers during blocked CloseAll, want 0", n)
		}
		close(ok)
	}()
	select {
	case <-ok:
	case <-time.After(2 * time.Second):
		t.Fatal("mesh accessors blocked while CloseAll is in flight")
	}
	close(release)
	<-done
}
