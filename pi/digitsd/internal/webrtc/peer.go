package owebrtc

import (
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/justinlindh/digits/pi/digitsd/internal/codec"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// PeerManager manages a single WebRTC peer connection with an audio track.
// Each PeerManager owns its own Opus encoder and decoder so concurrent peers
// in a conference decode independently without sharing mutable buffer state.
type PeerManager struct {
	iceCfg        *ICEConfig
	pc            *webrtc.PeerConnection
	track         *webrtc.TrackLocalStaticSample
	encoder       *codec.Encoder
	decoder       *codec.Decoder
	outboundMuted atomic.Bool
	inboundMuted  atomic.Bool
	zeroBuf       []int16 // reusable zero slice for muted encodes; allocated once at construction

	// Callbacks (set by caller before use):
	OnRemoteTrack     func(track *webrtc.TrackRemote)
	OnICECandidate    func(candidate string)
	OnConnectionState func(state webrtc.PeerConnectionState)
}

// NewPeerManager creates a PeerManager with the given ICE configuration.
// It creates a PeerConnection, adds a local Opus audio track, and wires up callbacks.
// Each PeerManager owns its own Opus encoder (for per-peer outbound muting) and
// decoder (to eliminate shared-buffer races when multiple peers decode concurrently
// in a conference).
func NewPeerManager(iceCfg *ICEConfig) (*PeerManager, error) {
	enc, err := codec.NewEncoder(48000, 1, 24000)
	if err != nil {
		return nil, fmt.Errorf("create opus encoder: %w", err)
	}
	dec, err := codec.NewDecoder(48000, 1)
	if err != nil {
		return nil, fmt.Errorf("create opus decoder: %w", err)
	}
	m := &PeerManager{
		iceCfg:  iceCfg,
		encoder: enc,
		decoder: dec,
		// zeroBuf is pre-allocated to match the 20ms Opus frame size at 48 kHz (mono).
		// It is used in place of a fresh allocation on every muted frame.
		zeroBuf: make([]int16, 960),
	}

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
		if cerr := pc.Close(); cerr != nil {
			slog.Warn("webrtc: close peer connection after track error", "error", cerr)
		}
		return nil, fmt.Errorf("create local track: %w", err)
	}
	m.track = track

	rtpSender, err := pc.AddTrack(track)
	if err != nil {
		if cerr := pc.Close(); cerr != nil {
			slog.Warn("webrtc: close peer connection after add track error", "error", cerr)
		}
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

// SetOutboundMuted toggles per-peer outbound mute. When true, outbound PCM
// frames are replaced with zero samples before encoding. With Opus DTX enabled,
// the receiver renders low-level comfort noise (matches 90s POTS silent hold).
// Safe to call concurrently.
func (m *PeerManager) SetOutboundMuted(v bool) {
	m.outboundMuted.Store(v)
}

// OutboundMuted reports the current per-peer outbound mute state.
func (m *PeerManager) OutboundMuted() bool {
	return m.outboundMuted.Load()
}

// SetInboundMuted toggles per-peer inbound mute. When true, the remote-track
// decode goroutine drops decoded frames instead of feeding them to the mixer,
// so the user does not hear the held party. Used together with SetOutboundMuted
// to implement 90s POTS silent hold (neither party hears the other).
// Safe to call concurrently.
func (m *PeerManager) SetInboundMuted(v bool) {
	m.inboundMuted.Store(v)
}

// InboundMuted reports the current per-peer inbound mute state.
func (m *PeerManager) InboundMuted() bool {
	return m.inboundMuted.Load()
}

// SendPCMFrame encodes a PCM frame with this peer's encoder and writes it to
// the local track. If outbound mute is active, a zero-filled frame is encoded
// instead so the remote end receives Opus comfort noise (DTX SID packets).
// The input slice is never modified; it is safe to pass the same slice to
// multiple peers concurrently.
func (m *PeerManager) SendPCMFrame(frame []int16) {
	toEncode := frame
	if m.outboundMuted.Load() {
		// Use the pre-allocated zero buffer to avoid a heap allocation on every
		// muted frame (50 per second per muted peer). The buffer is guaranteed
		// to be all-zeros because it is never written outside this path.
		toEncode = m.zeroBuf[:len(frame)]
	}
	encoded, err := m.encoder.Encode(toEncode)
	if err != nil {
		return
	}
	m.track.WriteSample(media.Sample{ //nolint:errcheck
		Data:     encoded,
		Duration: 20 * time.Millisecond,
	})
}

// Decode decodes an Opus packet into PCM samples using this peer's dedicated
// decoder. Each PeerManager has its own decoder so concurrent remote-track
// goroutines in a conference don't race on a shared internal buffer.
// Returns a slice of the decoder's internal buffer — valid until the next Decode
// call on the same PeerManager. Caller must copy the data if it needs to retain it.
func (m *PeerManager) Decode(pkt []byte) ([]int16, error) {
	return m.decoder.Decode(pkt)
}

// LocalTrack returns the local audio track.
func (m *PeerManager) LocalTrack() *webrtc.TrackLocalStaticSample {
	return m.track
}

// GetStats returns a snapshot of WebRTC statistics for this peer. Safe to
// call concurrently with audio; Pion's stats collector is thread-safe.
func (m *PeerManager) GetStats() webrtc.StatsReport {
	return m.pc.GetStats()
}

// Close closes the underlying PeerConnection.
func (m *PeerManager) Close() error {
	return m.pc.Close()
}
