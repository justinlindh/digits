package main

import (
	"testing"
	"time"

	owebrtc "github.com/justinlindh/digits/pi/digitsd/internal/webrtc"
)

// Teardown callbacks are invoked from the phone Controller with c.mu held.
// pion's pc.Close can block for minutes when a TURN dial is blackholed, so
// each callback must detach state and return promptly, deferring the Close
// to a detached goroutine. These tests inject a blocking Close and assert
// the callback returns while the close is still in flight.

// slowClose returns a Close func that reports entry on closing and blocks
// until release is closed.
func slowClose(closing chan<- struct{}, release <-chan struct{}) func() error {
	return func() error {
		closing <- struct{}{}
		<-release
		return nil
	}
}

// waitPrompt fails the test if fn does not return within 2 seconds.
func waitPrompt(t *testing.T, name string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s blocked on a peer close", name)
	}
}

// waitCloseStarted fails the test if no close entry arrives within 2 seconds.
func waitCloseStarted(t *testing.T, closing <-chan struct{}) {
	t.Helper()
	select {
	case <-closing:
	case <-time.After(2 * time.Second):
		t.Fatal("peer close never started")
	}
}

func TestTearDownPeerReturnsWhileCloseBlocks(t *testing.T) {
	closing := make(chan struct{}, 2)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	d := newTestDaemon()
	d.mesh = owebrtc.NewMeshManager(owebrtc.NewICEConfig(nil))
	d.mesh.Adopt("5550003", owebrtc.NewPeerManagerWithCloseFn(slowClose(closing, release)))
	d.peerMgr = owebrtc.NewPeerManagerWithCloseFn(slowClose(closing, release))
	d.callPeer = "5550003"

	waitPrompt(t, "TearDownPeer", func() { d.TearDownPeer("5550003") })

	// All state must be detached synchronously, before the closes finish.
	d.mu.Lock()
	pm, callPeer := d.peerMgr, d.callPeer
	d.mu.Unlock()
	if pm != nil || callPeer != "" {
		t.Fatalf("2-party state not detached: peerMgr=%v callPeer=%q", pm, callPeer)
	}
	if d.mesh.GetPeer("5550003") != nil {
		t.Fatal("mesh peer not detached")
	}

	// The mesh close and the 2-party close run concurrently: both must start
	// while both are still blocked.
	waitCloseStarted(t, closing)
	waitCloseStarted(t, closing)
}

func TestRemoveMeshPeerReturnsWhileCloseBlocks(t *testing.T) {
	closing := make(chan struct{}, 1)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	d := newTestDaemon()
	d.mesh = owebrtc.NewMeshManager(owebrtc.NewICEConfig(nil))
	d.mesh.Adopt("5550002", owebrtc.NewPeerManagerWithCloseFn(slowClose(closing, release)))

	waitPrompt(t, "RemoveMeshPeer", func() { d.RemoveMeshPeer("5550002") })

	if d.mesh.GetPeer("5550002") != nil {
		t.Fatal("mesh peer not detached")
	}
	waitCloseStarted(t, closing)
}

func TestTearDownAllMeshPeersReturnsWhileCloseBlocks(t *testing.T) {
	closing := make(chan struct{}, 2)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	d := newTestDaemon()
	d.mesh = owebrtc.NewMeshManager(owebrtc.NewICEConfig(nil))
	d.mesh.Adopt("5550002", owebrtc.NewPeerManagerWithCloseFn(slowClose(closing, release)))
	d.mesh.Adopt("5550003", owebrtc.NewPeerManagerWithCloseFn(slowClose(closing, release)))

	waitPrompt(t, "TearDownAllMeshPeers", func() { d.TearDownAllMeshPeers() })

	if n := len(d.mesh.ActivePeers()); n != 0 {
		t.Fatalf("mesh still has %d peers after teardown", n)
	}
	// Each peer closes on its own goroutine, so both must start even while
	// both are still blocked.
	waitCloseStarted(t, closing)
	waitCloseStarted(t, closing)
}
