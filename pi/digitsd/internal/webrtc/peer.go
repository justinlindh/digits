package owebrtc

import (
	"fmt"
	"log/slog"

	"github.com/pion/webrtc/v4"
)

// PeerManager manages a single WebRTC peer connection with an audio track.
type PeerManager struct {
	iceCfg *ICEConfig
	pc     *webrtc.PeerConnection
	track  *webrtc.TrackLocalStaticSample

	// Callbacks (set by caller before use):
	OnRemoteTrack     func(track *webrtc.TrackRemote)
	OnICECandidate    func(candidate string)
	OnConnectionState func(state webrtc.PeerConnectionState)
}

// NewPeerManager creates a PeerManager with the given ICE configuration.
// It creates a PeerConnection, adds a local Opus audio track, and wires up callbacks.
func NewPeerManager(iceCfg *ICEConfig) (*PeerManager, error) {
	m := &PeerManager{iceCfg: iceCfg}

	pc, err := webrtc.NewPeerConnection(iceCfg.WebRTCConfig())
	if err != nil {
		return nil, fmt.Errorf("create peer connection: %w", err)
	}
	m.pc = pc

	// Create local audio track (Opus)
	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"audio",
		"digits",
	)
	if err != nil {
		pc.Close()
		return nil, fmt.Errorf("create local track: %w", err)
	}
	m.track = track

	rtpSender, err := pc.AddTrack(track)
	if err != nil {
		pc.Close()
		return nil, fmt.Errorf("add local track: %w", err)
	}

	// Drain RTCP packets from the sender. Pion requires this — unread RTCP
	// backs up internal buffers and can delay/block RTP delivery.
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := rtpSender.Read(buf); err != nil {
				return
			}
		}
	}()

	// OnTrack: remote track received
	pc.OnTrack(func(t *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if m.OnRemoteTrack != nil {
			m.OnRemoteTrack(t)
		}
	})

	// OnICECandidate: local ICE candidate gathered
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		if m.OnICECandidate != nil {
			m.OnICECandidate(c.ToJSON().Candidate)
		}
	})

	// OnConnectionStateChange: connection state changed
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		slog.Info("webrtc: connection state changed", "state", state)
		if m.OnConnectionState != nil {
			m.OnConnectionState(state)
		}
	})

	return m, nil
}

// CreateOffer creates an SDP offer and sets the local description.
// ICE candidates are NOT gathered inline — they trickle via OnICECandidate.
// The caller MUST send the returned SDP before ICE candidates reach the remote peer.
func (m *PeerManager) CreateOffer() (string, error) {
	return m.createOffer(nil)
}

// CreateRestartOffer creates a new SDP offer with ICE restart requested.
// The existing PeerConnection and media tracks are preserved; only ICE
// credentials are rotated so connectivity can be re-established.
func (m *PeerManager) CreateRestartOffer() (string, error) {
	return m.createOffer(&webrtc.OfferOptions{ICERestart: true})
}

func (m *PeerManager) createOffer(opts *webrtc.OfferOptions) (string, error) {
	offer, err := m.pc.CreateOffer(opts)
	if err != nil {
		return "", fmt.Errorf("create offer: %w", err)
	}

	if err := m.pc.SetLocalDescription(offer); err != nil {
		return "", fmt.Errorf("set local description: %w", err)
	}

	return offer.SDP, nil
}

// AcceptOffer sets the remote description from an incoming offer SDP, creates an
// answer, and sets the local description. ICE candidates trickle via OnICECandidate.
// The caller MUST send the returned answer SDP before forwarding ICE candidates.
// Also used to accept ICE restart offers on an existing PeerConnection.
func (m *PeerManager) AcceptOffer(offerSDP string) (string, error) {
	if err := m.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offerSDP,
	}); err != nil {
		return "", fmt.Errorf("set remote description (offer): %w", err)
	}

	answer, err := m.pc.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("create answer: %w", err)
	}

	if err := m.pc.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("set local description (answer): %w", err)
	}

	return answer.SDP, nil
}

// SetAnswer sets the remote description from an answer SDP. Called by the caller
// after receiving the answer from the callee.
func (m *PeerManager) SetAnswer(answerSDP string) error {
	if err := m.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  answerSDP,
	}); err != nil {
		return fmt.Errorf("set remote description (answer): %w", err)
	}
	return nil
}

// AddICECandidate adds a remote ICE candidate (raw candidate string).
func (m *PeerManager) AddICECandidate(candidate string) error {
	if err := m.pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: candidate}); err != nil {
		return fmt.Errorf("add ICE candidate: %w", err)
	}
	return nil
}

// LocalTrack returns the local audio track.
func (m *PeerManager) LocalTrack() *webrtc.TrackLocalStaticSample {
	return m.track
}

// Close closes the underlying PeerConnection.
func (m *PeerManager) Close() error {
	return m.pc.Close()
}
