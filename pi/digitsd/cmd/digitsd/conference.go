package main

// Conference (mesh) callbacks and helpers. Cut from main.go to keep the
// daemon entrypoint focused on startup and shared infrastructure. The
// daemonCallbacks struct definition stays on main.go; only methods that
// operate on the mesh / multi-party state (or the lookup helper that
// chooses between the 2-party callPeer and the mesh's active peer) live
// here.

import (
	"fmt"
	"log/slog"

	"github.com/justinlindh/digits/pi/digitsd/internal/phone"
	sigclient "github.com/justinlindh/digits/pi/digitsd/internal/signal"
	owebrtc "github.com/justinlindh/digits/pi/digitsd/internal/webrtc"

	"github.com/pion/webrtc/v4"
)

// ensureMesh lazily creates the conference mesh manager and returns it.
// The caller must hold d.mu.
func (d *daemonCallbacks) ensureMesh() *owebrtc.MeshManager {
	if d.mesh == nil {
		d.mesh = owebrtc.NewMeshManager(owebrtc.NewICEConfig(d.iceServers))
	}
	return d.mesh
}

func (d *daemonCallbacks) MutePeer(phone string) { d.setPeerMuted(phone, true) }

func (d *daemonCallbacks) UnmutePeer(phone string) { d.setPeerMuted(phone, false) }

// setPeerMuted sets both audio directions on the peer's WebRTC connection,
// trying the conference mesh first and falling back to the 2-party peer.
func (d *daemonCallbacks) setPeerMuted(phone string, muted bool) {
	d.mu.Lock()
	mesh := d.mesh
	pm := d.peerMgr
	callPeer := d.callPeer
	d.mu.Unlock()

	// First try the conference mesh.
	if mesh != nil {
		if meshPM := mesh.GetPeer(phone); meshPM != nil {
			meshPM.SetOutboundMuted(muted)
			meshPM.SetInboundMuted(muted)
			slog.Info("mute peer (mesh)", "phone", phone, "muted", muted)
			return
		}
	}
	// Fall back to the 2-party peerMgr if phone matches the current 2-party peer.
	if pm != nil && callPeer == phone {
		pm.SetOutboundMuted(muted)
		pm.SetInboundMuted(muted)
		slog.Info("mute peer (2-party)", "phone", phone, "muted", muted)
		return
	}
	slog.Warn("setPeerMuted: no peer found", "phone", phone, "muted", muted)
}

func (d *daemonCallbacks) MigrateToMesh(phone string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.peerMgr == nil || d.callPeer != phone {
		slog.Warn("MigrateToMesh: no matching 2-party peer", "phone", phone, "callPeer", d.callPeer)
		return
	}

	d.ensureMesh()

	// Transfer ownership: the existing PeerManager moves into the mesh under
	// the peer's phone key. d.peerMgr is cleared so future 2-party calls
	// create a fresh PeerConnection. d.callPeer is intentionally kept so that
	// HOOK:FLASH dispatch and other paths can still identify the B party after
	// migration (the peer is now in the mesh, but its identity doesn't change).
	d.mesh.Adopt(phone, d.peerMgr)
	d.peerMgr = nil
}

// currentPeer returns the phone number of the 2-party remote peer that
// HOOK:FLASH dispatch should treat as the "active" party to hold. The answer
// depends on the controller's state rather than on daemon internals -- this
// dispatches explicitly so future state additions have to declare their own
// peer policy instead of silently inheriting the "len(mesh)==1" heuristic.
//
// - CONNECTED: prefer d.callPeer. It's set by InitiateCall/AnswerCall; if a
//   previous ADD was aborted and its TearDownPeer cleared d.callPeer while
//   leaving the original held party B in the mesh, fall back to the single
//   mesh peer.
// - ADD_*: the controller has already captured the held party in c.heldPeer,
//   so the value returned here is not consulted by onHookFlash. Return
//   d.callPeer as a best-effort identity.
// - All other states: no meaningful "current peer" -- return empty.
//
// Must NOT be called with d.mu held. ctrl.State() acquires c.mu, so the
// lock-order invariant is preserved by snapshotting the state first.
func (d *daemonCallbacks) currentPeer() string {
	s := d.ctrl.State()

	d.mu.Lock()
	defer d.mu.Unlock()

	switch s {
	case phone.StateCONNECTED:
		if d.callPeer != "" {
			return d.callPeer
		}
		if d.mesh != nil {
			peers := d.mesh.ActivePeers()
			if len(peers) == 1 {
				return peers[0]
			}
		}
		return ""
	case phone.StateADD_DIALTONE, phone.StateADD_DIALING,
		phone.StateADD_CALLING, phone.StateADD_PRIVATE,
		phone.StateADD_INTERCEPT, phone.StateCONFERENCE_MERGED:
		return d.callPeer
	default:
		return ""
	}
}

func (d *daemonCallbacks) TearDownPeer(phone string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if cancel, ok := d.meshReporterCancels[phone]; ok {
		cancel()
		delete(d.meshReporterCancels, phone)
	}
	if d.mesh != nil {
		d.mesh.RemovePeer(phone)
	}
	// If the phone being torn down is the current 2-party peer (e.g. an
	// ADD_CALLING target that the server rejected before it could migrate
	// into the mesh), close its PeerManager too so we don't leak a dead PC
	// across retries.
	if d.peerMgr != nil && d.callPeer == phone {
		if err := d.peerMgr.Close(); err != nil {
			slog.Warn("TearDownPeer: peerMgr close failed", "phone", phone, "error", err)
		}
		d.peerMgr = nil
		d.callPeer = ""
	}
	d.mixer.RemoveWebRTCSource(phone)
}

func (d *daemonCallbacks) RequestConferenceMerge(held, active string) {
	d.mu.Lock()
	sig := d.sig
	d.mu.Unlock()
	slog.Info("conference: sending ConferenceMerge to server", "held", held, "active", active)
	sendSignal(sig, &sigclient.Message{
		Type:       sigclient.TypeConferenceMerge,
		HeldPeer:   held,
		ActivePeer: active,
	})
}

// wireMeshRemoteTrack attaches a decode loop to pm's inbound track that feeds
// decoded PCM frames into webrtcCh (the peer's mixer source). role only tags
// log lines ("initiator"/"responder"); the loop body is identical for both.
// pm owns its own decoder, so the loop is safe to run concurrently per peer.
func (d *daemonCallbacks) wireMeshRemoteTrack(pm *owebrtc.PeerManager, phone, role string, webrtcCh chan []int16) {
	pm.OnRemoteTrack = func(track *webrtc.TrackRemote) {
		slog.Info("conference: remote track attached", "phone", phone, "role", role)
		go func() {
			defer recoverGoroutine("conf-remote-track-" + phone)
			gotFirst := false
			for {
				pkt, _, err := track.ReadRTP()
				if err != nil {
					slog.Info("conference: remote track ended", "phone", phone, "role", role)
					return
				}
				if !gotFirst {
					slog.Info("conference: first RTP packet received", "phone", phone, "role", role)
					gotFirst = true
				}
				pcm, err := pm.Decode(pkt.Payload)
				if err != nil {
					continue
				}
				if pm.InboundMuted() {
					// Silent hold: drop decoded audio rather than feeding the mixer.
					continue
				}
				frame := make([]int16, len(pcm))
				copy(frame, pcm)
				select {
				case webrtcCh <- frame:
				default:
					// drop frame if consumer is behind
				}
			}
		}()
	}
}

func (d *daemonCallbacks) AddMeshPeer(phone string, initiator bool) {
	if !initiator {
		// Responder path: don't pre-create the mesh peer. setupMeshResponder
		// will create it when the initiator's SDP offer arrives. Pre-creating
		// here would leave a PC in signaling state 'stable', which causes the
		// TypeSDP dispatch at line ~1855 to mistakenly route the incoming
		// offer as an answer (SetRemote(answer) from stable is an invalid
		// pion transition).
		slog.Info("conference: responder waiting for initiator SDP", "phone", phone)
		return
	}

	d.mu.Lock()
	mesh := d.ensureMesh()
	confID := d.ctrl.ConferenceID()
	sig := d.sig
	d.mu.Unlock()

	slog.Info("conference: adding mesh peer", "phone", phone, "initiator", initiator, "conf_id", confID)

	pm, err := mesh.AddPeer(phone)
	if err != nil {
		slog.Error("conference: add mesh peer failed", "phone", phone, "err", err)
		return
	}

	// Wire remote audio track into the mixer BEFORE any async signaling work.
	// Pion can fire OnTrack during negotiation; setting it after CreateOffer
	// would race against the remote track arriving.
	webrtcCh := d.mixer.AddWebRTCSource(phone)
	d.wireMeshRemoteTrack(pm, phone, "initiator", webrtcCh)

	// Wire ICE candidate forwarding. Gate candidates behind SDP send so the
	// remote side has a local description before processing candidates.
	sdpSent := make(chan struct{})
	pm.OnICECandidate = func(candidate string) {
		<-sdpSent
		sendSignal(sig, &sigclient.Message{
			Type:      sigclient.TypeICE,
			To:        phone,
			ConfID:    confID,
			Candidate: candidate,
		})
	}

	pm.OnConnectionState = d.meshReporterOnConnected(pm, phone)

	if initiator {
		// Initiator creates and sends the SDP offer to the peer.
		offer, err := pm.CreateOffer()
		if err != nil {
			slog.Error("conference: create offer failed", "phone", phone, "err", err)
			close(sdpSent)
			return
		}
		sendSignal(sig, &sigclient.Message{
			Type:   sigclient.TypeSDP,
			To:     phone,
			ConfID: confID,
			SDP:    offer,
		})
		close(sdpSent)
		slog.Info("conference: sent SDP offer to peer", "phone", phone, "conf_id", confID)
	} else {
		// Responder waits for the initiator's offer (handled in TypeSDP dispatch).
		// Ungate ICE candidates immediately since we don't send an offer here.
		close(sdpSent)
	}
}

func (d *daemonCallbacks) RemoveMeshPeer(phone string) {
	slog.Info("conference: removing mesh peer", "phone", phone)
	d.mu.Lock()
	mesh := d.mesh
	if cancel, ok := d.meshReporterCancels[phone]; ok {
		cancel()
		delete(d.meshReporterCancels, phone)
	}
	d.mu.Unlock()
	if mesh != nil {
		mesh.RemovePeer(phone)
	}
	d.mixer.RemoveWebRTCSource(phone)
}

func (d *daemonCallbacks) TearDownAllMeshPeers() {
	d.mu.Lock()
	mesh := d.mesh
	var peers []string
	if mesh != nil {
		peers = mesh.ActivePeers()
	}
	for phone, cancel := range d.meshReporterCancels {
		cancel()
		delete(d.meshReporterCancels, phone)
	}
	d.mu.Unlock()
	slog.Info("conference: tearing down all mesh peers", "count", len(peers), "peers", peers)
	if mesh == nil {
		return
	}
	for _, p := range peers {
		d.mixer.RemoveWebRTCSource(p)
	}
	mesh.CloseAll()
}

// setupMeshResponder creates a mesh peer for an incoming conference SDP offer,
// wires the remote track and ICE candidate handlers, and accepts the offer.
// Returns the answer SDP. Must NOT be called with d.mu held.
func (d *daemonCallbacks) setupMeshResponder(peer, offerSDP, confID string) (string, error) {
	d.mu.Lock()
	mesh := d.ensureMesh()
	d.mu.Unlock()

	pm, err := mesh.AddPeer(peer)
	if err != nil {
		return "", fmt.Errorf("mesh AddPeer: %w", err)
	}

	// Wire remote audio track BEFORE AcceptOffer so pion cannot miss it.
	webrtcCh := d.mixer.AddWebRTCSource(peer)
	d.wireMeshRemoteTrack(pm, peer, "responder", webrtcCh)

	// Gate ICE candidates behind answer SDP send.
	sdpSent := make(chan struct{})
	pm.OnICECandidate = func(candidate string) {
		<-sdpSent
		d.mu.Lock()
		sig := d.sig
		d.mu.Unlock()
		sendSignal(sig, &sigclient.Message{
			Type:      sigclient.TypeICE,
			To:        peer,
			ConfID:    confID,
			Candidate: candidate,
		})
	}

	pm.OnConnectionState = d.meshReporterOnConnected(pm, peer)

	// Use confID from the offer rather than ctrl.ConferenceID() so the answer
	// is correctly routed even if ConferenceMember has not yet arrived.
	answerSDP, err := pm.AcceptOffer(offerSDP)
	if err != nil {
		close(sdpSent)
		return "", fmt.Errorf("AcceptOffer: %w", err)
	}
	close(sdpSent)
	return answerSDP, nil
}
